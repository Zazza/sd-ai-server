package gpuqueue

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestQueue(budgetMB int, maxWait, ttl time.Duration) *Queue {
	return New(Config{
		TotalBudgetMB: budgetMB,
		MaxWait:       maxWait,
		LeaseTTL:      ttl,
		MaxWarnings:   20,
	})
}

func waitFor(t *testing.T, timeout time.Duration, cond func() bool) bool {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(5 * time.Millisecond)
	}
	return cond()
}

func TestKind_Valid(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		kind Kind
		want bool
	}{
		{"sd", KindSD, true},
		{"llm", KindLLM, true},
		{"yue", KindYue, true},
		{"unknown", Kind("foo"), false},
		{"empty", Kind(""), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, tt.kind.Valid())
		})
	}
}

func TestAcquire_ImmediateWhenBudgetAvailable(t *testing.T) {
	t.Parallel()
	q := newTestQueue(10000, 5*time.Second, 30*time.Second)

	lease, err := q.Acquire(context.Background(), Spec{Kind: KindSD, Client: "c1", WeightMB: 4000})
	require.NoError(t, err)
	require.NotNil(t, lease)
	assert.NotEmpty(t, lease.ID())

	st := q.Status()
	require.Len(t, st.Running, 1)
	assert.Equal(t, 4000, st.Running[0].WeightMB)
	assert.Equal(t, KindSD, st.Running[0].Kind)
	assert.Empty(t, st.Queue)
	assert.False(t, st.Running[0].StartedAt.IsZero())
	assert.False(t, st.Running[0].LeaseDeadline.IsZero())

	lease.Release()
	assert.Empty(t, q.Status().Running)
	assert.Empty(t, q.Status().Queue)
}

func TestQueue_PriorityThenFIFOOrder(t *testing.T) {
	t.Parallel()
	q := newTestQueue(5000, 5*time.Second, 30*time.Second)

	head, err := q.Acquire(context.Background(), Spec{Kind: KindSD, WeightMB: 5000})
	require.NoError(t, err)

	mid, acquired, pos := q.TryAcquire(Spec{Kind: KindLLM, WeightMB: 3000, Priority: 2})
	require.False(t, acquired)
	assert.Equal(t, 1, pos)

	high, acquired, _ := q.TryAcquire(Spec{Kind: KindYue, WeightMB: 3000, Priority: 0})
	require.False(t, acquired)

	norm, acquired, _ := q.TryAcquire(Spec{Kind: KindSD, WeightMB: 3000, Priority: 1})
	require.False(t, acquired)

	st := q.Status()
	require.Len(t, st.Queue, 3)
	assert.Equal(t, high.ID(), st.Queue[0].ID)
	assert.Equal(t, norm.ID(), st.Queue[1].ID)
	assert.Equal(t, mid.ID(), st.Queue[2].ID)
	assert.Equal(t, 1, st.Queue[0].Position)
	assert.Equal(t, 2, st.Queue[1].Position)
	assert.Equal(t, 3, st.Queue[2].Position)

	head.Release()

	st = q.Status()
	require.Len(t, st.Running, 1)
	assert.Equal(t, high.ID(), st.Running[0].ID)
	require.Len(t, st.Queue, 2)
	assert.Equal(t, norm.ID(), st.Queue[0].ID)
	assert.Equal(t, mid.ID(), st.Queue[1].ID)
}

func TestQueue_FIFOWithinSamePriority(t *testing.T) {
	t.Parallel()
	q := newTestQueue(2000, 5*time.Second, 30*time.Second)

	first, err := q.Acquire(context.Background(), Spec{Kind: KindSD, WeightMB: 2000})
	require.NoError(t, err)

	a, ok, _ := q.TryAcquire(Spec{Kind: KindSD, WeightMB: 1500})
	require.False(t, ok)
	b, ok, _ := q.TryAcquire(Spec{Kind: KindSD, WeightMB: 1500})
	require.False(t, ok)

	st := q.Status()
	require.Len(t, st.Queue, 2)
	assert.Equal(t, a.ID(), st.Queue[0].ID)
	assert.Equal(t, b.ID(), st.Queue[1].ID)

	first.Release()

	st = q.Status()
	require.Len(t, st.Running, 1)
	assert.Equal(t, a.ID(), st.Running[0].ID)
	require.Len(t, st.Queue, 1)
	assert.Equal(t, b.ID(), st.Queue[0].ID)
}

