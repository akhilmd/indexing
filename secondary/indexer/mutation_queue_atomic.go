//  Copyright (c) 2014 Couchbase, Inc.
//  Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file
//  except in compliance with the License. You may obtain a copy of the License at
//    http://www.apache.org/licenses/LICENSE-2.0
//  Unless required by applicable law or agreed to in writing, software distributed under the
//  License is distributed on an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND,
//  either express or implied. See the License for the specific language governing permissions
//  and limitations under the License.

package indexer

import (
	"errors"
	"github.com/couchbase/indexing/secondary/logging"
	"sync/atomic"
	"time"
	"unsafe"
)

//MutationQueue interface specifies methods which a mutation queue for indexer
//needs to implement
type MutationQueue interface {

	//enqueue a mutation reference based on vbucket
	Enqueue(mutation *MutationKeys, vbucket Vbucket) error

	//dequeue a vbucket's mutation and keep sending on a channel until stop signal
	Dequeue(vbucket Vbucket) (<-chan *MutationKeys, chan<- bool, error)
	//dequeue a vbucket's mutation upto seqno(wait if not available)
	DequeueUptoSeqno(vbucket Vbucket, seqno Seqno) (<-chan *MutationKeys, error)
	//dequeue single element for a vbucket and return
	DequeueSingleElement(vbucket Vbucket) *MutationKeys

	//return reference to a vbucket's mutation at Tail of queue without dequeue
	PeekTail(vbucket Vbucket) *MutationKeys
	//return reference to a vbucket's mutation at Head of queue without dequeue
	PeekHead(vbucket Vbucket) *MutationKeys

	//return size of queue per vbucket
	GetSize(vbucket Vbucket) int64

	//returns the numbers of vbuckets for the queue
	GetNumVbuckets() uint16

	//destroy the resources
	Destroy()
}

//AtomicMutationQueue is a lock-free multi-queue with internal queue per
//vbucket for storing mutation references. This is loosely based on
//http://www.drdobbs.com/parallel/writing-lock-free-code-a-corrected-queue/210604448?pgno=1
//with the main difference being that free nodes are being reused here to reduce GC.
//
//It doesn't copy the mutation and its caller's responsiblity
//to allocate/deallocate KeyVersions struct. A mutation which is currently in queue
//shouldn't be freed.
//
//This implementation uses Go "atomic" pkg to provide safe concurrent access
//for a single reader and writer per vbucket queue without using mutex locks.
//
//It provides safe concurrent read/write access across vbucket queues.

type atomicMutationQueue struct {
	// IMPORTANT: should be 64 bit aligned.
	head   []unsafe.Pointer //head pointer per vbucket queue
	tail   []unsafe.Pointer //tail pointer per vbucket queue
	size   []int64          //size of queue per vbucket
	maxLen int64            //max length of queue per vbucket

	free        []*node //free pointer per vbucket queue
	stopch      []StopChannel
	numVbuckets uint16 //num vbuckets for the queue
	isDestroyed bool
}

//NewAtomicMutationQueue allocates a new Atomic Mutation Queue and initializes it
func NewAtomicMutationQueue(numVbuckets uint16, maxLenPerVb int64) *atomicMutationQueue {

	q := &atomicMutationQueue{head: make([]unsafe.Pointer, numVbuckets),
		tail:        make([]unsafe.Pointer, numVbuckets),
		free:        make([]*node, numVbuckets),
		size:        make([]int64, numVbuckets),
		numVbuckets: numVbuckets,
		maxLen:      maxLenPerVb,
		stopch:      make([]StopChannel, numVbuckets),
	}

	var x uint16
	for x = 0; x < numVbuckets; x++ {
		node := &node{} //sentinel node for the queue
		q.head[x] = unsafe.Pointer(node)
		q.tail[x] = unsafe.Pointer(node)
		q.free[x] = node
		q.stopch[x] = make(StopChannel)
	}

	return q

}

//Node represents a single element in the queue
type node struct {
	mutation *MutationKeys
	next     *node
}

//Poll Interval for dequeue thread
const DEQUEUE_POLL_INTERVAL = 20
const ALLOC_POLL_INTERVAL = 30
const MAX_VB_QUEUE_LENGTH = 1000

