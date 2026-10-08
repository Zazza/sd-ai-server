package tui

import (
	"errors"
	"fmt"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type queueMoveCall struct {
	id  string
	dir int
}

type queueActions struct {
	moves   []queueMoveCall
	cancels []string
	err     error
}

func (a *queueActions) move(id string, dir int) error {
	a.moves = append(a.moves, queueMoveCall{id: id, dir: dir})
	return a.err
}

func (a *queueActions) cancel(id string) error {
	a.cancels = append(a.cancels, id)
	return a.err
}

func queueTestModel(deps ServerDeps) DashboardModel {
	m := NewDashboardModel(deps, "127.0.0.1")
	m.width, m.height = 120, 40
	return m
}

func queueTestDeps(snap QueueSnapshot, act *queueActions) ServerDeps {
	deps := ServerDeps{QueueSnapshot: func() QueueSnapshot { return snap }}
	if act != nil {
		deps.QueueMove = act.move
		deps.QueueCancel = act.cancel
	}
	return deps
}

func runeKey(s string) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

func pressKey(m DashboardModel, key tea.KeyMsg) DashboardModel {
	next, _ := m.Update(key)
	return next.(DashboardModel)
}

func assertSelectedJob(t *testing.T, m DashboardModel, id string, waiting bool) {
	t.Helper()
	job, w, ok := m.selectedQueueJob()
	require.True(t, ok, "expected selection for %s", id)
	assert.Equal(t, id, job.ID)
	assert.Equal(t, waiting, w)
}

func queueNavSnapshot() QueueSnapshot {
	return QueueSnapshot{
		Budget:  10000,
		Running: []QueueJob{{ID: "r1", WeightMB: 1000}, {ID: "r2", WeightMB: 1000}},
		Waiting: []QueueJob{{ID: "w1", WeightMB: 500}, {ID: "w2", WeightMB: 500}},
	}
}

func TestViewQueue_RenderSectionsAndFields(t *testing.T) {
	t.Parallel()
	now := time.Now()
	snap := QueueSnapshot{
		Budget: 15884,
		Running: []QueueJob{{
			ID: "run-1", Kind: "sd", Client: "192.168.1.50:777", WeightMB: 5000, Priority: 2,
			SubmittedAt: now.Add(-time.Minute), LeaseDeadline: now.Add(5 * time.Minute),
		}},
		Waiting: []QueueJob{{
			ID: "wait-1", Kind: "llm", Client: "yue-worker", WeightMB: 3000, Priority: 1,
			SubmittedAt: now.Add(-30 * time.Second),
		}},
		Warnings: []string{"stale lease"},
	}
	m := queueTestModel(queueTestDeps(snap, nil))
	m.showQueue = true

	view := m.View()
	for _, want := range []string{
		"GPU Queue", "RUNNING", "QUEUE",
		"5000/15884MB", "run 1", "queue 1", "warn 1",
		"run-1", "wait-1", "sd", "llm", "192.168.1.50:777", "yue-worker",
		"5000MB", "3000MB", "p 2", "p 1", "lease", "waits",
	} {
		assert.Contains(t, view, want)
	}
}

func TestBuildQueueRows_RunningSortDeterministic(t *testing.T) {
	t.Parallel()
	base := time.Date(2026, 10, 8, 10, 0, 0, 0, time.UTC)
	jobs := []QueueJob{
		{ID: "c", SubmittedAt: base},
		{ID: "b", SubmittedAt: base.Add(time.Minute)},
		{ID: "z", SubmittedAt: base.Add(2 * time.Minute)},
		{ID: "a", SubmittedAt: base.Add(time.Minute)},
	}
	want := []string{"c", "a", "b", "z"}

	for _, perm := range [][]int{{0, 1, 2, 3}, {3, 2, 1, 0}, {2, 0, 3, 1}, {1, 3, 0, 2}} {
		in := []QueueJob{jobs[perm[0]], jobs[perm[1]], jobs[perm[2]], jobs[perm[3]]}
		snap := QueueSnapshot{
			Budget:  100,
			Running: in,
			Waiting: []QueueJob{{ID: "w1"}, {ID: "w2"}},
		}

		rows := buildQueueRows(snap)
		require.Len(t, rows, 8)
		require.True(t, rows[0].isHeader)
		assert.Equal(t, "RUNNING", rows[0].header)

		var got []string
		for i := 1; i <= 4; i++ {
			require.False(t, rows[i].isHeader)
			got = append(got, rows[i].job.ID)
			assert.Equal(t, i-1, rows[i].idx, "job %s", rows[i].job.ID)
			assert.False(t, rows[i].waiting)
		}
		assert.Equal(t, want, got)

		require.True(t, rows[5].isHeader)
		assert.Equal(t, "QUEUE", rows[5].header)
		for i, r := range rows[6:] {
			assert.Equal(t, i+1, r.pos)
			assert.True(t, r.waiting)
			assert.Equal(t, 4+i, r.idx)
		}

		assert.Equal(t, []QueueJob{jobs[perm[0]], jobs[perm[1]], jobs[perm[2]], jobs[perm[3]]}, in)
	}
}

func TestBuildQueueRows_EmptySnapshot(t *testing.T) {
	t.Parallel()
	assert.Empty(t, buildQueueRows(QueueSnapshot{Budget: 100}))
	assert.Empty(t, buildQueueRows(QueueSnapshot{Budget: 100, Running: []QueueJob{}}))
}

func TestUpdate_KeyGOpensQueueScreen(t *testing.T) {
	t.Parallel()
	m := queueTestModel(queueTestDeps(QueueSnapshot{Budget: 1000}, nil))

	next := pressKey(m, runeKey("g"))
	assert.True(t, next.showQueue)
	assert.Zero(t, next.queueSel)
	assert.Empty(t, next.queueFlash)
	assert.Contains(t, next.View(), "GPU Queue")
}

func TestUpdate_KeyGWithoutQueueDepIgnored(t *testing.T) {
	t.Parallel()
	m := queueTestModel(ServerDeps{})

	next := pressKey(m, runeKey("g"))
	assert.False(t, next.showQueue)
	assert.NotContains(t, next.View(), "GPU Queue")
}

func TestUpdate_KeyQAndEscCloseQueueScreen(t *testing.T) {
	t.Parallel()
	for _, key := range []tea.KeyMsg{runeKey("q"), {Type: tea.KeyEsc}} {
		m := queueTestModel(queueTestDeps(queueNavSnapshot(), nil))
		m.showQueue = true
		m.queueSel = 3
		m.queueFlash = "error: boom"

		next := pressKey(m, key)
		assert.False(t, next.showQueue)
		assert.Zero(t, next.queueSel)
		assert.Empty(t, next.queueFlash)
		assert.NotContains(t, next.View(), "GPU Queue")
	}
}

func TestUpdate_QueueNavigation(t *testing.T) {
	t.Parallel()
	m := queueTestModel(queueTestDeps(queueNavSnapshot(), nil))
	m = pressKey(m, runeKey("g"))
	assertSelectedJob(t, m, "r1", false)

	steps := []struct {
		key     tea.KeyMsg
		wantID  string
		waiting bool
	}{
		{runeKey("j"), "r2", false},
		{runeKey("j"), "w1", true},
		{runeKey("j"), "w2", true},
		{runeKey("j"), "w2", true},
		{runeKey("k"), "w1", true},
		{runeKey("k"), "r2", false},
		{runeKey("k"), "r1", false},
		{runeKey("k"), "r1", false},
		{tea.KeyMsg{Type: tea.KeyDown}, "r2", false},
		{tea.KeyMsg{Type: tea.KeyDown}, "w1", true},
		{tea.KeyMsg{Type: tea.KeyUp}, "r2", false},
		{tea.KeyMsg{Type: tea.KeyUp}, "r1", false},
	}
	for _, s := range steps {
		m = pressKey(m, s.key)
		assertSelectedJob(t, m, s.wantID, s.waiting)
	}
}

func TestUpdate_QueueNavigationEmptyList(t *testing.T) {
	t.Parallel()
	m := queueTestModel(queueTestDeps(QueueSnapshot{Budget: 1000}, nil))
	m = pressKey(m, runeKey("g"))

	for _, key := range []tea.KeyMsg{
		runeKey("j"), runeKey("k"),
		{Type: tea.KeyDown}, {Type: tea.KeyUp},
	} {
		m = pressKey(m, key)
	}

	view := m.View()
	assert.Contains(t, view, "job 0/0")
	assert.Contains(t, view, "no jobs")
}

func TestUpdate_QueueMoveOnlyWaiting(t *testing.T) {
	t.Parallel()
	act := &queueActions{}
	m := queueTestModel(queueTestDeps(queueNavSnapshot(), act))
	m = pressKey(m, runeKey("g"))

	m = pressKey(m, runeKey("u"))
	m = pressKey(m, runeKey("d"))
	assert.Empty(t, act.moves)

	m = pressKey(m, runeKey("j"))
	m = pressKey(m, runeKey("j"))
	assertSelectedJob(t, m, "w1", true)

	next, cmd := m.Update(runeKey("u"))
	require.NotNil(t, cmd)
	msg, ok := cmd().(queueResultMsg)
	require.True(t, ok)
	assert.NoError(t, msg.err)
	assert.Equal(t, []queueMoveCall{{id: "w1", dir: -1}}, act.moves)

	next, cmd = next.(DashboardModel).Update(runeKey("d"))
	require.NotNil(t, cmd)
	_, ok = cmd().(queueResultMsg)
	require.True(t, ok)
	assert.Equal(t, []queueMoveCall{{id: "w1", dir: -1}, {id: "w1", dir: 1}}, act.moves)
}

func TestUpdate_QueueMoveWithoutDepNoPanic(t *testing.T) {
	t.Parallel()
	m := queueTestModel(queueTestDeps(queueNavSnapshot(), nil))
	m = pressKey(m, runeKey("g"))
	m = pressKey(m, runeKey("j"))
	m = pressKey(m, runeKey("j"))
	assertSelectedJob(t, m, "w1", true)

	next, cmd := m.Update(runeKey("u"))
	assert.Nil(t, cmd)
	next, cmd = next.(DashboardModel).Update(runeKey("d"))
	assert.Nil(t, cmd)
}

func TestUpdate_QueueCancelRunningAndWaiting(t *testing.T) {
	t.Parallel()
	act := &queueActions{}
	m := queueTestModel(queueTestDeps(queueNavSnapshot(), act))
	m = pressKey(m, runeKey("g"))

	next, cmd := m.Update(runeKey("x"))
	require.NotNil(t, cmd)
	msg, ok := cmd().(queueResultMsg)
	require.True(t, ok)
	assert.NoError(t, msg.err)
	assert.Equal(t, []string{"r1"}, act.cancels)

	m = pressKey(next.(DashboardModel), runeKey("j"))
	m = pressKey(m, runeKey("j"))
	assertSelectedJob(t, m, "w1", true)

	_, cmd = m.Update(runeKey("x"))
	require.NotNil(t, cmd)
	_, ok = cmd().(queueResultMsg)
	require.True(t, ok)
	assert.Equal(t, []string{"r1", "w1"}, act.cancels)
}

func TestUpdate_QueueCancelWithoutDepNoPanic(t *testing.T) {
	t.Parallel()
	m := queueTestModel(queueTestDeps(queueNavSnapshot(), nil))
	m = pressKey(m, runeKey("g"))

	_, cmd := m.Update(runeKey("x"))
	assert.Nil(t, cmd)
}

func TestUpdate_QueueResultMsgSetsFlash(t *testing.T) {
	t.Parallel()
	m := queueTestModel(queueTestDeps(QueueSnapshot{Budget: 1000}, nil))
	m.showQueue = true

	next, _ := m.Update(queueResultMsg{err: errors.New("lease gone")})
	d, ok := next.(DashboardModel)
	require.True(t, ok)
	assert.Equal(t, "error: lease gone", d.queueFlash)
	assert.Contains(t, d.View(), "error: lease gone")

	next, _ = d.Update(queueResultMsg{})
	d, ok = next.(DashboardModel)
	require.True(t, ok)
	assert.Empty(t, d.queueFlash)
	assert.NotContains(t, d.View(), "error: lease gone")
}

func TestUpdate_QueueResultMsgCarriesError(t *testing.T) {
	t.Parallel()
	act := &queueActions{err: errors.New("boom")}
	m := queueTestModel(queueTestDeps(queueNavSnapshot(), act))
	m = pressKey(m, runeKey("g"))

	_, cmd := m.Update(runeKey("x"))
	require.NotNil(t, cmd)
	msg, ok := cmd().(queueResultMsg)
	require.True(t, ok)
	assert.EqualError(t, msg.err, "boom")
}

func TestViewQueue_DisabledWhenBudgetNotPositive(t *testing.T) {
	t.Parallel()
	for _, budget := range []int{0, -5} {
		snap := QueueSnapshot{
			Budget:  budget,
			Running: []QueueJob{{ID: "r1"}},
			Waiting: []QueueJob{{ID: "w1"}},
		}
		m := queueTestModel(queueTestDeps(snap, nil))
		m.showQueue = true

		view := m.View()
		assert.Contains(t, view, "GPU queue disabled")
		assert.Contains(t, view, "[q] back")
		assert.NotContains(t, view, "RUNNING")
		assert.NotContains(t, view, "QUEUE")
	}
}

func TestFormatQueueDur(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		d    time.Duration
		want string
	}{
		{name: "zero", d: 0, want: "0s"},
		{name: "seconds", d: 45 * time.Second, want: "45s"},
		{name: "seconds near minute", d: 59 * time.Second, want: "59s"},
		{name: "minutes with seconds", d: 2*time.Minute + 10*time.Second, want: "2m10s"},
		{name: "exact minute", d: time.Minute, want: "1m0s"},
		{name: "hours with minutes", d: time.Hour + 3*time.Minute, want: "1h3m"},
		{name: "exact hour", d: 2 * time.Hour, want: "2h0m"},
		{name: "negative clamped to zero", d: -5 * time.Second, want: "0s"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, formatQueueDur(tt.d))
		})
	}
}

