package gRPC_pool

import "sync"

type poolQueue struct {
	list     []interface{}
	capacity int // 0 mean no limit
	mu       sync.RWMutex
}

func initQueue(capacity int) *poolQueue {
	newQueue := new(poolQueue)
	newQueue.list = []interface{}{}
	newQueue.capacity = capacity
	return newQueue
}

func (q *poolQueue) enqueue(item interface{}) {
	q.mu.Lock()
	if q.capacity == 0 || q.size() < q.capacity {
		q.list = append(q.list, item)
	}
	q.mu.Unlock()
}

func (q *poolQueue) dequeue() interface{} {
	q.mu.Lock()
	if q.size() == 0 {
		q.mu.Unlock()
		return nil
	}
	request := q.list[0]
	q.list = q.list[1:len(q.list)]
	q.mu.Unlock()
	return request
}

func (q *poolQueue) isEmpty() bool {
	return len(q.list) == 0
}

func (q *poolQueue) size() int {
	return len(q.list)
}