//Enqueue will enqueue the mutation reference for given vbucket.
//Caller should not free the mutation till it is dequeued.
//Mutation will not be copied internally by the queue.
func (q *atomicMutationQueue) Enqueue(mutation *MutationKeys, vbucket Vbucket) error {

	if vbucket < 0 || vbucket > Vbucket(q.numVbuckets)-1 {
		return errors.New("vbucket out of range")
	}

	//no more requests are taken once queue
	//is marked as destroyed
	if q.isDestroyed {
		return nil
	}

	//create a new node
	n := q.allocNode(vbucket)
	if n == nil {
		return nil
	}

	n.mutation = mutation
	n.next = nil

	//point tail's next to new node
	tail := (*node)(atomic.LoadPointer(&q.tail[vbucket]))
	tail.next = n
	//update tail to new node
	atomic.StorePointer(&q.tail[vbucket], unsafe.Pointer(tail.next))

	atomic.AddInt64(&q.size[vbucket], 1)

	return nil

}

//DequeueUptoSeqno returns a channel on which it will return mutation reference
//for specified vbucket upto the sequence number specified.
//This function will keep polling till mutations upto seqno are available
//to be sent. It terminates when it finds a mutation with seqno higher than
//the one specified as argument. This allow for multiple mutations with same
//seqno (e.g. in case of multiple indexes)
//It closes the mutation channel to indicate its done.
func (q *atomicMutationQueue) DequeueUptoSeqno(vbucket Vbucket, seqno Seqno) (
	<-chan *MutationKeys, error) {

	datach := make(chan *MutationKeys)

	go q.dequeueUptoSeqno(vbucket, seqno, datach)

	return datach, nil

}

func (q *atomicMutationQueue) dequeueUptoSeqno(vbucket Vbucket, seqno Seqno,
	datach chan *MutationKeys) {

	//every DEQUEUE_POLL_INTERVAL milliseconds, check for new mutations
	ticker := time.NewTicker(time.Millisecond * DEQUEUE_POLL_INTERVAL)

	var dequeueSeq Seqno

	for _ = range ticker.C {
		for atomic.LoadPointer(&q.head[vbucket]) !=
			atomic.LoadPointer(&q.tail[vbucket]) { //if queue is nonempty

			head := (*node)(atomic.LoadPointer(&q.head[vbucket]))
			//copy the mutation pointer
			m := head.next.mutation
			if seqno >= m.meta.seqno {
				//free mutation pointer
				head.next.mutation = nil
				//move head to next
				atomic.StorePointer(&q.head[vbucket], unsafe.Pointer(head.next))
				atomic.AddInt64(&q.size[vbucket], -1)
				//send mutation to caller
				dequeueSeq = m.meta.seqno
				datach <- m
			}

			//once the seqno is reached, close the channel
			if seqno <= dequeueSeq {
				ticker.Stop()
				close(datach)
				return
			}
		}
	}
}

//Dequeue returns a channel on which it will return mutation reference for specified vbucket.
//This function will keep polling and send mutations as those become available.
//It returns a stop channel on which caller can signal it to stop.
func (q *atomicMutationQueue) Dequeue(vbucket Vbucket) (<-chan *MutationKeys,
	chan<- bool, error) {

	datach := make(chan *MutationKeys)
	stopch := make(chan bool)

	//every DEQUEUE_POLL_INTERVAL milliseconds, check for new mutations
	ticker := time.NewTicker(time.Millisecond * DEQUEUE_POLL_INTERVAL)

	go func() {
		for {
			select {
			case <-ticker.C:
				q.dequeue(vbucket, datach)
			case <-stopch:
				ticker.Stop()
				close(datach)
				return
			}
		}
	}()

	return datach, stopch, nil

}

func (q *atomicMutationQueue) dequeue(vbucket Vbucket, datach chan *MutationKeys) {

	//keep dequeuing till list is empty
	for {
		m := q.DequeueSingleElement(vbucket)
		if m == nil {
			return
		}
		//send mutation to caller
		datach <- m
	}

}

