package skiplist

import (
	// "github.com/couchbase/indexing/secondary/logging"
	"math/rand"
	"runtime"
	"sync/atomic"
	"unsafe"
	"time"
)

var Debug bool

const MaxLevel = 32
const p = 0.25

type CompareFn func(unsafe.Pointer, unsafe.Pointer) int
type ItemSizeFn func(unsafe.Pointer) int

func defaultItemSize(unsafe.Pointer) int {
	return 0
}

type MallocFn func(int) unsafe.Pointer
type FreeFn func(unsafe.Pointer)

type Config struct {
	ItemSize ItemSizeFn

	UseMemoryMgmt     bool
	Malloc            MallocFn
	Free              FreeFn
	BarrierDestructor BarrierSessionDestructor
}

func (cfg *Config) SetItemSizeFunc(fn ItemSizeFn) {
	cfg.ItemSize = fn
}

func DefaultConfig() Config {
	return Config{
		ItemSize:      defaultItemSize,
		UseMemoryMgmt: false,
	}
}

type Skiplist struct {
	head    *Node
	tail    *Node
	level   int32
	Stats   Stats
	barrier *AccessBarrier

	newNode  func(itm unsafe.Pointer, level int, ib string) *Node
	freeNode func(*Node)

	ic int64

	Config
}

func New() *Skiplist {
	return NewWithConfig(DefaultConfig())
}
var start time.Time
func NewWithConfig(cfg Config) *Skiplist {
	start = time.Now()
	if runtime.GOARCH != "amd64" {
		cfg.UseMemoryMgmt = false
	}

	s := &Skiplist{
		Config:  cfg,
		barrier: newAccessBarrier(cfg.UseMemoryMgmt, cfg.BarrierDestructor),
	}

	s.newNode = func(itm unsafe.Pointer, level int, ib string) *Node {
		return allocNode(itm, level, cfg.Malloc, ib)
	}

	if cfg.UseMemoryMgmt {
		s.freeNode = func(n *Node) {
			if Debug {
				debugMarkFree(n)
			}
			cfg.Free(unsafe.Pointer(n))
		}
	} else {
		s.freeNode = func(*Node) {}
	}

	head := s.newNode(nil, MaxLevel, "HEAD")
	tail := s.newNode(nil, MaxLevel, "TAIL")

	for i := 0; i <= MaxLevel; i++ {
		head.setNext(i, tail, false)
		tail.setNext(i, nil, false)
	}

	s.head = head
	s.tail = tail

	return s
}

func (s *Skiplist) GetAccesBarrier() *AccessBarrier {
	return s.barrier
}

func (s *Skiplist) FreeNode(n *Node, sts *Stats) {
	s.freeNode(n)
	sts.AddInt64(&sts.nodeFrees, 1)
}

type ActionBuffer struct {
	preds []*Node
	succs []*Node
}

func (s *Skiplist) MakeBuf() *ActionBuffer {
	return &ActionBuffer{
		preds: make([]*Node, MaxLevel+1),
		succs: make([]*Node, MaxLevel+1),
	}
}

func (s *Skiplist) FreeBuf(b *ActionBuffer) {
}

func (s *Skiplist) Size(n *Node) int {
	return s.ItemSize(n.Item()) + n.Size()
}

func (s *Skiplist) NewLevel(randFn func() float32) int {
	var nextLevel int

	// TODO: Use exponential distribution to generate random number and use it directly as the level?
	// Shouldn't this be capped to prevent infinite loop?
	for ; randFn() < p; nextLevel++ {
	}

	if nextLevel > MaxLevel {
		nextLevel = MaxLevel
	}

	level := int(atomic.LoadInt32(&s.level))
	if nextLevel > level {
		if atomic.CompareAndSwapInt32(&s.level, int32(level), int32(level+1)) {
			nextLevel = level + 1
		} else {
			nextLevel = level
		}
	}

	return nextLevel
}

func (s *Skiplist) helpDelete(level int, prev, curr, next *Node, sts *Stats) bool {
	success := prev.dcasNext(level, curr, next, false, false)
	if success && level == 0 {
		sts.AddInt64(&sts.softDeletes, -1)
		sts.AddInt64(&sts.levelNodesCount[curr.Level()], -1)
		sts.AddInt64(&sts.usedBytes, -int64(s.Size(curr)))
	}
	return success
}

