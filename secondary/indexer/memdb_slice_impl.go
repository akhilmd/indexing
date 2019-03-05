// Copyright (c) 2014 Couchbase, Inc.
// Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file
// except in compliance with the License. You may obtain a copy of the License at
//   http://www.apache.org/licenses/LICENSE-2.0
// Unless required by applicable law or agreed to in writing, software distributed under the
// License is distributed on an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND,
// either express or implied. See the License for the specific language governing permissions
// and limitations under the License.

package indexer

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"hash/crc32"
	"io/ioutil"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"github.com/couchbase/indexing/secondary/common"
	"github.com/couchbase/indexing/secondary/common/queryutil"
	"github.com/couchbase/indexing/secondary/logging"
	"github.com/couchbase/indexing/secondary/memdb"
	"github.com/couchbase/indexing/secondary/memdb/nodetable"
	"github.com/couchbase/indexing/secondary/memdb/skiplist"
	"github.com/couchbase/indexing/secondary/stubs/nitro/mm"
)

const (
	opInsert = iota
	opUpdate
	opDelete
)

const tmpDirName = ".tmp"

type indexMutation struct {
	op    int
	key   []byte
	docid []byte
	meta  *MutationMeta
}

func docIdFromEntryBytes(e []byte) []byte {
	entryLen := len(e)

	offset := entryLen - 2
	l := binary.LittleEndian.Uint16(e[offset : offset+2])

	// Length & 00111111 11111111 (as MSB and 2nd MSB of length is used to indicate presence of count and expiry)
	docidlen := int(l & 0x3fff)

	// sub docid and the len of docid
	offset = entryLen - docidlen - 2

	// if count is encoded, subtract another 2 bytes
	if (e[entryLen - 1] & 0x80) == 0x80 {
		offset -= 2
	}

	// if expiry is encoded, subtract another 4 bytes
	if (e[entryLen - 1] & 0x40) == 0x40 {
		offset -= 4
	}

	return e[offset:offset+docidlen]
}

func entryBytesFromDocId(docid []byte) []byte {
	l := len(docid)
	entry := make([]byte, l+2)
	copy(entry, docid)
	binary.LittleEndian.PutUint16(entry[l:l+2], uint16(l))
	return entry
}

func vbucketFromEntryBytes(e []byte, numVbuckets int) int {
	docid := docIdFromEntryBytes(e)
	hash := crc32.ChecksumIEEE(docid)
	return int((hash >> 16) & uint32(numVbuckets-1))
}

func hashDocId(entry []byte) uint32 {
	return crc32.ChecksumIEEE(docIdFromEntryBytes(entry))
}

func nodeEquality(p unsafe.Pointer, entry []byte) bool {
	node := (*skiplist.Node)(p)
	docid1 := docIdFromEntryBytes(entry)
	itm := (*memdb.Item)(node.Item())
	docid2 := docIdFromEntryBytes(itm.Bytes())
	return bytes.Equal(docid1, docid2)
}

func byteItemCompare(a, b []byte) int {
	return bytes.Compare(a, b)
}

func resizeEncodeBuf(encodeBuf []byte, keylen int, doResize bool) []byte {
	if doResize && keylen+MAX_KEY_EXTRABYTES_LEN > cap(encodeBuf) {
		// TODO: Shrink the buffer periodically or as needed
		newSize := keylen + MAX_KEY_EXTRABYTES_LEN + ENCODE_BUF_SAFE_PAD
		encodeBuf = make([]byte, 0, newSize)
	}
	return encodeBuf
}

func resizeArrayBuf(arrayBuf []byte, keylen int) []byte {
	if allowLargeKeys && keylen > cap(arrayBuf) {
		newSize := keylen + MAX_KEY_EXTRABYTES_LEN + ENCODE_BUF_SAFE_PAD
		arrayBuf = make([]byte, 0, newSize)
	}
	return arrayBuf
}

var totalMemDBItems int64 = 0

type memdbSlice struct {
	get_bytes, insert_bytes, delete_bytes int64
	flushedCount                          uint64
	committedCount                        uint64
	qCount                                int64

	path string
	id   SliceId

	refCount int
	lock     sync.RWMutex

	mainstore *memdb.MemDB

	// MemDB writers
	main []*memdb.Writer

	// One local table per writer
	// Each table is only operated by the writer owner
	back []*nodetable.NodeTable

	idxDefn    common.IndexDefn
	idxDefnId  common.IndexDefnId
	idxInstId  common.IndexInstId
	idxPartnId common.PartitionId

	status        SliceStatus
	isActive      bool
	isDirty       bool
	isPrimary     bool
	isSoftDeleted bool
	isSoftClosed  bool

	cmdCh  []chan indexMutation
	stopCh []DoneChannel

	workerDone []chan bool

	fatalDbErr error

	numWriters     int
	maxRollbacks   int
	hasPersistence bool

	totalFlushTime  time.Duration
	totalCommitTime time.Duration

	idxStats *IndexStats
	sysconf  common.Config
	confLock sync.RWMutex

	isPersistorActive int32

	lastRollbackTs *common.TsVbuuid

	// Array processing
	arrayExprPosition int
	isArrayDistinct   bool

	encodeBuf [][]byte
	arrayBuf  [][]byte
}

func NewMemDBSlice(path string, sliceId SliceId, idxDefn common.IndexDefn,
	idxInstId common.IndexInstId, partitionId common.PartitionId,
	isPrimary bool, hasPersistance bool, numPartitions int,
	sysconf common.Config, idxStats *IndexStats) (*memdbSlice, error) {

	info, err := os.Stat(path)
	if err != nil || err == nil && info.IsDir() {
		os.Mkdir(path, 0777)
	}

	slice := &memdbSlice{}
	slice.idxStats = idxStats
	slice.idxStats.residentPercent.Set(100)
	slice.idxStats.cacheHitPercent.Set(100)

	slice.get_bytes = 0
	slice.insert_bytes = 0
	slice.delete_bytes = 0
	slice.flushedCount = 0
	slice.committedCount = 0
	slice.sysconf = sysconf
	slice.path = path
	slice.idxInstId = idxInstId
	slice.idxDefnId = idxDefn.DefnId
	slice.idxDefn = idxDefn
	slice.idxPartnId = partitionId
	slice.id = sliceId
	slice.numWriters = sysconf["numSliceWriters"].Int()
	slice.maxRollbacks = sysconf["settings.moi.recovery.max_rollbacks"].Int()

	sliceBufSize := sysconf["settings.sliceBufSize"].Uint64()
	if sliceBufSize < uint64(slice.numWriters) {
		sliceBufSize = uint64(slice.numWriters)
	}

	slice.encodeBuf = make([][]byte, slice.numWriters)
	if idxDefn.IsArrayIndex {
		slice.arrayBuf = make([][]byte, slice.numWriters)
	}
	slice.cmdCh = make([]chan indexMutation, slice.numWriters)

	for i := 0; i < slice.numWriters; i++ {
		slice.cmdCh[i] = make(chan indexMutation, sliceBufSize/uint64(slice.numWriters))
		slice.encodeBuf[i] = make([]byte, 0, maxIndexEntrySize+ENCODE_BUF_SAFE_PAD)
		if idxDefn.IsArrayIndex {
			slice.arrayBuf[i] = make([]byte, 0, maxArrayIndexEntrySize+ENCODE_BUF_SAFE_PAD)
		}
	}
	slice.workerDone = make([]chan bool, slice.numWriters)
	slice.stopCh = make([]DoneChannel, slice.numWriters)

	slice.isPrimary = isPrimary
	slice.hasPersistence = hasPersistance

	// Check if there is a storage corruption error
	err = slice.checkStorageCorruptionError()
	if err != nil {
		logging.Errorf("memdbSlice:NewMemDBSlice Id %v IndexInstId %v "+
			"fatal error occured: %v", sliceId, idxInstId, err)
		return nil, err
	}

	slice.initStores()

	// Array related initialization
	_, slice.isArrayDistinct, slice.arrayExprPosition, err = queryutil.GetArrayExpressionPosition(idxDefn.SecExprs)
	if err != nil {
		return nil, err
	}

	logging.Infof("MemDBSlice:NewMemDBSlice Created New Slice Id %v IndexInstId %v PartitionId %v "+
		"WriterThreads %v Persistence %v", sliceId, idxInstId, partitionId, slice.numWriters, slice.hasPersistence)

	for i := 0; i < slice.numWriters; i++ {
		slice.stopCh[i] = make(DoneChannel)
		slice.workerDone[i] = make(chan bool)
		go slice.handleCommandsWorker(i)
	}

	slice.setCommittedCount()
	return slice, nil
}

