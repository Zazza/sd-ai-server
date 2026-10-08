package gpuqueue

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"
)

type qjob struct {
	id        string
	seq       int
	kind      Kind
	client    string
	weightMB  int
	priority  int
	submitted time.Time
	started   time.Time
	deadline  time.Time

	admitted  chan struct{}
	removed   chan struct{}
	stopRenew chan struct{}
}

type Queue struct {
	mu          sync.Mutex
	totalMB     int
	usedMB      int
	disabled    bool
	waiting     []*qjob
	running     map[string]*qjob
	seq         int
	warnings    []string
	maxWarn     int
	maxWait     time.Duration
	leaseTTL    time.Duration
	maxQueueLen int
}

func New(cfg Config) *Queue {
	if cfg.MaxWait <= 0 {
		cfg.MaxWait = 120 * time.Second
	}
	if cfg.LeaseTTL <= 0 {
		cfg.LeaseTTL = 90 * time.Second
	}
	if cfg.MaxWarnings <= 0 {
		cfg.MaxWarnings = 20
	}
	if cfg.MaxQueueLen == 0 {
		cfg.MaxQueueLen = 100
	}
	return &Queue{
		totalMB:     cfg.TotalBudgetMB,
		disabled:    cfg.TotalBudgetMB <= 0,
		running:     make(map[string]*qjob),
		maxWarn:     cfg.MaxWarnings,
		maxWait:     cfg.MaxWait,
		leaseTTL:    cfg.LeaseTTL,
		maxQueueLen: cfg.MaxQueueLen,
	}
}

func (q *Queue) Enabled() bool {
	return !q.disabled
}

func (q *Queue) Budget() int {
	return q.totalMB
}

func (q *Queue) QueueFull() bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.maxQueueLen > 0 && len(q.waiting) >= q.maxQueueLen
}

func (q *Queue) Warn(msg string) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.warnLocked(msg)
}

func (q *Queue) MaxWait() time.Duration {
	return q.maxWait
}

func (q *Queue) TTL() time.Duration {
	return q.leaseTTL
}

func (q *Queue) Start(ctx context.Context) {
	if q.disabled {
		return
	}
	go q.sweep(ctx)
}

func (q *Queue) sweep(ctx context.Context) {
	interval := q.leaseTTL / 3
	if interval < 5*time.Second {
		interval = 5 * time.Second
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			q.evictExpired()
		}
	}
}

func (q *Queue) Acquire(ctx context.Context, spec Spec) (*Lease, error) {
	if q.disabled {
		return &Lease{q: q}, nil
	}
	if spec.WeightMB <= 0 {
		q.warn("non-positive weight for kind " + string(spec.Kind))
		return &Lease{q: q}, nil
	}
	j, err := q.enqueue(spec)
	if err != nil {
		return nil, err
	}
	timer := time.NewTimer(q.maxWait)
	defer timer.Stop()
	select {
	case <-j.admitted:
	case <-timer.C:
		if q.dequeue(j.id) {
			return nil, ErrTimeout
		}
		if !q.isRunning(j.id) {
			return nil, ErrCancelled
		}
	case <-ctx.Done():
		if q.dequeue(j.id) {
			return nil, ErrCancelled
		}
		if !q.isRunning(j.id) {
			return nil, ErrCancelled
		}
	case <-j.removed:
		return nil, ErrCancelled
	}
	if spec.AutoRenew {
		q.startRenew(j)
	}
	return &Lease{q: q, id: j.id}, nil
}

