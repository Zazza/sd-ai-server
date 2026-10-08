package main

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"sd-studio-server/gpuqueue"
	"sd-studio-server/tui"
)

func TestGpuQueueSnapshot_MapsJobs(t *testing.T) {
	t.Parallel()
	submitted := time.Date(2026, 10, 8, 10, 0, 0, 0, time.UTC)
	deadline := submitted.Add(5 * time.Minute)
	st := gpuqueue.Status{
		Budget: 15884,
		Running: []gpuqueue.Job{{
			ID:            "run-1",
			Kind:          gpuqueue.KindSD,
			Client:        "192.168.1.50:777",
			WeightMB:      5000,
			Priority:      2,
			SubmittedAt:   submitted,
			LeaseDeadline: deadline,
		}},
		Queue: []gpuqueue.Job{{
			ID:          "wait-1",
			Kind:        gpuqueue.KindLLM,
			Client:      "yue",
			WeightMB:    3000,
			Priority:    1,
			SubmittedAt: submitted.Add(90 * time.Second),
		}},
		Warnings: []string{"stale lease"},
	}

	snap := gpuQueueSnapshot(st)

	assert.Equal(t, 15884, snap.Budget)
	require.Len(t, snap.Running, 1)
	assert.Equal(t, tui.QueueJob{
		ID:            "run-1",
		Kind:          "sd",
		Client:        "192.168.1.50:777",
		WeightMB:      5000,
		Priority:      2,
		SubmittedAt:   submitted,
		LeaseDeadline: deadline,
	}, snap.Running[0])
	require.Len(t, snap.Waiting, 1)
	assert.Equal(t, tui.QueueJob{
		ID:          "wait-1",
		Kind:        "llm",
		Client:      "yue",
		WeightMB:    3000,
		Priority:    1,
		SubmittedAt: submitted.Add(90 * time.Second),
	}, snap.Waiting[0])
	assert.True(t, snap.Waiting[0].LeaseDeadline.IsZero())
	assert.Equal(t, []string{"stale lease"}, snap.Warnings)
}

func TestGpuQueueSnapshot_NilSlicesStayEmpty(t *testing.T) {
	t.Parallel()
	snap := gpuQueueSnapshot(gpuqueue.Status{Budget: 100})
	assert.Equal(t, 100, snap.Budget)
	assert.Empty(t, snap.Running)
	assert.Empty(t, snap.Waiting)
	assert.Empty(t, snap.Warnings)
}

func TestGpuQueueSnapshot_WarningsCopied(t *testing.T) {
	t.Parallel()
	source := []string{"a", "b"}
	st := gpuqueue.Status{Budget: 1, Warnings: source}

	snap := gpuQueueSnapshot(st)
	require.Equal(t, []string{"a", "b"}, snap.Warnings)

	source[0] = "mutated"
	assert.Equal(t, []string{"a", "b"}, snap.Warnings)

	snap.Warnings[1] = "changed"
	assert.Equal(t, "b", st.Warnings[1])
}

func TestQueueJobDTO_SanitizesClient(t *testing.T) {
	t.Parallel()
	dto := queueJobDTO(gpuqueue.Job{
		ID:     "sd-1",
		Kind:   gpuqueue.KindSD,
		Client: "\u001b]52;c;boom\u0007x\ty\nz\u009bm",
	})

	assert.Equal(t, "]52;c;boomxyzm", dto.Client)
	for _, bad := range []string{"\u001b", "\u0007", "\t", "\n", "\u009b"} {
		assert.NotContains(t, dto.Client, bad)
	}
}