var (
	moiWriterSemaphoreCh chan bool
	moiWritersAllowed    int
	initLock             sync.Mutex
)

func updateMOIWriters(to int) {
	if to > cap(moiWriterSemaphoreCh) {
		to = cap(moiWriterSemaphoreCh)
	}
	initLock.Lock()
	defer initLock.Unlock()
	if to == moiWritersAllowed {
		return
	}
	if to < moiWritersAllowed {
		for i := to; i < moiWritersAllowed; i++ {
			moiWriterSemaphoreCh <- true
		}
	} else {
		for i := moiWritersAllowed; i < to; i++ {
			<-moiWriterSemaphoreCh
		}
	}
	moiWritersAllowed = to
}

func init() {
	moiWritersAllowed = runtime.NumCPU() * 4
	moiWriterSemaphoreCh = make(chan bool, moiWritersAllowed)
}

func (slice *memdbSlice) initStores() {
	cfg := memdb.DefaultConfig()
	if slice.sysconf["moi.useMemMgmt"].Bool() {
		cfg.UseMemoryMgmt(mm.Malloc, mm.Free)
	}

	if slice.sysconf["moi.useDeltaInterleaving"].Bool() {
		cfg.UseDeltaInterleaving()
	}

	cfg.SetKeyComparator(byteItemCompare)
	slice.mainstore = memdb.NewWithConfig(cfg)
	slice.main = make([]*memdb.Writer, slice.numWriters)
	for i := 0; i < slice.numWriters; i++ {
		slice.main[i] = slice.mainstore.NewWriter()
	}

	if !slice.isPrimary {
		slice.back = make([]*nodetable.NodeTable, slice.numWriters)
		for i := 0; i < slice.numWriters; i++ {
			slice.back[i] = nodetable.New(hashDocId, nodeEquality)
		}
	}
}

func (mdb *memdbSlice) checkStorageCorruptionError() error {
	if data, err := ioutil.ReadFile(filepath.Join(mdb.path, "error")); err == nil {
		if string(data) == fmt.Sprintf("%v", errStorageCorrupted) {
			return errStorageCorrupted
		}
	}
	return nil
}

func (mdb *memdbSlice) IncrRef() {
	mdb.lock.Lock()
	defer mdb.lock.Unlock()

	mdb.refCount++
}

func (mdb *memdbSlice) DecrRef() {
	mdb.lock.Lock()
	defer mdb.lock.Unlock()

	mdb.refCount--
	if mdb.refCount == 0 {
		if mdb.isSoftClosed {
			go tryClosememdbSlice(mdb)
		}
		if mdb.isSoftDeleted {
			tryDeletememdbSlice(mdb)
		}
	}
}

func (mdb *memdbSlice) Insert(key []byte, docid []byte, meta *MutationMeta) error {
	mut := indexMutation{
		op:    opUpdate,
		key:   key,
		docid: docid,
		meta:  meta,
	}
	atomic.AddInt64(&mdb.qCount, 1)
	mdb.cmdCh[int(meta.vbucket)%mdb.numWriters] <- mut
	mdb.idxStats.numDocsFlushQueued.Add(1)
	return mdb.fatalDbErr
}

func (mdb *memdbSlice) Delete(docid []byte, meta *MutationMeta) error {
	mdb.idxStats.numDocsFlushQueued.Add(1)
	atomic.AddInt64(&mdb.qCount, 1)
	mdb.cmdCh[int(meta.vbucket)%mdb.numWriters] <- indexMutation{op: opDelete, docid: docid}
	return mdb.fatalDbErr
}

func (mdb *memdbSlice) handleCommandsWorker(workerId int) {
	var start time.Time
	var elapsed time.Duration
	var icmd indexMutation

loop:
	for {
		var nmut int
		select {
		case icmd = <-mdb.cmdCh[workerId]:
			switch icmd.op {
			case opUpdate:
				start = time.Now()
				nmut = mdb.insert(icmd.key, icmd.docid, workerId, icmd.meta)
				elapsed = time.Since(start)
				mdb.totalFlushTime += elapsed

			case opDelete:
				start = time.Now()
				nmut = mdb.delete(icmd.docid, workerId)
				elapsed = time.Since(start)
				mdb.totalFlushTime += elapsed

			default:
				logging.Errorf("MemDBSlice::handleCommandsWorker \n\tSliceId %v IndexInstId %v PartitionId %v Received "+
					"Unknown Command %v", mdb.id, mdb.idxInstId, mdb.idxPartnId, logging.TagUD(icmd))
			}

			mdb.idxStats.numItemsFlushed.Add(int64(nmut))
			mdb.idxStats.numDocsIndexed.Add(1)
			atomic.AddInt64(&mdb.qCount, -1)

		case <-mdb.stopCh[workerId]:
			mdb.stopCh[workerId] <- true
			break loop

		case <-mdb.workerDone[workerId]:
			mdb.workerDone[workerId] <- true

		}
	}
}

func (mdb *memdbSlice) insert(key []byte, docid []byte, workerId int, meta *MutationMeta) int {
	var nmut int

	if mdb.isPrimary {
		nmut = mdb.insertPrimaryIndex(key, docid, workerId)
	} else if len(key) == 0 {
		nmut = mdb.delete(docid, workerId)
	} else {
		if mdb.idxDefn.IsArrayIndex {
			nmut = mdb.insertSecArrayIndex(key, docid, workerId, meta)
		} else {
			nmut = mdb.insertSecIndex(key, docid, workerId, meta)
		}
	}

	mdb.logWriterStat()
	return nmut
}