//DequeueSingleElement dequeues a single element and returns.
//Returns nil in case of empty queue.
func (q *atomicMutationQueue) DequeueSingleElement(vbucket Vbucket) *MutationKeys {

	if atomic.LoadPointer(&q.head[vbucket]) !=
		atomic.LoadPointer(&q.tail[vbucket]) { //if queue is nonempty

		head := (*node)(atomic.LoadPointer(&q.head[vbucket]))
		//copy the mutation pointer
		m := head.next.mutation
		//free mutation pointer
		head.next.mutation = nil
		//move head to next
		atomic.StorePointer(&q.head[vbucket], unsafe.Pointer(head.next))
		atomic.AddInt64(&q.size[vbucket], -1)
		return m
	}
	return nil
}

//PeekTail returns reference to a vbucket's mutation at tail of queue without dequeue
func (q *atomicMutationQueue) PeekTail(vbucket Vbucket) *MutationKeys {
	if atomic.LoadPointer(&q.head[vbucket]) !=
		atomic.LoadPointer(&q.tail[vbucket]) { //if queue is nonempty
		tail := (*node)(atomic.LoadPointer(&q.tail[vbucket]))
		return tail.mutation
	}
	return nil
}

//PeekHead returns reference to a vbucket's mutation at head of queue without dequeue
func (q *atomicMutationQueue) PeekHead(vbucket Vbucket) *MutationKeys {
	if atomic.LoadPointer(&q.head[vbucket]) !=
		atomic.LoadPointer(&q.tail[vbucket]) { //if queue is nonempty
		head := (*node)(atomic.LoadPointer(&q.head[vbucket]))
		return head.mutation
	}
	return nil
}

//GetSize returns the size of the vbucket queue
func (q *atomicMutationQueue) GetSize(vbucket Vbucket) int64 {
	return atomic.LoadInt64(&q.size[vbucket])
}

//GetNumVbuckets returns the numbers of vbuckets for the queue
func (q *atomicMutationQueue) GetNumVbuckets() uint16 {
	return q.numVbuckets
}

//allocNode tries to get node from freelist, otherwise allocates a new node and returns
func (q *atomicMutationQueue) allocNode(vbucket Vbucket) *node {

	//get node from freelist
	n := q.popFreeList(vbucket)
	if n != nil {
		return n
	} else {
		currLen := atomic.LoadInt64(&q.size[vbucket])
		if currLen < q.maxLen {
			//allocate new node and return
			return &node{}
		}
	}

	//every ALLOC_POLL_INTERVAL milliseconds, check for free nodes
	ticker := time.NewTicker(time.Millisecond * ALLOC_POLL_INTERVAL)

	var totalWait int
	for {
		select {
		case <-ticker.C:
			totalWait += ALLOC_POLL_INTERVAL
			n = q.popFreeList(vbucket)
			if n != nil {
				return n
			}
			if totalWait > 5000 {
				logging.Warnf("Indexer::MutationQueue Waiting for Node "+
					"Alloc for %v Milliseconds Vbucket %v", totalWait, vbucket)
			}

		case <-q.stopch[vbucket]:
			return nil

		}
	}

	return nil

}

//popFreeList removes a node from freelist and returns to caller.
//if freelist is empty, it returns nil.
func (q *atomicMutationQueue) popFreeList(vbucket Vbucket) *node {

	if q.free[vbucket] != (*node)(atomic.LoadPointer(&q.head[vbucket])) {
		n := q.free[vbucket]
		q.free[vbucket] = q.free[vbucket].next
		n.mutation = nil
		n.next = nil
		return n
	} else {
		return nil
	}

}

//Destroy will free up all resources of the queue.
//Importantly it will free up pending mutations as well.
//Once destroy have been called, further enqueue operations
//will be no-op.
func (q *atomicMutationQueue) Destroy() {

	//set the flag so no more Enqueue requests
	//are taken on this queue
	q.isDestroyed = true

	//ensure all pending allocs get stopped
	var i uint16
	for i = 0; i < q.numVbuckets; i++ {
		close(q.stopch[i])
	}

	//dequeue all the items in the queue and free
	for i = 0; i < q.numVbuckets; i++ {
		mutch := make(chan *MutationKeys)
		go func() {
			for mutk := range mutch {
				mutk.Free()
			}
		}()
		q.dequeue(Vbucket(i), mutch)
		close(mutch)
	}

}