func (s *Skiplist) findPath(itm unsafe.Pointer, cmp CompareFn,
	buf *ActionBuffer, sts *Stats) (foundNode *Node) {
	var cmpVal int = 1

retry:
	prev := s.head
	level := int(atomic.LoadInt32(&s.level))
	for i := level; i >= 0; i-- {
		curr, _ := prev.getNext(i)
	levelSearch:
		for {
			next, deleted := curr.getNext(i)
			for deleted {
				if !s.helpDelete(i, prev, curr, next, sts) {
					sts.AddUint64(&sts.readConflicts, 1)
					goto retry
				}

				curr, _ = prev.getNext(i)
				next, deleted = curr.getNext(i)
			}

			cmpVal = compare(cmp, curr.Item(), itm)
			if cmpVal < 0 {
				prev = curr
				curr = next
			} else {
				break levelSearch
			}
		}

		buf.preds[i] = prev
		buf.succs[i] = curr
	}

	if cmpVal == 0 {
		foundNode = buf.succs[0]
	}
	return
}

func (s *Skiplist) Insert(itm unsafe.Pointer, cmp CompareFn,
	buf *ActionBuffer, sts *Stats) (success bool) {
	_, success = s.Insert2(itm, cmp, nil, buf, rand.Float32, sts, "fromInsert")
	return
}

func (s *Skiplist) Insert2(itm unsafe.Pointer, inscmp CompareFn, eqCmp CompareFn,
	buf *ActionBuffer, randFn func() float32, sts *Stats, ib string) (*Node, bool) {
	itemLevel := s.NewLevel(randFn)
	return s.Insert3(itm, inscmp, eqCmp, buf, itemLevel, false, sts, ib)
}

func (s *Skiplist) Insert3(itm unsafe.Pointer, insCmp CompareFn, eqCmp CompareFn,
	buf *ActionBuffer, itemLevel int, skipFindPath bool, sts *Stats, ib string) (*Node, bool) {

	// cc := atomic.AddInt64(&s.ic, 1)

	token := s.barrier.Acquire()
	defer s.barrier.Release(token)

	x := s.newNode(itm, itemLevel, ib)

retry:
	if skipFindPath {
		skipFindPath = false
	} else {
		if s.findPath(itm, insCmp, buf, sts) != nil ||
			eqCmp != nil && compare(eqCmp, itm, buf.preds[0].Item()) == 0 {

			s.freeNode(x)
			return nil, false
		}
	}

	// logging.Infof("amd: Insert3 colled [%d] times", cc)

	// One of the succs is getting either one of the preds somehow or some other pointer from behind?
	// TODO: just low prob, make it happen and see if we get large files?
	
	// if 10000 == cc {
	// 	nextNode := buf.succs[0]
	// 	if itemLevel > 0 {
	// 	// if rand.Float32() > 0.9999 && itemLevel > 0 {
	// 	// if time.Since(start) > time.Minute && itemLevel > 0 {
	// 		ri := rand.Int()
	// 		pl := itemLevel - 1 - (ri % itemLevel)
	// 		logging.Infof("amd: XXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXX [%d], ri[%d], il[%d]", pl, ri, itemLevel)
 	// 		nextNode = buf.preds[pl]
	// 	}
	// 	buf.succs[0] = nextNode
	// }

	// // Set all next links for the node non-atomically
	// for i := 0; i <= int(itemLevel); i++ {
	// 	if false && i==0 && itemLevel > 0 && 10000 == cc {
	// 		ri := rand.Int()
	// 		pl := itemLevel - 1 - (ri % itemLevel)
	// 		logging.Infof("amd: XAAXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXX [%d], ri[%d], il[%d]", pl, ri, itemLevel)
 	// 		nextNode := buf.preds[pl]
	// 		x.setNext(i, nextNode, false)
	// 	} else {
	// 		x.setNext(i, buf.succs[i], false)
	// 	}
	// }

	// Set all next links for the node non-atomically
	for i := 0; i <= int(itemLevel); i++ {
		x.setNext(i, buf.succs[i], false)
	}

	// Now node is part of the skiplist
	if !buf.preds[0].dcasNext(0, buf.succs[0], x, false, false) {
		sts.AddUint64(&sts.insertConflicts, 1)
		goto retry
	}

	// Add to index levels
	for i := 1; i <= int(itemLevel); i++ {
	fixThisLevel:
		for {
			nodeNext, deleted := x.getNext(i)
			next := buf.succs[i]

			// Update the node's next pointer at current level if required.
			// This is the only thread which can modify next pointer at this level
			// The dcas operation can fail only if another thread marked delete
			if deleted || (nodeNext != next && !x.dcasNext(i, nodeNext, next, false, false)) {
				goto finished
			}

			if buf.preds[i].dcasNext(i, next, x, false, false) {
				break fixThisLevel
			}

			s.findPath(itm, insCmp, buf, sts)
		}
	}

finished:
	sts.AddInt64(&sts.nodeAllocs, 1)
	sts.AddInt64(&sts.levelNodesCount[itemLevel], 1)
	sts.AddInt64(&sts.usedBytes, int64(s.Size(x)))
	return x, true
}