func (mdb *memdbSlice) insertPrimaryIndex(key []byte, docid []byte, workerId int) int {

	entry, err := NewPrimaryIndexEntry(docid)
	common.CrashOnError(err)
	t0 := time.Now()
	mdb.main[workerId].Put(entry)
	mdb.idxStats.Timings.stKVSet.Put(time.Now().Sub(t0))
	atomic.AddInt64(&mdb.insert_bytes, int64(len(entry)))
	mdb.isDirty = true
	return 1
}

func (mdb *memdbSlice) insertSecIndex(key []byte, docid []byte, workerId int, meta *MutationMeta) int {

	// 1. Insert entry into main index
	// 2. Upsert into backindex with docid, mainnode pointer
	// 3. Delete old entry from main index if back index had
	// a previous mainnode pointer entry
	t0 := time.Now()

	var expiry uint32
	if mdb.idxDefn.TTL > 0 {
		expiry = uint32(common.CalcAbsNow()/1000000000) + mdb.idxDefn.TTL
		logging.Infof("amd: TTL set! expiry timestamp = [%d]", expiry)
	}

	mdb.encodeBuf[workerId] = resizeEncodeBuf(mdb.encodeBuf[workerId], len(key), allowLargeKeys)
	entry, err := NewSecondaryIndexEntry(key, docid, mdb.idxDefn.IsArrayIndex,
		1, expiry, mdb.idxDefn.Desc, mdb.encodeBuf[workerId], meta)
	if err != nil {
		logging.Errorf("MemDBSlice::insertSecIndex Slice Id %v IndexInstId %v PartitionId %v "+
			"Skipping docid:%s (%v)", mdb.Id, mdb.idxInstId, mdb.idxPartnId, logging.TagStrUD(docid), err)
		return mdb.deleteSecIndex(docid, workerId)
	}

	newNode := mdb.main[workerId].Put2(entry)
	mdb.idxStats.Timings.stKVSet.Put(time.Now().Sub(t0))
	atomic.AddInt64(&mdb.insert_bytes, int64(len(docid)+len(entry)))

	// Insert succeeded. Failure means same entry already exist.
	if newNode != nil {
		if updated, oldNode := mdb.back[workerId].Update(entry, unsafe.Pointer(newNode)); updated {
			t0 := time.Now()
			mdb.main[workerId].DeleteNode((*skiplist.Node)(oldNode))
			mdb.idxStats.Timings.stKVDelete.Put(time.Since(t0))
			atomic.AddInt64(&mdb.delete_bytes, int64(len(docid)))
		}
	}

	mdb.isDirty = true
	return 1
}

func (mdb *memdbSlice) insertSecArrayIndex(keys []byte, docid []byte, workerId int,
	meta *MutationMeta) int {

	mdb.arrayBuf[workerId] = resizeArrayBuf(mdb.arrayBuf[workerId], len(keys)*3)

	if !allowLargeKeys && len(keys) > maxArrayIndexEntrySize {
		logging.Errorf("MemDBSlice::insertSecArrayIndex Error indexing docid: %s in Slice: %v. Error: Encoded array key (size %v) too long (> %v). Skipped.",
			logging.TagStrUD(docid), mdb.id, len(keys), maxArrayIndexEntrySize)
		return mdb.deleteSecArrayIndex(docid, workerId)
	}

	var nmut int
	newEntriesBytes, newKeyCount, newbufLen, err := ArrayIndexItems(keys, mdb.arrayExprPosition,
		mdb.arrayBuf[workerId], mdb.isArrayDistinct, !allowLargeKeys)
	mdb.arrayBuf[workerId] = resizeArrayBuf(mdb.arrayBuf[workerId], newbufLen)
	if err != nil {
		logging.Errorf("MemDBSlice::insert Error indexing docid: %s in Slice: %v. Error in creating "+
			"compostite new secondary keys %v. Skipped.", logging.TagStrUD(docid), mdb.id, err)
		return mdb.deleteSecArrayIndex(docid, workerId)
	}

	// Get old back index entry
	lookupentry := entryBytesFromDocId(docid)

	// Remove should be done before performing delete of nodes (SMR)
	// Otherwise, by the time back update happens the pointing node
	// may be freed and update operation may crash.
	_, ptr := mdb.back[workerId].Remove(lookupentry)

	list := memdb.NewNodeList((*skiplist.Node)(ptr))
	oldEntriesBytes := list.Keys()
	oldKeyCount := make([]int, len(oldEntriesBytes))
	for i, _ := range oldEntriesBytes {
		e := secondaryIndexEntry(oldEntriesBytes[i])
		oldKeyCount[i] = e.Count()
		oldEntriesBytes[i] = oldEntriesBytes[i][:e.lenKey()]
	}

	//get keys in original form
	if mdb.idxDefn.Desc != nil {
		for _, item := range oldEntriesBytes {
			jsonEncoder.ReverseCollate(item, mdb.idxDefn.Desc)
		}

	}

	entryBytesToBeAdded, entryBytesToDeleted := CompareArrayEntriesWithCount(newEntriesBytes, oldEntriesBytes, newKeyCount, oldKeyCount)
	nmut = 0

	emptyList := func() int {
		entriesToRemove := list.Keys()
		for _, item := range entriesToRemove {
			node := list.Remove(item)
			mdb.main[workerId].DeleteNode(node)
		}
		mdb.isDirty = true
		return 0
	}

	//convert to storage format
	if mdb.idxDefn.Desc != nil {
		for _, item := range list.Keys() {
			jsonEncoder.ReverseCollate(item, mdb.idxDefn.Desc)
		}

	}

	// Delete each entry in entryBytesToDeleted
	for i, item := range entryBytesToDeleted {
		if item != nil { // nil item indicates it should not be deleted
			mdb.encodeBuf[workerId] = resizeEncodeBuf(mdb.encodeBuf[workerId], len(item), true)
			entry, err := NewSecondaryIndexEntry2(item, docid, false,
				oldKeyCount[i], 0, nil, mdb.encodeBuf[workerId][:0], false, nil)
			if err != nil {
				logging.Errorf("MemDBSlice::insertSecArrayIndex Slice Id %v IndexInstId %v PartitionId %v "+
					"Skipping docid:%s (%v)", mdb.Id, mdb.idxInstId, mdb.idxPartnId, logging.TagStrUD(docid), err)
				return emptyList()
			}
			node := list.Remove(entry)
			mdb.main[workerId].DeleteNode(node)
			nmut++
		}
	}

	// Insert each entry in entryBytesToBeAdded
	for i, key := range entryBytesToBeAdded {
		if key != nil { // nil item indicates it should not be added
			t0 := time.Now()
			mdb.encodeBuf[workerId] = resizeEncodeBuf(mdb.encodeBuf[workerId], len(key), allowLargeKeys)
			entry, err := NewSecondaryIndexEntry(key, docid, false,
				newKeyCount[i], 0, mdb.idxDefn.Desc, mdb.encodeBuf[workerId][:0], meta)
			if err != nil {
				logging.Errorf("MemDBSlice::insertSecArrayIndex Slice Id %v IndexInstId %v PartitionId %v "+
					"Skipping docid:%s (%v)", mdb.Id, mdb.idxInstId, mdb.idxPartnId, logging.TagStrUD(docid), err)
				return emptyList()
			}
			newNode := mdb.main[workerId].Put2(entry)
			nmut++
			if newNode != nil { // Ignore if duplicate key
				list.Add(newNode)
				mdb.idxStats.Timings.stKVSet.Put(time.Now().Sub(t0))
				atomic.AddInt64(&mdb.insert_bytes, int64(len(entry)))
			}
		}
	}

	// Update back index entry
	mdb.back[workerId].Update(lookupentry, unsafe.Pointer(list.Head()))
	mdb.isDirty = true
	return nmut
}