func TestQueue_SkipAheadPastOversizedHead(t *testing.T) {
	t.Parallel()
	q := newTestQueue(10000, 5*time.Second, 30*time.Second)

	holder, err := q.Acquire(context.Background(), Spec{Kind: KindSD, WeightMB: 6000})
	require.NoError(t, err)

	big, acquired, pos := q.TryAcquire(Spec{Kind: KindSD, WeightMB: 6000})
	require.False(t, acquired)
	assert.Equal(t, 1, pos)

	small, acquired, pos := q.TryAcquire(Spec{Kind: KindYue, WeightMB: 3000})
	require.True(t, acquired)
	assert.Equal(t, 0, pos)

	st := q.Status()
	require.Len(t, st.Running, 2)
	require.Len(t, st.Queue, 1)
	assert.Equal(t, big.ID(), st.Queue[0].ID)

	holder.Release()
	small.Release()
	big.Release()
	assert.Empty(t, q.Status().Running)
}

func TestQueue_ExhaustedBudgetQueues(t *testing.T) {
	t.Parallel()
	q := newTestQueue(5000, 5*time.Second, 30*time.Second)

	lease, err := q.Acquire(context.Background(), Spec{Kind: KindLLM, WeightMB: 3000})
	require.NoError(t, err)

	waiting, acquired, pos := q.TryAcquire(Spec{Kind: KindLLM, WeightMB: 3000})
	require.False(t, acquired)
	assert.Equal(t, 1, pos)

	st := q.Status()
	assert.True(t, st.Enabled)
	assert.Equal(t, 5000, st.Budget)
	require.Len(t, st.Running, 1)
	require.Len(t, st.Queue, 1)
	assert.Equal(t, waiting.ID(), st.Queue[0].ID)
	assert.Equal(t, 1, st.Queue[0].Position)
	assert.True(t, st.Queue[0].StartedAt.IsZero())

	lease.Release()
	waiting.Release()
	st = q.Status()
	assert.Empty(t, st.Running)
	assert.Empty(t, st.Queue)
}

func TestAcquire_MaxWaitTimeout(t *testing.T) {
	q := newTestQueue(5000, 80*time.Millisecond, 30*time.Second)

	_, err := q.Acquire(context.Background(), Spec{Kind: KindSD, WeightMB: 5000})
	require.NoError(t, err)

	start := time.Now()
	lease, err := q.Acquire(context.Background(), Spec{Kind: KindSD, WeightMB: 4000})
	require.ErrorIs(t, err, ErrTimeout)
	assert.Nil(t, lease)
	assert.GreaterOrEqual(t, time.Since(start), 70*time.Millisecond)
	assert.Empty(t, q.Status().Queue)
	assert.Len(t, q.Status().Running, 1)
}

func TestAcquire_ContextCancelled(t *testing.T) {
	q := newTestQueue(5000, 5*time.Second, 30*time.Second)

	_, err := q.Acquire(context.Background(), Spec{Kind: KindSD, WeightMB: 5000})
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(30 * time.Millisecond)
		cancel()
	}()

	lease, err := q.Acquire(ctx, Spec{Kind: KindSD, WeightMB: 4000})
	require.ErrorIs(t, err, ErrCancelled)
	assert.Nil(t, lease)
	assert.Empty(t, q.Status().Queue)
}