func (q *Queue) TryAcquire(spec Spec) (*Lease, bool, int) {
	if q.disabled {
		return &Lease{q: q}, true, 0
	}
	if spec.WeightMB <= 0 {
		q.warn("non-positive weight for kind " + string(spec.Kind))
		return &Lease{q: q}, true, 0
	}
	q.mu.Lock()
	if q.maxQueueLen > 0 && len(q.waiting) >= q.maxQueueLen {
		q.mu.Unlock()
		return nil, false, 0
	}
	j := q.newQJobLocked(spec)
	q.waiting = append(q.waiting, j)
	sortWaiting(q.waiting)
	q.dispatchLocked()
	admitted := false
	var pos int
	if _, ok := q.running[j.id]; ok {
		admitted = true
	} else {
		pos = q.positionLocked(j.id)
	}
	q.mu.Unlock()
	if !admitted {
		return &Lease{q: q, id: j.id}, false, pos
	}
	if spec.AutoRenew {
		q.startRenew(j)
	}
	return &Lease{q: q, id: j.id}, true, 0
}

func (q *Queue) WaitLease(ctx context.Context, id string) (*Lease, error) {
	if q.disabled {
		return &Lease{q: q}, nil
	}
	q.mu.Lock()
	j, running := q.running[id]
	if !running {
		for _, w := range q.waiting {
			if w.id == id {
				j = w
				break
			}
		}
	}
	q.mu.Unlock()
	if j == nil {
		return nil, ErrNotFound
	}
	if running {
		return &Lease{q: q, id: j.id}, nil
	}
	select {
	case <-j.admitted:
		return &Lease{q: q, id: j.id}, nil
	case <-j.removed:
		return nil, ErrCancelled
	case <-ctx.Done():
		return nil, ErrCancelled
	}
}

func (q *Queue) Heartbeat(id string) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	j, ok := q.running[id]
	if !ok {
		return false
	}
	j.deadline = time.Now().Add(q.leaseTTL)
	return true
}

func (q *Queue) Release(id string) {
	if id == "" {
		return
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if j, ok := q.running[id]; ok {
		q.finishLocked(j)
		q.dispatchLocked()
		return
	}
	if q.dequeueLocked(id) {
		return
	}
	q.warnLocked("release unknown lease " + id)
}

func (q *Queue) Cancel(id string) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	if j, ok := q.running[id]; ok {
		q.finishLocked(j)
		q.dispatchLocked()
		return true
	}
	return q.dequeueLocked(id)
}

func (q *Queue) Move(id string, dir int) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	for _, j := range q.waiting {
		if j.id == id {
			j.priority = clampPriority(j.priority + dir)
			sortWaiting(q.waiting)
			return true
		}
	}
	return false
}

func (q *Queue) Get(id string) (Job, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if j, ok := q.running[id]; ok {
		return q.snapshotLocked(j, 0), true
	}
	for i, j := range q.waiting {
		if j.id == id {
			return q.snapshotLocked(j, i+1), true
		}
	}
	return Job{}, false
}

func (q *Queue) Status() Status {
	q.mu.Lock()
	defer q.mu.Unlock()
	st := Status{
		Enabled:  !q.disabled,
		Budget:   q.totalMB,
		Running:  make([]Job, 0, len(q.running)),
		Queue:    make([]Job, len(q.waiting)),
		Warnings: append([]string{}, q.warnings...),
	}
	for _, j := range q.running {
		st.Running = append(st.Running, q.snapshotLocked(j, 0))
	}
	for i, j := range q.waiting {
		st.Queue[i] = q.snapshotLocked(j, i+1)
	}
	return st
}

func (q *Queue) enqueue(spec Spec) (*qjob, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.maxQueueLen > 0 && len(q.waiting) >= q.maxQueueLen {
		return nil, ErrQueueFull
	}
	j := q.newQJobLocked(spec)
	q.waiting = append(q.waiting, j)
	sortWaiting(q.waiting)
	q.dispatchLocked()
	return j, nil
}

func (q *Queue) newQJobLocked(spec Spec) *qjob {
	q.seq++
	return &qjob{
		id:        fmt.Sprintf("%s-%d", spec.Kind, q.seq),
		seq:       q.seq,
		kind:      spec.Kind,
		client:    spec.Client,
		weightMB:  spec.WeightMB,
		priority:  clampPriority(spec.Priority),
		submitted: time.Now(),
		admitted:  make(chan struct{}),
		removed:   make(chan struct{}),
		stopRenew: make(chan struct{}),
	}
}