func (mdb *memdbSlice) delete(docid []byte, workerId int) int {
	var nmut int

	if mdb.isPrimary {
		nmut = mdb.deletePrimaryIndex(docid, workerId)
	} else if !mdb.idxDefn.IsArrayIndex {
		nmut = mdb.deleteSecIndex(docid, workerId)
	} else {
		nmut = mdb.deleteSecArrayIndex(docid, workerId)
	}

	mdb.logWriterStat()
	return nmut
}

func (mdb *memdbSlice) deletePrimaryIndex(docid []byte, workerId int) (nmut int) {
	if docid == nil {
		common.CrashOnError(errors.New("Nil Primary Key"))
		return
	}

	// docid -> key format
	entry, err := NewPrimaryIndexEntry(docid)
	common.CrashOnError(err)

	// Delete from main index
	t0 := time.Now()
	itm := entry.Bytes()
	mdb.main[workerId].Delete(itm)
	mdb.idxStats.Timings.stKVDelete.Put(time.Now().Sub(t0))
	atomic.AddInt64(&mdb.delete_bytes, int64(len(entry.Bytes())))
	mdb.isDirty = true
	return 1

}

func (mdb *memdbSlice) deleteSecIndex(docid []byte, workerId int) int {
	lookupentry := entryBytesFromDocId(docid)

	// Delete entry from back and main index if present
	t0 := time.Now()
	success, node := mdb.back[workerId].Remove(lookupentry)
	if success {
		mdb.idxStats.Timings.stKVDelete.Put(time.Since(t0))
		atomic.AddInt64(&mdb.delete_bytes, int64(len(docid)))
		t0 = time.Now()
		mdb.main[workerId].DeleteNode((*skiplist.Node)(node))
		mdb.idxStats.Timings.stKVDelete.Put(time.Since(t0))
	}
	mdb.isDirty = true
	return 1
}

func (mdb *memdbSlice) deleteSecArrayIndex(docid []byte, workerId int) (nmut int) {
	// Get old back index entry
	lookupentry := entryBytesFromDocId(docid)
	ptr := (*skiplist.Node)(mdb.back[workerId].Get(lookupentry))
	if ptr == nil {
		return
	}
	list := memdb.NewNodeList(ptr)
	oldEntriesBytes := list.Keys()

	t0 := time.Now()
	mdb.back[workerId].Remove(lookupentry)
	mdb.idxStats.Timings.stKVDelete.Put(time.Since(t0))
	atomic.AddInt64(&mdb.delete_bytes, int64(len(lookupentry)))

	// Delete each entry in oldEntriesBytes
	for _, item := range oldEntriesBytes {
		node := list.Remove(item)
		mdb.main[workerId].DeleteNode(node)
	}

	mdb.isDirty = true

	return len(oldEntriesBytes)
}

//checkFatalDbError checks if the error returned from DB
//is fatal and stores it. This error will be returned
//to caller on next DB operation
func (mdb *memdbSlice) checkFatalDbError(err error) {

	//panic on all DB errors and recover rather than risk
	//inconsistent db state
	common.CrashOnError(err)

	errStr := err.Error()
	switch errStr {

	case "checksum error", "file corruption", "no db instance",
		"alloc fail", "seek fail", "fsync fail":
		mdb.fatalDbErr = err

	}

}

type memdbSnapshotInfo struct {
	Ts       *common.TsVbuuid
	MainSnap *memdb.Snapshot `json:"-"`

	Committed bool `json:"-"`
	dataPath  string
}

type memdbSnapshot struct {
	slice      *memdbSlice
	idxDefnId  common.IndexDefnId
	idxInstId  common.IndexInstId
	idxPartnId common.PartitionId
	ts         *common.TsVbuuid
	info       *memdbSnapshotInfo
	committed  bool

	refCount int32
}

// Creates an open snapshot handle from snapshot info
// Snapshot info is obtained from NewSnapshot() or GetSnapshots() API
// Returns error if snapshot handle cannot be created.
func (mdb *memdbSlice) OpenSnapshot(info SnapshotInfo) (Snapshot, error) {
	var err error
	snapInfo := info.(*memdbSnapshotInfo)

	s := &memdbSnapshot{slice: mdb,
		idxDefnId:  mdb.idxDefnId,
		idxInstId:  mdb.idxInstId,
		idxPartnId: mdb.idxPartnId,
		info:       info.(*memdbSnapshotInfo),
		ts:         snapInfo.Timestamp(),
		committed:  info.IsCommitted(),
	}

	s.Open()
	s.slice.IncrRef()
	if s.committed && mdb.hasPersistence {
		s.info.MainSnap.Open()
		go mdb.doPersistSnapshot(s)
	}

	if s.info.MainSnap == nil {
		err = mdb.loadSnapshot(s.info)
	}

	if info.IsCommitted() {
		logging.Infof("MemDBSlice::OpenSnapshot SliceId %v IndexInstId %v PartitionId %v Creating New "+
			"Snapshot %v", mdb.id, mdb.idxInstId, mdb.idxPartnId, snapInfo)
	}

	return s, err
}