func TestAcquire_PreCancelledContext(t *testing.T) {
	t.Parallel()
	q := newTestQueue(5000, 5*time.Second, 30*time.Second)

	_, err := q.Acquire(context.Background(), Spec{Kind: KindSD, WeightMB: 5000})
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	lease, err := q.Acquire(ctx, Spec{Kind: KindSD, WeightMB: 4000})
	require.ErrorIs(t, err, ErrCancelled)
	assert.Nil(t, lease)
	assert.Empty(t, q.Status().Queue)
}

func TestRelease_AdmitsBlockedAcquire(t *testing.T) {
	t.Parallel()
	q := newTestQueue(5000, 5*time.Second, 30*time.Second)

	first, err := q.Acquire(context.Background(), Spec{Kind: KindSD, WeightMB: 4000})
	require.NoError(t, err)

	done := make(chan *Lease, 1)
	errCh := make(chan error, 1)
	go func() {
		l, err := q.Acquire(context.Background(), Spec{Kind: KindYue, WeightMB: 4000})
		if err != nil {
			errCh <- err
			return
		}
		done <- l
	}()

	require.True(t, waitFor(t, time.Second, func() bool { return len(q.Status().Queue) == 1 }))
	first.Release()

	select {
	case l := <-done:
		require.NotNil(t, l)
		l.Release()
	case err := <-errCh:
		t.Fatalf("unexpected acquire error: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("blocked acquire did not return after release")
	}

	st := q.Status()
	assert.Empty(t, st.Running)
	assert.Empty(t, st.Queue)
}

func TestHeartbeat_ExtendsDeadlineThenEvicts(t *testing.T) {
	q := newTestQueue(5000, 5*time.Second, 100*time.Millisecond)

	lease, err := q.Acquire(context.Background(), Spec{Kind: KindSD, WeightMB: 3000})
	require.NoError(t, err)

	before, ok := q.Get(lease.ID())
	require.True(t, ok)
	require.False(t, before.LeaseDeadline.IsZero())

	time.Sleep(40 * time.Millisecond)
	require.True(t, q.Heartbeat(lease.ID()))

	after, ok := q.Get(lease.ID())
	require.True(t, ok)
	assert.True(t, after.LeaseDeadline.After(before.LeaseDeadline))

	waiting, acquired, _ := q.TryAcquire(Spec{Kind: KindYue, WeightMB: 3000})
	require.False(t, acquired)

	time.Sleep(120 * time.Millisecond)
	q.evictExpired()

	assert.False(t, q.Heartbeat(lease.ID()))
	st := q.Status()
	require.Len(t, st.Running, 1)
	assert.Equal(t, waiting.ID(), st.Running[0].ID)
	assert.Empty(t, st.Queue)

	found := false
	for _, w := range st.Warnings {
		if strings.Contains(w, "lease expired") {
			found = true
		}
	}
	assert.True(t, found, "expected expiry warning, got %v", st.Warnings)

	waiting.Release()
	assert.Empty(t, q.Status().Running)
}

func TestQueue_SweeperEvictsExpiredLease(t *testing.T) {
	q := newTestQueue(5000, 5*time.Second, 10*time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	q.Start(ctx)

	lease, err := q.Acquire(context.Background(), Spec{Kind: KindSD, WeightMB: 5000})
	require.NoError(t, err)

	waiting, acquired, _ := q.TryAcquire(Spec{Kind: KindYue, WeightMB: 5000})
	require.False(t, acquired)

	require.True(t, waitFor(t, 8*time.Second, func() bool {
		j, ok := q.Get(waiting.ID())
		return ok && !j.StartedAt.IsZero()
	}), "sweeper did not dispatch waiting job")

	assert.False(t, q.Heartbeat(lease.ID()))
	waiting.Release()
	assert.Empty(t, q.Status().Running)
}

func TestCancel_RunningAndQueued(t *testing.T) {
	t.Parallel()
	q := newTestQueue(5000, 5*time.Second, 30*time.Second)

	running, err := q.Acquire(context.Background(), Spec{Kind: KindSD, WeightMB: 3000})
	require.NoError(t, err)

	queued, acquired, _ := q.TryAcquire(Spec{Kind: KindYue, WeightMB: 3000})
	require.False(t, acquired)

	require.True(t, q.Cancel(queued.ID()))
	assert.Empty(t, q.Status().Queue)
	assert.False(t, q.Cancel(queued.ID()))

	require.True(t, q.Cancel(running.ID()))
	st := q.Status()
	assert.Empty(t, st.Running)
	assert.Empty(t, st.Queue)

	promoted, acquired, _ := q.TryAcquire(Spec{Kind: KindLLM, WeightMB: 5000})
	require.True(t, acquired)
	promoted.Release()
}

func TestMove_ReordersWaiting(t *testing.T) {
	t.Parallel()
	q := newTestQueue(1000, 5*time.Second, 30*time.Second)

	holder, err := q.Acquire(context.Background(), Spec{Kind: KindSD, WeightMB: 1000})
	require.NoError(t, err)

	a, okA, _ := q.TryAcquire(Spec{Kind: KindSD, WeightMB: 1000})
	require.False(t, okA)
	b, okB, _ := q.TryAcquire(Spec{Kind: KindSD, WeightMB: 1000})
	require.False(t, okB)
	c, okC, _ := q.TryAcquire(Spec{Kind: KindSD, WeightMB: 1000})
	require.False(t, okC)

	require.True(t, q.Move(c.ID(), -1))
	st := q.Status()
	require.Len(t, st.Queue, 3)
	assert.Equal(t, c.ID(), st.Queue[0].ID)
	assert.Equal(t, -1, st.Queue[0].Priority)

	require.True(t, q.Move(c.ID(), 1))
	st = q.Status()
	assert.Equal(t, 0, st.Queue[0].Priority)
	assert.Equal(t, a.ID(), st.Queue[0].ID)

	require.True(t, q.Move(a.ID(), 1))
	st = q.Status()
	assert.Equal(t, a.ID(), st.Queue[len(st.Queue)-1].ID)

	assert.False(t, q.Move("sd-999", -1))

	holder.Release()
	a.Release()
	b.Release()
	c.Release()
	assert.Empty(t, q.Status().Running)
	assert.Empty(t, q.Status().Queue)
}

func TestMove_PriorityClamped(t *testing.T) {
	t.Parallel()
	q := newTestQueue(1000, 5*time.Second, 30*time.Second)

	holder, err := q.Acquire(context.Background(), Spec{Kind: KindSD, WeightMB: 1000})
	require.NoError(t, err)
	defer holder.Release()

	j, acquired, _ := q.TryAcquire(Spec{Kind: KindYue, WeightMB: 1000})
	require.False(t, acquired)

	for i := 0; i < 12; i++ {
		require.True(t, q.Move(j.ID(), -1))
	}
	snap, ok := q.Get(j.ID())
	require.True(t, ok)
	assert.Equal(t, -9, snap.Priority)

	for i := 0; i < 20; i++ {
		require.True(t, q.Move(j.ID(), 1))
	}
	snap, ok = q.Get(j.ID())
	require.True(t, ok)
	assert.Equal(t, 9, snap.Priority)

	j.Release()
}

func TestRelease_UnknownIDWarns(t *testing.T) {
	t.Parallel()
	q := newTestQueue(1000, 5*time.Second, 30*time.Second)

	q.Release("sd-999")

	st := q.Status()
	require.Len(t, st.Warnings, 1)
	assert.Contains(t, st.Warnings[0], "release unknown lease sd-999")
	assert.Empty(t, st.Running)
	assert.Empty(t, st.Queue)
}

func TestQueue_WarningsRingBuffer(t *testing.T) {
	t.Parallel()
	q := New(Config{TotalBudgetMB: 1000, MaxWait: time.Second, LeaseTTL: time.Second, MaxWarnings: 3})

	for i := 0; i < 5; i++ {
		q.Release(fmt.Sprintf("unknown-%d", i))
	}

	st := q.Status()
	require.Len(t, st.Warnings, 3)
	joined := strings.Join(st.Warnings, " ")
	assert.Contains(t, st.Warnings[2], "unknown-4")
	assert.Contains(t, joined, "unknown-2")
	assert.NotContains(t, joined, "unknown-0")
}

func TestQueue_DisabledBudget(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		budget int
	}{
		{"zero budget", 0},
		{"negative budget", -100},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			q := New(Config{TotalBudgetMB: tt.budget, MaxWait: 10 * time.Millisecond, LeaseTTL: time.Second})

			assert.False(t, q.Enabled())

			lease, err := q.Acquire(context.Background(), Spec{Kind: KindSD, WeightMB: 999999})
			require.NoError(t, err)
			lease.Release()

			lease, acquired, pos := q.TryAcquire(Spec{Kind: KindLLM, WeightMB: 999999})
			require.True(t, acquired)
			assert.Equal(t, 0, pos)
			lease.Release()

			wl, err := q.WaitLease(context.Background(), "any")
			require.NoError(t, err)
			wl.Release()

			assert.False(t, q.Heartbeat("any"))

			st := q.Status()
			assert.False(t, st.Enabled)
			assert.Empty(t, st.Running)
			assert.Empty(t, st.Queue)
		})
	}
}