func TestQueueCmd_WrapsResult(t *testing.T) {
	t.Parallel()
	called := false
	cmd := queueCmd(func() error { called = true; return nil })
	msg, ok := cmd().(queueResultMsg)
	require.True(t, ok)
	assert.True(t, called)
	assert.NoError(t, msg.err)

	wantErr := errors.New("boom")
	cmd = queueCmd(func() error { return wantErr })
	msg, ok = cmd().(queueResultMsg)
	require.True(t, ok)
	assert.Equal(t, wantErr, msg.err)
}

func TestQueueScrollOffset(t *testing.T) {
	t.Parallel()
	snap := QueueSnapshot{Budget: 100}
	for i := 0; i < 10; i++ {
		snap.Waiting = append(snap.Waiting, QueueJob{ID: fmt.Sprintf("w%d", i)})
	}
	rows := buildQueueRows(snap)
	require.Len(t, rows, 11)

	tests := []struct {
		name    string
		sel     int
		visible int
		want    int
	}{
		{name: "top selection no scroll", sel: 0, visible: 4, want: 0},
		{name: "still visible no scroll", sel: 2, visible: 4, want: 0},
		{name: "selection pushed down scrolls", sel: 4, visible: 4, want: 2},
		{name: "last selection clamped to max", sel: 9, visible: 4, want: 7},
		{name: "non positive visible", sel: 5, visible: 0, want: 0},
		{name: "all rows visible", sel: 5, visible: 20, want: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, queueScrollOffset(rows, tt.sel, tt.visible))
		})
	}
}