func (mdb *memdbSlice) doPersistSnapshot(s *memdbSnapshot) {
	logging.Infof("amd: doPersistSnapshot()")
	var concurrency int = 1

	if atomic.CompareAndSwapInt32(&mdb.isPersistorActive, 0, 1) {
		defer atomic.StoreInt32(&mdb.isPersistorActive, 0)

		t0 := time.Now()
		dir := newSnapshotPath(mdb.path)
		tmpdir := filepath.Join(mdb.path, tmpDirName)
		manifest := filepath.Join(tmpdir, "manifest.json")
		os.RemoveAll(tmpdir)
		mdb.confLock.RLock()
		maxThreads := mdb.sysconf["settings.moi.persistence_threads"].Int()
		total := atomic.LoadInt64(&totalMemDBItems)
		indexCount := mdb.GetCommittedCount()
		// Compute number of workers to be used for taking backup
		if total > 0 {
			concurrency = int(math.Ceil(float64(maxThreads) * float64(indexCount) / float64(total)))
		}

		mdb.confLock.RUnlock()

		// StoreToDisk call below will spawn 'concurrency' go routines to write
		// and will wait for this group to complete.
		// To ensure that CPU isn't overwhelmed, we limit how many such groups
		// can run in parallel.
		// To avoid holding a snapshot we must limit the caller via the item callback,
		// But as this callback is invoked per item, per writer,
		// it is enough to throttle just one writer go routine of each batch.
		var throttleToken int64
		limitWriterThreads := func(itm *memdb.ItemEntry) {
			var docid []byte
			entry := secondaryIndexEntry(itm.Item().Bytes())
			docid, _ = entry.ReadDocId(docid)
			currTime := uint32(common.CalcAbsNow()/1000000000)

			if entry.isExpiryEncoded() && currTime > entry.Expiry() {
				logging.Infof("amd: deleting: %s", docid)
				// only one will succeed since an entry into back exists only in one worker's back index
				// The results of the delete will be visible after the next snapshot
				for workerId := 0; workerId < mdb.numWriters; workerId++ {
					mdb.cmdCh[workerId] <- indexMutation{op: opDelete, docid: docid}
				}
			}
			if atomic.CompareAndSwapInt64(&throttleToken, 0, 1) {
				moiWriterSemaphoreCh <- true
			}
		}
		defer func() {
			if throttleToken == 1 { // Only post if callback above ran
				<-moiWriterSemaphoreCh
			}
		}()
		err := mdb.mainstore.StoreToDisk(tmpdir, s.info.MainSnap, concurrency, limitWriterThreads)
		if err == nil {
			var fd *os.File
			var bs []byte
			bs, err = json.Marshal(s.info)
			if err == nil {
				fd, err = os.OpenFile(manifest, os.O_WRONLY|os.O_CREATE, 0755)
				_, err = fd.Write(bs)
				if err == nil {
					err = fd.Close()
				}
			}

			if err == nil {
				err = os.Rename(tmpdir, dir)
				if err == nil {
					mdb.cleanupOldSnapshotFiles(mdb.maxRollbacks)
				}
			}
		}

		if err == nil {
			dur := time.Since(t0)
			logging.Infof("MemDBSlice Slice Id %v, Threads %d, IndexInstId %v, PartitionId %v created ondisk"+
				" snapshot %v. Took %v", mdb.id, concurrency, mdb.idxInstId, mdb.idxPartnId, dir, dur)
			mdb.idxStats.diskSnapStoreDuration.Set(int64(dur / time.Millisecond))
		} else {
			logging.Errorf("MemDBSlice Slice Id %v, IndexInstId %v, PartitionId %v failed to"+
				" create ondisk snapshot %v (error=%v)", mdb.id, mdb.idxInstId, mdb.idxPartnId, dir, err)
			os.RemoveAll(tmpdir)
			os.RemoveAll(dir)
		}
	} else {
		logging.Infof("MemDBSlice Slice Id %v, IndexInstId %v, PartitionId %v Skipping ondisk"+
			" snapshot. A snapshot writer is in progress.", mdb.id, mdb.idxInstId, mdb.idxPartnId)
		s.info.MainSnap.Close()
	}
}

func (mdb *memdbSlice) cleanupOldSnapshotFiles(keepn int) {
	manifests := mdb.getSnapshotManifests()
	if len(manifests) > keepn {
		toRemove := len(manifests) - keepn
		manifests = manifests[:toRemove]
		for _, m := range manifests {
			dir := filepath.Dir(m)
			logging.Infof("MemDBSlice Removing disk snapshot %v", dir)
			os.RemoveAll(dir)
		}
	}
}

func (mdb *memdbSlice) diskSize() int64 {
	var sz int64
	snapdirs, _ := filepath.Glob(filepath.Join(mdb.path, "snapshot.*"))
	for _, dir := range snapdirs {
		s, _ := common.DiskUsage(dir)
		sz += s
	}

	return sz
}

func (mdb *memdbSlice) getSnapshotManifests() []string {
	var files []string
	pattern := "*/manifest.json"
	all, _ := filepath.Glob(filepath.Join(mdb.path, pattern))
	for _, f := range all {
		if !strings.Contains(f, tmpDirName) {
			files = append(files, f)
		}
	}
	sort.Strings(files)
	return files
}

// Returns snapshot info list in reverse sorted order
func (mdb *memdbSlice) GetSnapshots() ([]SnapshotInfo, error) {
	var infos []SnapshotInfo

	files := mdb.getSnapshotManifests()
	for i := len(files) - 1; i >= 0; i-- {
		f := files[i]
		info := &memdbSnapshotInfo{dataPath: filepath.Dir(f)}
		fd, err := os.Open(f)
		if err == nil {
			defer fd.Close()
			bs, err := ioutil.ReadAll(fd)
			if err == nil {
				err = json.Unmarshal(bs, info)
				if err == nil {
					infos = append(infos, info)
				}
			}
		}

	}
	return infos, nil
}

func (mdb *memdbSlice) setCommittedCount() {
	prev := atomic.LoadUint64(&mdb.committedCount)
	curr := mdb.mainstore.ItemsCount()
	atomic.AddInt64(&totalMemDBItems, int64(curr)-int64(prev))
	atomic.StoreUint64(&mdb.committedCount, uint64(curr))
}

func (mdb *memdbSlice) GetCommittedCount() uint64 {
	return atomic.LoadUint64(&mdb.committedCount)
}

func (mdb *memdbSlice) resetStores() {
	// This is blocking call if snap refcounts != 0
	go mdb.mainstore.Close()
	if !mdb.isPrimary {
		for i := 0; i < mdb.numWriters; i++ {
			mdb.back[i].Close()
		}
	}

	mdb.initStores()

	prev := atomic.LoadUint64(&mdb.committedCount)
	atomic.AddInt64(&totalMemDBItems, -int64(prev))
	mdb.committedCount = 0
	mdb.idxStats.itemsCount.Set(0)
}

//Rollback slice to given snapshot. Return error if
//not possible
func (mdb *memdbSlice) Rollback(info SnapshotInfo) error {

	//before rollback make sure there are no mutations
	//in the slice buffer. Timekeeper will make sure there
	//are no flush workers before calling rollback.
	mdb.waitPersist()

	qc := atomic.LoadInt64(&mdb.qCount)
	if qc > 0 {
		common.CrashOnError(errors.New("Slice Invariant Violation - rollback with pending mutations"))
	}

	target := info.(*memdbSnapshotInfo)

	// Remove all the disk snapshots which were created after rollback snapshot
	snapInfos, err := mdb.GetSnapshots()
	if err != nil {
		return err
	}

	for _, snapInfo := range snapInfos {
		si := snapInfo.(*memdbSnapshotInfo)
		if si.dataPath == target.dataPath {
			break
		}

		if err := os.RemoveAll(si.dataPath); err != nil {
			return err
		}
	}

	mdb.resetStores()

	mdb.lastRollbackTs = info.Timestamp()
	return nil
}