func TestAcquire_NonPositiveWeightFailsOpen(t *testing.T) {
	t.Parallel()
	q := newTestQueue(1000, 10*time.Millisecond, time.Second)

	lease, err := q.Acquire(context.Background(), Spec{Kind: KindSD, WeightMB: 0})
	require.NoError(t, err)
	assert.Empty(t, lease.ID())
	lease.Release()

	st := q.Status()
	require.Len(t, st.Warnings, 1)
	assert.Contains(t, st.Warnings[0], "non-positive weight")
	assert.Empty(t, st.Running)
	assert.Empty(t, st.Queue)
}

func TestAcquire_AutoRenewKeepsLeaseAlive(t *testing.T) {
	q := newTestQueue(1000, 5*time.Second, 300*time.Millisecond)

	lease, err := q.Acquire(context.Background(), Spec{Kind: KindSD, WeightMB: 1000, AutoRenew: true})
	require.NoError(t, err)

	time.Sleep(500 * time.Millisecond)
	q.evictExpired()

	st := q.Status()
	require.Len(t, st.Running, 1)
	assert.Equal(t, lease.ID(), st.Running[0].ID)

	lease.Release()
	assert.Empty(t, q.Status().Running)
}

func TestGet_UnknownID(t *testing.T) {
	t.Parallel()
	q := newTestQueue(1000, time.Second, time.Second)

	_, ok := q.Get("nope")
	assert.False(t, ok)
}

