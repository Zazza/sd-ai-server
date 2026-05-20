package gpuproxy

import (
	"context"
	"sync"
	"sync/atomic"
	"time"
)

type Priority int

const (
	PriorityHigh Priority = iota
	PriorityLow
)

type Request struct {
	ID       int64
	Priority Priority
	Enqueued time.Time
	Endpoint string
	Proceed  chan struct{}
	Complete chan struct{}
}

type QueueStats struct {
	HighWaiting int `json:"high_waiting"`
	LowWaiting  int `json:"low_waiting"`
	Active      int `json:"active"`
	TotalSlots  int `json:"total_slots"`
}

type PriorityQueue struct {
	mu     sync.Mutex
	highQ  []*Request
	lowQ   []*Request
	nextID int64
	signal chan struct{}
	active int64
	slots  int
}

func NewPriorityQueue(slots int) *PriorityQueue {
	if slots < 1 {
		slots = 1
	}
	return &PriorityQueue{
		highQ:  make([]*Request, 0),
		lowQ:   make([]*Request, 0),
		signal: make(chan struct{}, 1),
		slots:  slots,
	}
}

func (q *PriorityQueue) Enqueue(endpoint string, priority Priority) *Request {
	q.mu.Lock()
	defer q.mu.Unlock()

	q.nextID++
	qr := &Request{
		ID:       q.nextID,
		Priority: priority,
		Enqueued: time.Now(),
		Endpoint: endpoint,
		Proceed:  make(chan struct{}),
		Complete: make(chan struct{}),
	}

	switch priority {
	case PriorityHigh:
		q.highQ = append(q.highQ, qr)
	default:
		q.lowQ = append(q.lowQ, qr)
	}

	select {
	case q.signal <- struct{}{}:
	default:
	}

	return qr
}

func (q *PriorityQueue) Dequeue(ctx context.Context) (*Request, bool) {
	for {
		q.mu.Lock()
		if req := q.dequeueLocked(); req != nil {
			atomic.AddInt64(&q.active, 1)
			q.mu.Unlock()
			return req, true
		}
		q.mu.Unlock()

		select {
		case <-ctx.Done():
			return nil, false
		case <-q.signal:
		}
	}
}

func (q *PriorityQueue) dequeueLocked() *Request {
	if len(q.highQ) > 0 {
		req := q.highQ[0]
		q.highQ[0] = nil
		q.highQ = q.highQ[1:]
		return req
	}
	if len(q.lowQ) > 0 {
		req := q.lowQ[0]
		q.lowQ[0] = nil
		q.lowQ = q.lowQ[1:]
		return req
	}
	return nil
}

func (q *PriorityQueue) Release() {
	atomic.AddInt64(&q.active, -1)
	select {
	case q.signal <- struct{}{}:
	default:
	}
}

func (q *PriorityQueue) Stats() QueueStats {
	q.mu.Lock()
	defer q.mu.Unlock()
	return QueueStats{
		HighWaiting: len(q.highQ),
		LowWaiting:  len(q.lowQ),
		Active:      int(atomic.LoadInt64(&q.active)),
		TotalSlots:  q.slots,
	}
}

func (q *PriorityQueue) Len() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.highQ) + len(q.lowQ)
}