func (mdb *memdbSlice) loadSnapshot(snapInfo *memdbSnapshotInfo) (err error) {
	defer func() {
		if r := recover(); r != nil || err != nil {
			logging.Errorf("MemDBSlice::loadSnapshot Slice Id %v, IndexInstId %v PartitionId %v failed to recover from the snapshot %v (err=%v,%v)",
				mdb.id, mdb.idxInstId, mdb.idxPartnId, snapInfo.dataPath, r, err)
			if err != errStorageCorrupted {
				os.RemoveAll(snapInfo.dataPath)
				os.Exit(1)
			} else {
				// Persist error in a file. Next time indexer comes up, required cleanup will happen.
				os.RemoveAll(snapInfo.dataPath)
				msg := fmt.Sprintf("%v", errStorageCorrupted)
				ioutil.WriteFile(filepath.Join(mdb.path, "error"), []byte(msg), 0755)
			}
		} else {
			os.RemoveAll(filepath.Join(mdb.path, "error"))
		}
	}()

	var wg sync.WaitGroup
	var backIndexCallback memdb.ItemCallback
	mdb.confLock.RLock()
	numVbuckets := mdb.sysconf["numVbuckets"].Int()
	mdb.confLock.RUnlock()

	partShardCh := make([]chan *memdb.ItemEntry, mdb.numWriters)

	logging.Infof("MemDBSlice::loadSnapshot Slice Id %v, IndexInstId %v, PartitionId %v reading %v",
		mdb.id, mdb.idxInstId, mdb.idxPartnId, snapInfo.dataPath)

	t0 := time.Now()
	if !mdb.isPrimary {
		for wId := 0; wId < mdb.numWriters; wId++ {
			wg.Add(1)
			partShardCh[wId] = make(chan *memdb.ItemEntry, 1000)
			go func(i int, wg *sync.WaitGroup) {
				defer wg.Done()
				for entry := range partShardCh[i] {
					if !mdb.isPrimary {
						entryBytes := entry.Item().Bytes()
						if updated, oldPtr := mdb.back[i].Update(entryBytes, unsafe.Pointer(entry.Node())); updated {
							oldNode := (*skiplist.Node)(oldPtr)
							entry.Node().SetLink(oldNode)
						}
					}
				}
			}(wId, &wg)
		}

		backIndexCallback = func(e *memdb.ItemEntry) {
			wId := vbucketFromEntryBytes(e.Item().Bytes(), numVbuckets) % mdb.numWriters
			partShardCh[wId] <- e
		}
	}

	mdb.confLock.RLock()
	concurrency := mdb.sysconf["settings.moi.recovery_threads"].Int()
	mdb.confLock.RUnlock()

	var snap *memdb.Snapshot
	snap, err = mdb.mainstore.LoadFromDisk(snapInfo.dataPath, concurrency, backIndexCallback)
	if err == memdb.ErrCorruptSnapshot {
		err = errStorageCorrupted
		logging.Errorf("MemDBSlice::loadSnapshot Slice Id %v, IndexInstId %v failed to load snapshot %v error(%v).",
			mdb.id, mdb.idxInstId, snapInfo.dataPath, err)
		return
	}

	if !mdb.isPrimary {
		for wId := 0; wId < mdb.numWriters; wId++ {
			close(partShardCh[wId])
		}
		wg.Wait()
	}

	dur := time.Since(t0)
	if err == nil {
		snapInfo.MainSnap = snap
		mdb.setCommittedCount()
		logging.Infof("MemDBSlice::loadSnapshot Slice Id %v, IndexInstId %v, PartitionId %v finished reading %v. Took %v",
			mdb.id, mdb.idxInstId, mdb.idxPartnId, snapInfo.dataPath, dur)
	} else {
		logging.Errorf("MemDBSlice::loadSnapshot Slice Id %v, IndexInstId %v, PartitionId %v failed to load snapshot %v error(%v).",
			mdb.id, mdb.idxInstId, mdb.idxPartnId, snapInfo.dataPath, err)
	}

	mdb.idxStats.diskSnapLoadDuration.Set(int64(dur / time.Millisecond))
	mdb.idxStats.numItemsRestored.Set(mdb.mainstore.ItemsCount())
	return
}

//RollbackToZero rollbacks the slice to initial state. Return error if
//not possible
func (mdb *memdbSlice) RollbackToZero() error {

	//before rollback make sure there are no mutations
	//in the slice buffer. Timekeeper will make sure there
	//are no flush workers before calling rollback.
	mdb.waitPersist()

	mdb.resetStores()
	mdb.cleanupOldSnapshotFiles(0)

	mdb.lastRollbackTs = nil

	return nil
}

func (mdb *memdbSlice) LastRollbackTs() *common.TsVbuuid {
	return mdb.lastRollbackTs
}

//slice insert/delete methods are async. There
//can be outstanding mutations in internal queue to flush even
//after insert/delete have return success to caller.
//This method provides a mechanism to wait till internal
//queue is empty.
func (mdb *memdbSlice) waitPersist() {

	if !mdb.checkAllWorkersDone() {
		//every SLICE_COMMIT_POLL_INTERVAL milliseconds,
		//check for outstanding mutations. If there are
		//none, proceed with the commit.
		mdb.confLock.RLock()
		commitPollInterval := mdb.sysconf["storage.moi.commitPollInterval"].Uint64()
		mdb.confLock.RUnlock()

		for {
			if mdb.checkAllWorkersDone() {
				break
			}
			time.Sleep(time.Millisecond * time.Duration(commitPollInterval))
		}
	}

}

//Commit persists the outstanding writes in underlying
//forestdb database. If Commit returns error, slice
//should be rolled back to previous snapshot.
func (mdb *memdbSlice) NewSnapshot(ts *common.TsVbuuid, commit bool) (SnapshotInfo, error) {

	mdb.waitPersist()

	qc := atomic.LoadInt64(&mdb.qCount)
	if qc > 0 {
		common.CrashOnError(errors.New("Slice Invariant Violation - commit with pending mutations"))
	}

	mdb.isDirty = false

	snap, err := mdb.mainstore.NewSnapshot()
	if err == memdb.ErrMaxSnapshotsLimitReached {
		logging.Warnf("Maximum snapshots limit reached for indexer. Restarting indexer...")
		os.Exit(0)
	}

	newSnapshotInfo := &memdbSnapshotInfo{
		Ts:        ts,
		MainSnap:  snap,
		Committed: commit,
	}
	mdb.setCommittedCount()

	return newSnapshotInfo, err
}

func (mdb *memdbSlice) FlushDone() {
	// no-op
}

//checkAllWorkersDone return true if all workers have
//finished processing
func (mdb *memdbSlice) checkAllWorkersDone() bool {

	//if there are mutations in the cmdCh, workers are
	//not yet done
	if mdb.getCmdsCount() > 0 {
		return false
	}

	//worker queue is empty, make sure both workers are done
	//processing the last mutation
	for i := 0; i < mdb.numWriters; i++ {
		mdb.workerDone[i] <- true
		<-mdb.workerDone[i]
	}
	return true
}

func (mdb *memdbSlice) Close() {
	mdb.lock.Lock()
	defer mdb.lock.Unlock()

	prev := atomic.LoadUint64(&mdb.committedCount)
	atomic.AddInt64(&totalMemDBItems, -int64(prev))

	logging.Infof("MemDBSlice::Close Closing Slice Id %v, IndexInstId %v, PartitionId %v, "+
		"IndexDefnId %v", mdb.id, mdb.idxInstId, mdb.idxPartnId, mdb.idxDefnId)

	//signal shutdown for command handler routines
	for i := 0; i < mdb.numWriters; i++ {
		mdb.stopCh[i] <- true
		<-mdb.stopCh[i]
	}

	if mdb.refCount > 0 {
		mdb.isSoftClosed = true
	} else {
		go tryClosememdbSlice(mdb)
	}
}