func TestWaitLease_RunningQueuedUnknown(t *testing.T) {
	t.Parallel()
	q := newTestQueue(1000, 5*time.Second, 30*time.Second)

	_, err := q.WaitLease(context.Background(), "nope")
	require.ErrorIs(t, err, ErrNotFound)

	holder, err := q.Acquire(context.Background(), Spec{Kind: KindSD, WeightMB: 1000})
	require.NoError(t, err)

	queued, acquired, _ := q.TryAcquire(Spec{Kind: KindYue, WeightMB: 1000})
	require.False(t, acquired)

	running, err := q.WaitLease(context.Background(), holder.ID())
	require.NoError(t, err)
	assert.Equal(t, holder.ID(), running.ID())

	done := make(chan *Lease, 1)
	go func() {
		l, err := q.WaitLease(context.Background(), queued.ID())
		if err == nil {
			done <- l
		}
	}()

	holder.Release()

	select {
	case l := <-done:
		assert.Equal(t, queued.ID(), l.ID())
	case <-time.After(2 * time.Second):
		t.Fatal("WaitLease not resolved after admit")
	}
}

func TestWaitLease_ContextCancelled(t *testing.T) {
	q := newTestQueue(1000, 5*time.Second, 30*time.Second)

	holder, err := q.Acquire(context.Background(), Spec{Kind: KindSD, WeightMB: 1000})
	require.NoError(t, err)
	defer holder.Release()

	queued, acquired, _ := q.TryAcquire(Spec{Kind: KindYue, WeightMB: 1000})
	require.False(t, acquired)

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(30 * time.Millisecond)
		cancel()
	}()

	_, err = q.WaitLease(ctx, queued.ID())
	require.ErrorIs(t, err, ErrCancelled)
}