func GetCallerName() string {
	pc, _, _, ok := runtime.Caller(2)
	details := runtime.FuncForPC(pc)
	if ok && details != nil {
		return details.Name()
	}
	return "gg"
}

// var cc int64
func (s *Skiplist) softDelete(delNode *Node, sts *Stats) bool {
	var marked bool

	// c := atomic.AddInt64(&cc, 1)

	targetLevel := delNode.Level()
	for i := targetLevel; i >= 0; i-- {
		next, deleted := delNode.getNext(i)
		for !deleted {
			if delNode.dcasNext(i, next, next, false, true) {
				if i == 0 {
					sts.AddInt64(&sts.softDeletes, 1)
					marked = true
				}
			} else if i == 0 {
				// logging.Infof("amd: -----MARKING FAILED----- %d", c)
			}

			next, deleted = delNode.getNext(i)
		}
	}
	if !marked {
		// logging.Infof("amd: marking realllly failed------ %d", c)
	}
	return marked
}

func (s *Skiplist) Delete(itm unsafe.Pointer, cmp CompareFn,
	buf *ActionBuffer, sts *Stats) bool {
	token := s.barrier.Acquire()
	defer s.barrier.Release(token)

	found := s.findPath(itm, cmp, buf, sts) != nil
	if !found {
		return false
	}

	delNode := buf.succs[0]
	return s.deleteNode(delNode, cmp, buf, sts)
}

func (s *Skiplist) DeleteNode(n *Node, cmp CompareFn,
	buf *ActionBuffer, sts *Stats) bool {
	// logging.Infof("amd: DeleteNode called from [%s] [%s]", GetCallerName(), n.Item())
	token := s.barrier.Acquire()
	defer s.barrier.Release(token)

	return s.deleteNode(n, cmp, buf, sts)
}

func (s *Skiplist) deleteNode(n *Node, cmp CompareFn, buf *ActionBuffer, sts *Stats) bool {
	itm := n.Item()
	if s.softDelete(n, sts) {
		s.findPath(itm, cmp, buf, sts)
		return true
	}

	return false
}

// Explicit barrier and release should be used by the caller before
// and after this function call
func (s *Skiplist) GetRangeSplitItems(nways int) []unsafe.Pointer {
	var deleted bool
repeat:
	var itms []unsafe.Pointer
	var finished bool

	l := int(atomic.LoadInt32(&s.level))
	for ; l >= 0; l-- {
		c := int(atomic.LoadInt64(&s.Stats.levelNodesCount[l]) + 1)
		if c >= nways {
			perSplit := c / nways
			node := s.head
			for j := 0; node != s.tail && !finished; j++ {
				if j == perSplit {
					j = -1
					itms = append(itms, node.Item())
					finished = len(itms) == nways-1
				}

				node, deleted = node.getNext(l)
				if deleted {
					goto repeat
				}
			}

			break
		}
	}

	return itms
}

func (s *Skiplist) HeadNode() *Node {
	return s.head
}

func (s *Skiplist) TailNode() *Node {
	return s.tail
}