func TestUpdate_QueueNavigationFromStaleSel(t *testing.T) {
	t.Parallel()
	m := queueTestModel(queueTestDeps(queueNavSnapshot(), nil))
	m.showQueue = true
	m.queueSel = 10

	m = pressKey(m, runeKey("k"))
	assert.Equal(t, 2, m.queueSel)
	assertSelectedJob(t, m, "w1", true)

	m = pressKey(m, runeKey("k"))
	assert.Equal(t, 1, m.queueSel)
	assertSelectedJob(t, m, "r2", false)
}

func TestUpdate_QueueNavJFromStaleSelClamps(t *testing.T) {
	t.Parallel()
	m := queueTestModel(queueTestDeps(queueNavSnapshot(), nil))
	m.showQueue = true
	m.queueSel = 10

	m = pressKey(m, runeKey("j"))
	assert.Equal(t, 3, m.queueSel)
	assertSelectedJob(t, m, "w2", true)

	m = pressKey(m, runeKey("j"))
	assert.Equal(t, 3, m.queueSel)
	assertSelectedJob(t, m, "w2", true)
}

func TestViewQueue_SingleSnapshotReadPerFrame(t *testing.T) {
	t.Parallel()
	calls := 0
	snap := queueNavSnapshot()
	deps := ServerDeps{QueueSnapshot: func() QueueSnapshot {
		calls++
		return snap
	}}
	m := queueTestModel(deps)
	m.showQueue = true
	m.queueSel = 2
	m.queueFlash = "error: x"

	_ = m.viewQueue()
	assert.Equal(t, 1, calls)
}