func TestAcquire_CancelWhileWaitingReturnsCancelled(t *testing.T) {
	q := newTestQueue(1000, 5*time.Second, 30*time.Second)

	holder, err := q.Acquire(context.Background(), Spec{Kind: KindSD, WeightMB: 1000})
	require.NoError(t, err)
	defer holder.Release()

	errCh := make(chan error, 1)
	leaseCh := make(chan *Lease, 1)
	go func() {
		l, err := q.Acquire(context.Background(), Spec{Kind: KindSD, WeightMB: 1000})
		if err != nil {
			errCh <- err
			return
		}
		leaseCh <- l
	}()

	require.True(t, waitFor(t, time.Second, func() bool { return len(q.Status().Queue) == 1 }))
	queuedID := q.Status().Queue[0].ID
	require.True(t, q.Cancel(queuedID))

	select {
	case err := <-errCh:
		require.ErrorIs(t, err, ErrCancelled)
	case l := <-leaseCh:
		t.Fatalf("expected error, got lease %s", l.ID())
	case <-time.After(2 * time.Second):
		t.Fatal("acquire did not return after cancel")
	}

	st := q.Status()
	assert.Empty(t, st.Queue)
	require.Len(t, st.Running, 1)
	assert.Equal(t, holder.ID(), st.Running[0].ID)
}

func TestAcquire_AdmittedBeforeTimeoutReturnsLease(t *testing.T) {
	q := newTestQueue(1000, 400*time.Millisecond, 30*time.Second)

	holder, err := q.Acquire(context.Background(), Spec{Kind: KindSD, WeightMB: 1000})
	require.NoError(t, err)

	go func() {
		time.Sleep(100 * time.Millisecond)
		holder.Release()
	}()

	lease, err := q.Acquire(context.Background(), Spec{Kind: KindSD, WeightMB: 1000})
	require.NoError(t, err)
	require.NotNil(t, lease)
	assert.NotEmpty(t, lease.ID())

	require.True(t, waitFor(t, time.Second, func() bool { return len(q.Status().Running) == 1 }))
	lease.Release()
	assert.Empty(t, q.Status().Running)
}

func TestEvictExpired_DropsAbandonedWaiting(t *testing.T) {
	q := newTestQueue(1000, 30*time.Millisecond, time.Minute)

	holder, err := q.Acquire(context.Background(), Spec{Kind: KindSD, WeightMB: 1000})
	require.NoError(t, err)
	defer holder.Release()

	orphan, acquired, _ := q.TryAcquire(Spec{Kind: KindYue, WeightMB: 1000})
	require.False(t, acquired)
	require.Len(t, q.Status().Queue, 1)

	time.Sleep(60 * time.Millisecond)
	q.evictExpired()

	st := q.Status()
	assert.Empty(t, st.Queue)
	require.Len(t, st.Running, 1)
	found := false
	for _, w := range st.Warnings {
		if strings.Contains(w, "job abandoned: "+orphan.ID()) {
			found = true
		}
	}
	assert.True(t, found, "expected abandoned warning, got %v", st.Warnings)
}