func (q *Queue) dispatchLocked() {
	now := time.Now()
	kept := make([]*qjob, 0, len(q.waiting))
	for _, j := range q.waiting {
		if q.usedMB+j.weightMB <= q.totalMB {
			q.usedMB += j.weightMB
			j.started = now
			j.deadline = now.Add(q.leaseTTL)
			q.running[j.id] = j
			close(j.admitted)
		} else {
			kept = append(kept, j)
		}
	}
	q.waiting = kept
}

func (q *Queue) evictExpired() {
	q.mu.Lock()
	defer q.mu.Unlock()
	now := time.Now()
	for id, j := range q.running {
		if now.After(j.deadline) {
			q.finishLocked(j)
			q.warnLocked("lease expired: " + id)
		}
	}
	q.dispatchLocked()
	q.dropAbandonedLocked(now)
}

func (q *Queue) dropAbandonedLocked(now time.Time) {
	kept := make([]*qjob, 0, len(q.waiting))
	for _, j := range q.waiting {
		if now.Sub(j.submitted) > q.maxWait {
			q.warnLocked("job abandoned: " + j.id)
			close(j.removed)
			continue
		}
		kept = append(kept, j)
	}
	q.waiting = kept
}

func (q *Queue) isRunning(id string) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	_, ok := q.running[id]
	return ok
}

func (q *Queue) finishLocked(j *qjob) {
	delete(q.running, j.id)
	q.usedMB -= j.weightMB
	if q.usedMB < 0 {
		q.usedMB = 0
		q.warnLocked("budget invariant violated for " + j.id)
	}
	close(j.stopRenew)
}

func (q *Queue) dequeue(id string) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.dequeueLocked(id)
}

func (q *Queue) dequeueLocked(id string) bool {
	for i, j := range q.waiting {
		if j.id == id {
			q.waiting = append(q.waiting[:i], q.waiting[i+1:]...)
			close(j.removed)
			return true
		}
	}
	return false
}

func (q *Queue) positionLocked(id string) int {
	for i, j := range q.waiting {
		if j.id == id {
			return i + 1
		}
	}
	return 0
}

func (q *Queue) startRenew(j *qjob) {
	interval := q.leaseTTL / 3
	if interval <= 0 {
		interval = time.Second
	}
	go func() {
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-j.stopRenew:
				return
			case <-t.C:
				if !q.Heartbeat(j.id) {
					return
				}
			}
		}
	}()
}

func (q *Queue) snapshotLocked(j *qjob, position int) Job {
	return Job{
		ID:            j.id,
		Kind:          j.kind,
		Client:        j.client,
		WeightMB:      j.weightMB,
		Priority:      j.priority,
		SubmittedAt:   j.submitted,
		StartedAt:     j.started,
		LeaseDeadline: j.deadline,
		Position:      position,
	}
}

func (q *Queue) warn(msg string) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.warnLocked(msg)
}

func (q *Queue) warnLocked(msg string) {
	entry := time.Now().UTC().Format(time.RFC3339) + " " + msg
	if len(q.warnings) >= q.maxWarn {
		copy(q.warnings, q.warnings[1:])
		q.warnings[len(q.warnings)-1] = entry
		return
	}
	q.warnings = append(q.warnings, entry)
}

func sortWaiting(js []*qjob) {
	sort.Slice(js, func(a, b int) bool {
		x, y := js[a], js[b]
		if x.priority != y.priority {
			return x.priority < y.priority
		}
		if !x.submitted.Equal(y.submitted) {
			return x.submitted.Before(y.submitted)
		}
		return x.seq < y.seq
	})
}

func clampPriority(p int) int {
	if p < -9 {
		return -9
	}
	if p > 9 {
		return 9
	}
	return p
}