//Destroy removes the database file from disk.
//Slice is not recoverable after this.
func (mdb *memdbSlice) Destroy() {
	mdb.lock.Lock()
	defer mdb.lock.Unlock()

	if mdb.refCount > 0 {
		logging.Infof("MemDBSlice::Destroy Softdeleted Slice Id %v, IndexInstId %v, PartitionId %v, "+
			"IndexDefnId %v", mdb.id, mdb.idxInstId, mdb.idxPartnId, mdb.idxDefnId)
		mdb.isSoftDeleted = true
	} else {
		tryDeletememdbSlice(mdb)
	}
}

//Id returns the Id for this Slice
func (mdb *memdbSlice) Id() SliceId {
	return mdb.id
}

// FilePath returns the filepath for this Slice
func (mdb *memdbSlice) Path() string {
	return mdb.path
}

//IsActive returns if the slice is active
func (mdb *memdbSlice) IsActive() bool {
	return mdb.isActive
}

//SetActive sets the active state of this slice
func (mdb *memdbSlice) SetActive(isActive bool) {
	mdb.isActive = isActive
}

//Status returns the status for this slice
func (mdb *memdbSlice) Status() SliceStatus {
	return mdb.status
}

//SetStatus set new status for this slice
func (mdb *memdbSlice) SetStatus(status SliceStatus) {
	mdb.status = status
}

//IndexInstId returns the Index InstanceId this
//slice is associated with
func (mdb *memdbSlice) IndexInstId() common.IndexInstId {
	return mdb.idxInstId
}

//IndexDefnId returns the Index DefnId this slice
//is associated with
func (mdb *memdbSlice) IndexDefnId() common.IndexDefnId {
	return mdb.idxDefnId
}

// IsDirty returns true if there has been any change in
// in the slice storage after last in-mem/persistent snapshot
func (mdb *memdbSlice) IsDirty() bool {
	mdb.waitPersist()
	return mdb.isDirty
}

func (mdb *memdbSlice) Compact(abortTime time.Time, minFrag int) error {
	return nil
}

func (mdb *memdbSlice) Statistics() (StorageStatistics, error) {
	var sts StorageStatistics

	var internalData []string
	var ntMemUsed int64

	internalData = append(internalData, fmt.Sprintf("{\n\"MainStore\": %s", mdb.mainstore.DumpStats()))

	if !mdb.isPrimary {
		for i := 0; i < mdb.numWriters; i++ {
			internalData = append(internalData, ",\n")
			internalData = append(internalData, fmt.Sprintf(`"BackStore_%d": %s`, i, mdb.back[i].Stats()))
			ntMemUsed += mdb.back[i].MemoryInUse()
		}
	}

	internalData = append(internalData, "\n}")

	sts.InternalData = internalData
	sts.DataSize = mdb.mainstore.MemoryInUse()
	sts.MemUsed = mdb.mainstore.MemoryInUse() + ntMemUsed
	sts.DiskSize = mdb.diskSize()

	// Ideally, we should also count items in backstore. But numRecsInMem is mainly used for resident % computation
	// and for MOI it's always 100%. So an approximate number is fine as numRecsOnDisk will always be 0
	mdb.idxStats.numRecsInMem.Set(mdb.mainstore.ItemsCount())

	return sts, nil
}

func (mdb *memdbSlice) UpdateConfig(cfg common.Config) {
	mdb.confLock.Lock()
	defer mdb.confLock.Unlock()

	mdb.sysconf = cfg
	mdb.maxRollbacks = cfg["settings.moi.recovery.max_rollbacks"].Int()
}

func (mdb *memdbSlice) GetReaderContext() IndexReaderContext {
	return &cursorCtx{}
}

func (mdb *memdbSlice) String() string {

	str := fmt.Sprintf("SliceId: %v ", mdb.id)
	str += fmt.Sprintf("File: %v ", mdb.path)
	str += fmt.Sprintf("Index: %v ", mdb.idxInstId)
	str += fmt.Sprintf("Partition: %v ", mdb.idxPartnId)

	return str

}

func tryDeletememdbSlice(mdb *memdbSlice) {

	//cleanup the disk directory
	if err := os.RemoveAll(mdb.path); err != nil {
		logging.Errorf("MemDBSlice::Destroy Error Cleaning Up Slice Id %v, "+
			"IndexInstId %v, PartitionId %v, IndexDefnId %v. Error %v", mdb.id, mdb.idxInstId, mdb.idxPartnId, mdb.idxDefnId, err)
	}
}

func tryClosememdbSlice(mdb *memdbSlice) {
	mdb.mainstore.Close()
	if !mdb.isPrimary {
		for i := 0; i < mdb.numWriters; i++ {
			mdb.back[i].Close()
		}
	}
}

func (mdb *memdbSlice) getCmdsCount() int {
	qc := atomic.LoadInt64(&mdb.qCount)
	return int(qc)
}

func (mdb *memdbSlice) logWriterStat() {
	count := atomic.AddUint64(&mdb.flushedCount, 1)
	if (count%10000 == 0) || count == 1 {
		logging.Debugf("logWriterStat:: %v:%v "+
			"FlushedCount %v QueuedCount %v", mdb.idxInstId, mdb.idxPartnId,
			count, mdb.getCmdsCount())
	}

}

func (info *memdbSnapshotInfo) Timestamp() *common.TsVbuuid {
	return info.Ts
}

func (info *memdbSnapshotInfo) IsCommitted() bool {
	return info.Committed
}

func (info *memdbSnapshotInfo) String() string {
	if info.MainSnap == nil {
		return fmt.Sprintf("SnapInfo: file: %s", info.dataPath)
	}
	return fmt.Sprintf("SnapshotInfo: count:%v committed:%v", info.MainSnap.Count(), info.Committed)
}

func (s *memdbSnapshot) Create() error {
	return nil
}

func (s *memdbSnapshot) Open() error {
	atomic.AddInt32(&s.refCount, int32(1))

	return nil
}

func (s *memdbSnapshot) IsOpen() bool {

	count := atomic.LoadInt32(&s.refCount)
	return count > 0
}

func (s *memdbSnapshot) Id() SliceId {
	return s.slice.Id()
}

func (s *memdbSnapshot) IndexInstId() common.IndexInstId {
	return s.idxInstId
}

func (s *memdbSnapshot) IndexDefnId() common.IndexDefnId {
	return s.idxDefnId
}

func (s *memdbSnapshot) Timestamp() *common.TsVbuuid {
	return s.ts
}

//Close the snapshot
func (s *memdbSnapshot) Close() error {

	count := atomic.AddInt32(&s.refCount, int32(-1))

	if count < 0 {
		logging.Errorf("MemDBSnapshot::Close Close operation requested " +
			"on already closed snapshot")
		return errors.New("Snapshot Already Closed")

	} else if count == 0 {
		go s.Destroy()
	}

	return nil
}

func (s *memdbSnapshot) Destroy() {
	s.info.MainSnap.Close()

	defer s.slice.DecrRef()
}

func (s *memdbSnapshot) String() string {

	str := fmt.Sprintf("Index: %v ", s.idxInstId)
	str += fmt.Sprintf("Partition: %v ", s.idxPartnId)
	str += fmt.Sprintf("SliceId: %v ", s.slice.Id())
	str += fmt.Sprintf("TS: %v ", s.ts)
	return str
}