func TestEvictExpired_DispatchesBeforeAbandonedDrop(t *testing.T) {
	q := newTestQueue(1000, 30*time.Millisecond, 20*time.Millisecond)

	expired, err := q.Acquire(context.Background(), Spec{Kind: KindSD, WeightMB: 1000})
	require.NoError(t, err)
	_ = expired

	waiting, acquired, _ := q.TryAcquire(Spec{Kind: KindYue, WeightMB: 1000})
	require.False(t, acquired)

	time.Sleep(60 * time.Millisecond)
	q.evictExpired()

	st := q.Status()
	assert.Empty(t, st.Queue)
	require.Len(t, st.Running, 1)
	assert.Equal(t, waiting.ID(), st.Running[0].ID)
	waiting.Release()
}

func TestQueue_MaxQueueLenRejects(t *testing.T) {
	q := New(Config{TotalBudgetMB: 1000, MaxWait: 5 * time.Second, LeaseTTL: time.Second, MaxQueueLen: 2})

	holder, err := q.Acquire(context.Background(), Spec{Kind: KindSD, WeightMB: 1000})
	require.NoError(t, err)
	defer holder.Release()

	for i := 0; i < 2; i++ {
		_, acquired, pos := q.TryAcquire(Spec{Kind: KindYue, WeightMB: 1000})
		require.False(t, acquired)
		assert.Equal(t, i+1, pos)
	}

	assert.True(t, q.QueueFull())

	lease, acquired, pos := q.TryAcquire(Spec{Kind: KindYue, WeightMB: 1000})
	require.False(t, acquired)
	assert.Zero(t, pos)
	assert.Nil(t, lease)

	_, err = q.Acquire(context.Background(), Spec{Kind: KindYue, WeightMB: 1000})
	require.ErrorIs(t, err, ErrQueueFull)
	assert.Len(t, q.Status().Queue, 2)
}

func TestQueue_UnlimitedQueueLenWhenNegative(t *testing.T) {
	q := New(Config{TotalBudgetMB: 100, MaxWait: time.Hour, LeaseTTL: time.Hour, MaxQueueLen: -1})

	holder, err := q.Acquire(context.Background(), Spec{Kind: KindSD, WeightMB: 100})
	require.NoError(t, err)
	defer holder.Release()

	for i := 0; i < 150; i++ {
		_, acquired, _ := q.TryAcquire(Spec{Kind: KindYue, WeightMB: 100})
		require.False(t, acquired)
	}
	assert.Len(t, q.Status().Queue, 150)
	assert.False(t, q.QueueFull())
}

func TestQueue_DefaultMaxQueueLen100(t *testing.T) {
	q := newTestQueue(100, 50*time.Millisecond, time.Minute)

	holder, err := q.Acquire(context.Background(), Spec{Kind: KindSD, WeightMB: 100})
	require.NoError(t, err)
	defer holder.Release()

	for i := 0; i < 100; i++ {
		_, acquired, _ := q.TryAcquire(Spec{Kind: KindYue, WeightMB: 100})
		require.False(t, acquired)
	}

	_, err = q.Acquire(context.Background(), Spec{Kind: KindYue, WeightMB: 100})
	require.ErrorIs(t, err, ErrQueueFull)
	assert.Len(t, q.Status().Queue, 100)
}

func TestSanitizeClient(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"plain", "yue-worker", "yue-worker"},
		{"esc osc boom", "\u001b]52;c;boom\u0007x", "]52;c;boomx"},
		{"csi erase", "a\u001b[2Jb", "a[2Jb"},
		{"tab newline cr", "a\tb\nc\rd", "abcd"},
		{"del", "a\u007fb", "ab"},
		{"c1 csi", "a\u009bb", "ab"},
		{"nbsp kept", "a\u00a0b", "a\u00a0b"},
		{"cyrillic kept", "клиент", "клиент"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, SanitizeClient(tt.in))
		})
	}
}
