package gpuproxy

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEnqueueDequeue_HighFirst(t *testing.T) {
	q := NewPriorityQueue(1)

	q.Enqueue("ollama", PriorityLow)
	q.Enqueue("ollama", PriorityHigh)

	req, ok := q.Dequeue(context.Background())
	require.True(t, ok)
	assert.Equal(t, PriorityHigh, req.Priority)

	req, ok = q.Dequeue(context.Background())
	require.True(t, ok)
	assert.Equal(t, PriorityLow, req.Priority)
}

func TestEnqueueDequeue_FIFO(t *testing.T) {
	q := NewPriorityQueue(1)

	q.Enqueue("ollama", PriorityHigh)
	q.Enqueue("ollama", PriorityHigh)
	q.Enqueue("ollama", PriorityHigh)

	req1, ok := q.Dequeue(context.Background())
	require.True(t, ok)

	req2, ok := q.Dequeue(context.Background())
	require.True(t, ok)

	req3, ok := q.Dequeue(context.Background())
	require.True(t, ok)

	assert.True(t, req1.ID < req2.ID)
	assert.True(t, req2.ID < req3.ID)
}

func TestEnqueueDequeue_HighDrainedBeforeLow(t *testing.T) {
	q := NewPriorityQueue(1)

	q.Enqueue("ollama", PriorityLow)
	q.Enqueue("ollama", PriorityLow)
	q.Enqueue("ollama", PriorityHigh)
	q.Enqueue("ollama", PriorityHigh)
	q.Enqueue("ollama", PriorityLow)

	req1, _ := q.Dequeue(context.Background())
	assert.Equal(t, PriorityHigh, req1.Priority)

	req2, _ := q.Dequeue(context.Background())
	assert.Equal(t, PriorityHigh, req2.Priority)

	req3, _ := q.Dequeue(context.Background())
	assert.Equal(t, PriorityLow, req3.Priority)

	req4, _ := q.Dequeue(context.Background())
	assert.Equal(t, PriorityLow, req4.Priority)

	req5, _ := q.Dequeue(context.Background())
	assert.Equal(t, PriorityLow, req5.Priority)
}

func TestDequeue_BlocksOnEmpty(t *testing.T) {
	q := NewPriorityQueue(1)

	go func() {
		time.Sleep(50 * time.Millisecond)
		q.Enqueue("ollama", PriorityHigh)
	}()

	req, ok := q.Dequeue(context.Background())
	require.True(t, ok)
	assert.Equal(t, PriorityHigh, req.Priority)
}

func TestDequeue_ContextCancel(t *testing.T) {
	q := NewPriorityQueue(1)
	ctx, cancel := context.WithCancel(context.Background())

	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	_, ok := q.Dequeue(ctx)
	assert.False(t, ok)
}

func TestStats(t *testing.T) {
	q := NewPriorityQueue(1)

	q.Enqueue("ollama", PriorityHigh)
	q.Enqueue("sd", PriorityLow)
	q.Enqueue("ollama", PriorityHigh)

	stats := q.Stats()
	assert.Equal(t, 2, stats.HighWaiting)
	assert.Equal(t, 1, stats.LowWaiting)
	assert.Equal(t, 1, stats.TotalSlots)
}

func TestLen(t *testing.T) {
	q := NewPriorityQueue(1)
	assert.Equal(t, 0, q.Len())

	q.Enqueue("ollama", PriorityHigh)
	q.Enqueue("sd", PriorityLow)
	assert.Equal(t, 2, q.Len())
}

func TestRelease_SignalsNewDequeue(t *testing.T) {
	q := NewPriorityQueue(1)

	q.Enqueue("ollama", PriorityHigh)
	req1, ok := q.Dequeue(context.Background())
	require.True(t, ok)

	close(req1.Complete)
	q.Release()

	go func() {
		time.Sleep(50 * time.Millisecond)
		q.Enqueue("sd", PriorityLow)
	}()

	req2, ok := q.Dequeue(context.Background())
	require.True(t, ok)
	assert.Equal(t, PriorityLow, req2.Priority)
}

func TestNewPriorityQueue_MinSlots(t *testing.T) {
	q := NewPriorityQueue(0)
	assert.Equal(t, 1, q.slots)

	q = NewPriorityQueue(-5)
	assert.Equal(t, 1, q.slots)
}

func TestRequest_HasChannels(t *testing.T) {
	q := NewPriorityQueue(1)
	qr := q.Enqueue("ollama", PriorityHigh)

	assert.NotNil(t, qr.Proceed)
	assert.NotNil(t, qr.Complete)
	assert.Equal(t, "ollama", qr.Endpoint)
	assert.False(t, qr.Enqueued.IsZero())
}