func (s *memdbSnapshot) Info() SnapshotInfo {
	return s.info
}

// ==============================
// Snapshot reader implementation
// ==============================

// Approximate items count
func (s *memdbSnapshot) StatCountTotal() (uint64, error) {
	c := s.slice.GetCommittedCount()
	return c, nil
}

func (s *memdbSnapshot) CountTotal(ctx IndexReaderContext, stopch StopChannel) (uint64, error) {
	return uint64(s.info.MainSnap.Count()), nil
}

func (s *memdbSnapshot) CountRange(ctx IndexReaderContext, low, high IndexKey, inclusion Inclusion,
	stopch StopChannel) (uint64, error) {

	var count uint64
	callb := func([]byte) error {
		select {
		case <-stopch:
			return common.ErrClientCancel
		default:
			count++
		}

		return nil
	}

	err := s.Range(ctx, low, high, inclusion, callb)
	return count, err
}

func (s *memdbSnapshot) MultiScanCount(ctx IndexReaderContext, low, high IndexKey, inclusion Inclusion,
	scan Scan, distinct bool,
	stopch StopChannel) (uint64, error) {

	var err error
	var scancount uint64
	count := 1
	checkDistinct := distinct && !s.isPrimary()
	isIndexComposite := len(s.slice.idxDefn.SecExprs) > 1

	buf := secKeyBufPool.Get()
	defer secKeyBufPool.Put(buf)

	previousRow := ctx.GetCursorKey()

	revbuf := secKeyBufPool.Get()
	defer secKeyBufPool.Put(revbuf)

	callb := func(entry []byte) error {
		select {
		case <-stopch:
			return common.ErrClientCancel
		default:

			skipRow := false
			var ck [][]byte

			//get the key in original format
			if s.slice.idxDefn.Desc != nil {
				revbuf := (*revbuf)[:0]
				//copy is required, otherwise storage may get updated
				revbuf = append(revbuf, entry...)
				jsonEncoder.ReverseCollate(revbuf, s.slice.idxDefn.Desc)
				entry = revbuf
			}
			if scan.ScanType == FilterRangeReq {
				if len(entry) > cap(*buf) {
					*buf = make([]byte, 0, len(entry)+RESIZE_PAD)
				}

				skipRow, ck, err = filterScanRow(entry, scan, (*buf)[:0])
				if err != nil {
					return err
				}
			}
			if skipRow {
				return nil
			}

			if checkDistinct {
				if isIndexComposite {
					// For Count Distinct, only leading key needs to be considered for
					// distinct comparison as N1QL supports distinct on only single key
					entry, err = projectLeadingKey(ck, entry, buf)
				}
				if len(*previousRow) != 0 && distinctCompare(entry, *previousRow) {
					return nil // Ignore the entry as it is same as previous entry
				}
			}

			if !s.isPrimary() {
				e := secondaryIndexEntry(entry)
				count = e.Count()
			}

			if checkDistinct {
				scancount++
				*previousRow = append((*previousRow)[:0], entry...)
			} else {
				scancount += uint64(count)
			}
		}
		return nil
	}

	e := s.Range(ctx, low, high, inclusion, callb)
	return scancount, e
}

func (s *memdbSnapshot) CountLookup(ctx IndexReaderContext, keys []IndexKey, stopch StopChannel) (uint64, error) {
	var err error
	var count uint64

	callb := func([]byte) error {
		select {
		case <-stopch:
			return common.ErrClientCancel
		default:
			count++
		}

		return nil
	}

	for _, k := range keys {
		if err = s.Lookup(ctx, k, callb); err != nil {
			break
		}
	}

	return count, err
}

func (s *memdbSnapshot) Exists(ctx IndexReaderContext, key IndexKey, stopch StopChannel) (bool, error) {
	var count uint64
	callb := func([]byte) error {
		select {
		case <-stopch:
			return common.ErrClientCancel
		default:
			count++
		}

		return nil
	}

	err := s.Lookup(ctx, key, callb)
	return count != 0, err
}

func (s *memdbSnapshot) Lookup(ctx IndexReaderContext, key IndexKey, callb EntryCallback) error {
	return s.Iterate(ctx, key, key, Both, compareExact, callb)
}

func (s *memdbSnapshot) Range(ctx IndexReaderContext, low, high IndexKey, inclusion Inclusion,
	callb EntryCallback) error {

	var cmpFn CmpEntry
	if s.isPrimary() {
		cmpFn = compareExact
	} else {
		cmpFn = comparePrefix
	}

	return s.Iterate(ctx, low, high, inclusion, cmpFn, callb)
}

func (s *memdbSnapshot) All(ctx IndexReaderContext, callb EntryCallback) error {
	return s.Range(ctx, MinIndexKey, MaxIndexKey, Both, callb)
}

func (s *memdbSnapshot) Iterate(ctx IndexReaderContext, low, high IndexKey, inclusion Inclusion,
	cmpFn CmpEntry, callback EntryCallback) error {
	var entry IndexEntry
	var err error
	t0 := time.Now()
	it := s.info.MainSnap.NewIterator()
	defer it.Close()

	if low.Bytes() == nil {
		it.SeekFirst()
	} else {
		it.Seek(low.Bytes())

		// Discard equal keys if low inclusion is requested
		if inclusion == Neither || inclusion == High {
			err = s.iterEqualKeys(low, it, cmpFn, nil)
			if err != nil {
				return err
			}
		}
	}
	s.slice.idxStats.Timings.stNewIterator.Put(time.Since(t0))

loop:
	for it.Valid() {
		itm := it.Get()
		s.newIndexEntry(itm, &entry)

		// Iterator has reached past the high key, no need to scan further
		if cmpFn(high, entry) <= 0 {
			break loop
		}

		err = callback(entry.Bytes())
		if err != nil {
			return err
		}

		it.Next()
	}

	// Include equal keys if high inclusion is requested
	if inclusion == Both || inclusion == High {
		err = s.iterEqualKeys(high, it, cmpFn, callback)
		if err != nil {
			return err
		}
	}

	return nil
}

func (s *memdbSnapshot) isPrimary() bool {
	return s.slice.isPrimary
}

func (s *memdbSnapshot) newIndexEntry(b []byte, entry *IndexEntry) {
	var err error

	if s.slice.isPrimary {
		*entry, err = BytesToPrimaryIndexEntry(b)
	} else {
		*entry, err = BytesToSecondaryIndexEntry(b)
	}
	common.CrashOnError(err)
}

func (s *memdbSnapshot) iterEqualKeys(k IndexKey, it *memdb.Iterator,
	cmpFn CmpEntry, callback func([]byte) error) error {
	var err error

	var entry IndexEntry
	for ; it.Valid(); it.Next() {
		itm := it.Get()
		s.newIndexEntry(itm, &entry)
		if cmpFn(k, entry) == 0 {
			if callback != nil {
				err = callback(itm)
				if err != nil {
					return err
				}
			}
		} else {
			break
		}
	}

	return err
}

func newSnapshotPath(dirpath string) string {
	file := time.Now().Format("snapshot.2006-01-02.15:04:05.000")
	file = strings.Replace(file, ":", "", -1)
	return filepath.Join(dirpath, file)
}
