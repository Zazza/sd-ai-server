package attach

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"sd-studio-server/tui"
)

const statusFixture = `{
  "processes": {
    "sd": {"name": "SD Forge", "status": "running", "pid": 1234, "uptime": "2h5m", "category": "service"},
    "ollama": {"name": "Ollama", "status": "stopped", "pid": 0, "uptime": "", "category": "service"}
  },
  "health": {
    "sd": {"healthy": true, "latency_ms": 42, "error": ""},
    "ollama": {"healthy": false, "latency_ms": 0, "error": "dial refused"}
  },
  "gpu": {"name": "RTX 4080", "memory_total_mb": 16376, "memory_used_mb": 9001, "utilization_percent": 55, "available": true},
  "installs": {"git": {"key": "git", "installed": true, "installing": false, "progress": "done", "error": "", "version": "2.43.0"}},
  "sys": {"cpu_percent": 12.5, "ram_usage": 48.2, "ram_used": 8589934592, "ram_total": 17179869184}
}`

const queueFixture = `{
  "budget": 15884,
  "running": [{"weight_mb": 5000}, {"weight_mb": 4000}],
  "queue": [{"weight_mb": 3000}, {"weight_mb": 2000}, {"weight_mb": 1000}],
  "warnings": ["stale lease", "over commit"]
}`

const logsFixture = `{"logs": ["line1", "line2", "line3"]}`

type recordedRequest struct {
	method string
	path   string
	body   string
}

type fakeDaemon struct {
	mu            sync.Mutex
	statusBody    string
	statusCode    int
	queueBody     string
	queueCode     int
	queueMoveBody string
	queueMoveCode int
	cancelBody    string
	cancelCode    int
	logsBody      string
	logsCode      int
	ctrlBody      string
	ctrlCode      int
	requests      []string
	detailed      []recordedRequest
}

func (f *fakeDaemon) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var reqBody string
	if r.Body != nil {
		b, _ := io.ReadAll(r.Body)
		reqBody = string(b)
	}
	f.mu.Lock()
	f.requests = append(f.requests, r.Method+" "+r.URL.RequestURI())
	f.detailed = append(f.detailed, recordedRequest{method: r.Method, path: r.URL.Path, body: reqBody})
	body, code := "", 0
	switch {
	case r.URL.Path == "/api/server/status":
		body, code = f.statusBody, f.statusCode
	case r.URL.Path == "/api/gpu/status":
		body, code = f.queueBody, f.queueCode
	case strings.HasPrefix(r.URL.Path, "/api/gpu/queue/"):
		body, code = f.queueMoveBody, f.queueMoveCode
	case strings.HasPrefix(r.URL.Path, "/api/gpu/lease/"):
		body, code = f.cancelBody, f.cancelCode
	case strings.HasPrefix(r.URL.Path, "/api/server/logs/"):
		body, code = f.logsBody, f.logsCode
	case strings.HasPrefix(r.URL.Path, "/api/server/start/"),
		strings.HasPrefix(r.URL.Path, "/api/server/stop/"),
		strings.HasPrefix(r.URL.Path, "/api/server/restart/"):
		body, code = f.ctrlBody, f.ctrlCode
	default:
		body, code = `{"error":"not found"}`, http.StatusNotFound
	}
	f.mu.Unlock()

	if code == 0 {
		code = http.StatusOK
	}
	if body == "" {
		body = "{}"
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	fmt.Fprintln(w, body)
}

func (f *fakeDaemon) setStatus(code int, body string) {
	f.mu.Lock()
	f.statusCode, f.statusBody = code, body
	f.mu.Unlock()
}

func (f *fakeDaemon) setQueue(code int, body string) {
	f.mu.Lock()
	f.queueCode, f.queueBody = code, body
	f.mu.Unlock()
}

func (f *fakeDaemon) setQueueMove(code int, body string) {
	f.mu.Lock()
	f.queueMoveCode, f.queueMoveBody = code, body
	f.mu.Unlock()
}

func (f *fakeDaemon) setCancel(code int, body string) {
	f.mu.Lock()
	f.cancelCode, f.cancelBody = code, body
	f.mu.Unlock()
}

func (f *fakeDaemon) setLogs(code int, body string) {
	f.mu.Lock()
	f.logsCode, f.logsBody = code, body
	f.mu.Unlock()
}

func (f *fakeDaemon) setCtrl(code int, body string) {
	f.mu.Lock()
	f.ctrlCode, f.ctrlBody = code, body
	f.mu.Unlock()
}

func (f *fakeDaemon) recorded() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, len(f.requests))
	copy(out, f.requests)
	return out
}

func (f *fakeDaemon) countPath(prefix string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, r := range f.requests {
		if strings.Contains(r, prefix) {
			n++
		}
	}
	return n
}

func (f *fakeDaemon) bodiesFor(method, prefix string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, d := range f.detailed {
		if d.method == method && strings.HasPrefix(d.path, prefix) {
			out = append(out, d.body)
		}
	}
	return out
}

func startDaemon(t *testing.T) (*fakeDaemon, *httptest.Server, *Client) {
	t.Helper()
	f := &fakeDaemon{
		statusBody: statusFixture,
		queueBody:  queueFixture,
		logsBody:   logsFixture,
		ctrlBody:   `{"status": "ok"}`,
	}
	srv := httptest.NewServer(f)
	target, err := ParseTarget(srv.URL)
	require.NoError(t, err)
	return f, srv, NewClient(target)
}

func expectMsg(t *testing.T, ch chan tea.Msg, want tea.Msg, label ...string) {
	t.Helper()
	name := "message"
	if len(label) > 0 {
		name = label[0]
	}
	select {
	case got := <-ch:
		assert.Equal(t, want, got, name)
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for %s", name)
	}
}

func expectNoMsg(t *testing.T, ch chan tea.Msg) {
	t.Helper()
	select {
	case got := <-ch:
		t.Fatalf("unexpected message %#v", got)
	default:
	}
}

func TestClient_Preflight(t *testing.T) {
	t.Parallel()
	f, srv, c := startDaemon(t)
	defer srv.Close()

	require.NoError(t, c.Preflight())

	f.setStatus(http.StatusInternalServerError, "{}")
	err := c.Preflight()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "HTTP 500")
}

func TestClient_Deps_MapsSnapshot(t *testing.T) {
	t.Parallel()
	_, srv, c := startDaemon(t)
	defer srv.Close()

	ctx := context.Background()
	require.NoError(t, c.refreshOnce(ctx))
	c.markOK()

	deps := c.Deps()

	procs := deps.ProcStatus()
	require.Len(t, procs, 2)
	assert.Equal(t, tui.ServiceInfo{
		Name:     "SD Forge",
		Status:   "running",
		PID:      1234,
		Uptime:   "2h5m",
		Category: "service",
	}, procs["sd"])
	assert.Equal(t, tui.ServiceInfo{
		Name:     "Ollama",
		Status:   "stopped",
		Category: "service",
	}, procs["ollama"])

	health := deps.HealthResults()
	require.Len(t, health, 2)
	assert.Equal(t, tui.HealthResult{Healthy: true, LatencyMs: 42}, health["sd"])
	assert.Equal(t, tui.HealthResult{Healthy: false, Error: "dial refused"}, health["ollama"])

	assert.Equal(t, tui.GPUInfo{
		Name:        "RTX 4080",
		MemoryTotal: 16376,
		MemoryUsed:  9001,
		Utilization: 55,
		Available:   true,
	}, deps.GPUInfo())

	installs := deps.InstallStatus()
	require.Len(t, installs, 1)
	assert.Equal(t, tui.ComponentInstallStatus{
		Key:       "git",
		Installed: true,
		Progress:  "done",
		Version:   "2.43.0",
	}, installs["git"])

	assert.Equal(t, tui.SysStats{
		CPUUsage: 12.5,
		RAMUsage: 48.2,
		RAMUsed:  8589934592,
		RAMTotal: 17179869184,
	}, deps.PollStats())
}

func TestClient_GPUQueueArithmetic(t *testing.T) {
	t.Parallel()
	_, srv, c := startDaemon(t)
	defer srv.Close()

	require.NoError(t, c.refreshOnce(context.Background()))

	assert.Equal(t, tui.QueueSnapshot{
		Budget:   15884,
		Running:  []tui.QueueJob{{WeightMB: 5000}, {WeightMB: 4000}},
		Waiting:  []tui.QueueJob{{WeightMB: 3000}, {WeightMB: 2000}, {WeightMB: 1000}},
		Warnings: []string{"stale lease", "over commit"},
	}, c.Deps().QueueSnapshot())
}

func TestClient_QueueFailureKeepsQueue(t *testing.T) {
	t.Parallel()
	f, srv, c := startDaemon(t)
	defer srv.Close()

	ctx := context.Background()
	c.refreshAndMark(ctx)
	before := c.Deps().QueueSnapshot()

	f.setQueue(http.StatusInternalServerError, `{"error":"boom"}`)
	c.refreshAndMark(ctx)

	assert.Equal(t, StateOK, c.stateNow())
	assert.Empty(t, c.Deps().ConnState())
	assert.Equal(t, before, c.Deps().QueueSnapshot())
}

func TestClient_Deps_QueueSnapshotMapsAllFields(t *testing.T) {
	t.Parallel()
	f, srv, c := startDaemon(t)
	defer srv.Close()

	f.setQueue(http.StatusOK, `{
	  "budget": 15884,
	  "running": [{
	    "id": "run-1",
	    "kind": "s\u001b[2Jd",
	    "client": "192.168.1.50:777",
	    "weight_mb": 5000,
	    "priority": 2,
	    "submitted_at": "2026-10-08T10:00:00Z",
	    "lease_deadline": "2026-10-08T10:05:00Z"
	  }],
	  "queue": [{
	    "id": "wait-1",
	    "kind": "llm",
	    "client": "yue",
	    "weight_mb": 3000,
	    "priority": 1,
	    "submitted_at": "2026-10-08T10:01:30Z",
	    "lease_deadline": "0001-01-01T00:00:00Z"
	  }],
	  "warnings": ["w\u001b[2Jarn", "plain"]
	}`)
	require.NoError(t, c.refreshOnce(context.Background()))

	snap := c.Deps().QueueSnapshot()
	assert.Equal(t, 15884, snap.Budget)
	require.Len(t, snap.Running, 1)
	assert.Equal(t, tui.QueueJob{
		ID:            "run-1",
		Kind:          "s[2Jd",
		Client:        "192.168.1.50:777",
		WeightMB:      5000,
		Priority:      2,
		SubmittedAt:   time.Date(2026, 10, 8, 10, 0, 0, 0, time.UTC),
		LeaseDeadline: time.Date(2026, 10, 8, 10, 5, 0, 0, time.UTC),
	}, snap.Running[0])
	require.Len(t, snap.Waiting, 1)
	assert.Equal(t, tui.QueueJob{
		ID:          "wait-1",
		Kind:        "llm",
		Client:      "yue",
		WeightMB:    3000,
		Priority:    1,
		SubmittedAt: time.Date(2026, 10, 8, 10, 1, 30, 0, time.UTC),
	}, snap.Waiting[0])
	assert.True(t, snap.Waiting[0].LeaseDeadline.IsZero())
	assert.Equal(t, []string{"w[2Jarn", "plain"}, snap.Warnings)
	for _, s := range []string{snap.Running[0].Kind, snap.Warnings[0]} {
		assert.NotContains(t, s, "\x1b")
	}
}

func TestClient_QueueMove_SendsPatchAndRefreshes(t *testing.T) {
	t.Parallel()
	f, srv, c := startDaemon(t)
	defer srv.Close()

	msgs := make(chan tea.Msg, 8)
	c.Notify(func(m tea.Msg) { msgs <- m })
	c.refreshAndMark(context.Background())
	expectMsg(t, msgs, tui.ServicesChangeMsg{})

	deps := c.Deps()
	statusGets := f.countPath("GET /api/gpu/status")

	require.NoError(t, deps.QueueMove("job-1", -1))
	assert.Contains(t, f.recorded(), "PATCH /api/gpu/queue/job-1")
	assert.Equal(t, []string{`{"move":"up"}`}, f.bodiesFor("PATCH", "/api/gpu/queue/"))
	assert.Equal(t, statusGets+1, f.countPath("GET /api/gpu/status"))
	expectMsg(t, msgs, tui.ServicesChangeMsg{})

	require.NoError(t, deps.QueueMove("job-2", 1))
	assert.Contains(t, f.recorded(), "PATCH /api/gpu/queue/job-2")
	assert.Equal(t, []string{`{"move":"up"}`, `{"move":"down"}`}, f.bodiesFor("PATCH", "/api/gpu/queue/"))
	assert.Equal(t, statusGets+2, f.countPath("GET /api/gpu/status"))
	expectMsg(t, msgs, tui.ServicesChangeMsg{})
	expectNoMsg(t, msgs)
}

func TestClient_QueueCancel_SendsDeleteLeaseAndRefreshes(t *testing.T) {
	t.Parallel()
	f, srv, c := startDaemon(t)
	defer srv.Close()

	msgs := make(chan tea.Msg, 8)
	c.Notify(func(m tea.Msg) { msgs <- m })
	c.refreshAndMark(context.Background())
	expectMsg(t, msgs, tui.ServicesChangeMsg{})

	deps := c.Deps()
	statusGets := f.countPath("GET /api/gpu/status")

	require.NoError(t, deps.QueueCancel("lease-9"))
	assert.Contains(t, f.recorded(), "DELETE /api/gpu/lease/lease-9")
	deleteBodies := f.bodiesFor("DELETE", "/api/gpu/lease/")
	require.Len(t, deleteBodies, 1)
	assert.Empty(t, deleteBodies[0])
	assert.Equal(t, statusGets+1, f.countPath("GET /api/gpu/status"))
	expectMsg(t, msgs, tui.ServicesChangeMsg{})
	expectNoMsg(t, msgs)
}

func TestClient_QueueMoveServerError(t *testing.T) {
	t.Parallel()
	f, srv, c := startDaemon(t)
	defer srv.Close()

	c.refreshAndMark(context.Background())
	statusGets := f.countPath("GET /api/gpu/status")
	deps := c.Deps()

	tests := []struct {
		name       string
		code       int
		body       string
		wantErrSeg string
	}{
		{name: "json error body", code: http.StatusNotFound, body: `{"error":"lease not found"}`, wantErrSeg: "lease not found"},
		{name: "plain body", code: http.StatusBadGateway, body: "gateway is down", wantErrSeg: "HTTP 502"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f.setQueueMove(tt.code, tt.body)
			err := deps.QueueMove("gone", -1)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "queue move gone")
			assert.Contains(t, err.Error(), tt.wantErrSeg)
		})
	}
	assert.Equal(t, statusGets, f.countPath("GET /api/gpu/status"))
}

func TestClient_QueueCancelServerError(t *testing.T) {
	t.Parallel()
	f, srv, c := startDaemon(t)
	defer srv.Close()

	c.refreshAndMark(context.Background())
	statusGets := f.countPath("GET /api/gpu/status")
	deps := c.Deps()

	tests := []struct {
		name       string
		code       int
		body       string
		wantErrSeg string
	}{
		{name: "json error body", code: http.StatusNotFound, body: `{"error":"lease not found"}`, wantErrSeg: "lease not found"},
		{name: "plain body", code: http.StatusBadGateway, body: "gateway is down", wantErrSeg: "HTTP 502"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f.setCancel(tt.code, tt.body)
			err := deps.QueueCancel("gone")
			require.Error(t, err)
			assert.Contains(t, err.Error(), "queue cancel gone")
			assert.Contains(t, err.Error(), tt.wantErrSeg)
		})
	}
	assert.Equal(t, statusGets, f.countPath("GET /api/gpu/status"))
}

func TestClient_ControlSuccess(t *testing.T) {
	t.Parallel()
	f, srv, c := startDaemon(t)
	defer srv.Close()

	msgs := make(chan tea.Msg, 8)
	c.Notify(func(m tea.Msg) { msgs <- m })

	c.refreshAndMark(context.Background())
	expectMsg(t, msgs, tui.ServicesChangeMsg{})

	deps := c.Deps()
	actions := []struct {
		seg string
		run func(name string) error
	}{
		{"start", deps.StartProc},
		{"stop", deps.StopProc},
		{"restart", deps.RestartProc},
	}
	for _, a := range actions {
		require.NoError(t, a.run("forge ui"), a.seg)
		assert.Contains(t, f.recorded(), "POST /api/server/"+a.seg+"/forge%20ui", a.seg)
		expectMsg(t, msgs, tui.ServicesChangeMsg{}, a.seg)
	}

	require.NoError(t, deps.StopProc("sd"))
	expectMsg(t, msgs, tui.ServicesChangeMsg{})
}

func TestClient_ControlServerError(t *testing.T) {
	t.Parallel()
	f, srv, c := startDaemon(t)
	defer srv.Close()

	f.setCtrl(http.StatusInternalServerError, `{"error":"boom"}`)
	err := c.Deps().RestartProc("sd")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "restart sd")
	assert.Contains(t, err.Error(), "boom")

	f.setCtrl(http.StatusBadGateway, "gateway is down")
	err = c.Deps().StopProc("sd")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "HTTP 502")
}

func TestClient_LostFreezesSnapshot(t *testing.T) {
	t.Parallel()
	_, srv, c := startDaemon(t)

	ctx := context.Background()
	c.refreshAndMark(ctx)
	require.Equal(t, StateOK, c.stateNow())

	srv.Close()
	require.Error(t, c.refreshOnce(ctx))
	c.refreshAndMark(ctx)
	require.Equal(t, StateLost, c.stateNow())

	banner := c.Deps().ConnState()
	require.NotEmpty(t, banner)
	assert.Contains(t, banner, "connection lost")
	assert.Contains(t, banner, "attempt 1")

	deps := c.Deps()
	assert.Len(t, deps.ProcStatus(), 2)
	assert.Equal(t, true, deps.HealthResults()["sd"].Healthy)
	assert.Equal(t, "RTX 4080", deps.GPUInfo().Name)
	used := 0
	for _, j := range deps.QueueSnapshot().Running {
		used += j.WeightMB
	}
	assert.Equal(t, 9000, used)
	assert.Equal(t, 12.5, deps.PollStats().CPUUsage)
}

func TestClient_ProcLogs(t *testing.T) {
	t.Parallel()
	f, srv, c := startDaemon(t)
	defer srv.Close()

	assert.Nil(t, c.ProcLogs("sd", 200))
	assert.Zero(t, f.countPath("/api/server/logs/"))

	c.refreshAndMark(context.Background())

	logs := c.ProcLogs("sd", 200)
	assert.Equal(t, []string{"line1", "line2", "line3"}, logs)
	assert.Equal(t, 1, f.countPath("/api/server/logs/"))
	assert.Contains(t, f.recorded(), "GET /api/server/logs/sd?lines=200")

	f.setLogs(http.StatusNotFound, `{"error":"no logs"}`)
	logs = c.ProcLogs("sd", 200)
	assert.Equal(t, []string{"line1", "line2", "line3"}, logs)
	assert.Equal(t, 2, f.countPath("/api/server/logs/"))

	f.setStatus(http.StatusInternalServerError, "{}")
	c.refreshAndMark(context.Background())
	require.Equal(t, StateLost, c.stateNow())

	f.setLogs(http.StatusOK, logsFixture)
	logs = c.ProcLogs("sd", 200)
	assert.Equal(t, []string{"line1", "line2", "line3"}, logs)
	assert.Equal(t, 2, f.countPath("/api/server/logs/"))
}

func TestNextDelay_Backoff(t *testing.T) {
	t.Parallel()
	tests := []struct {
		fails int
		want  time.Duration
	}{
		{0, 3 * time.Second},
		{1, 6 * time.Second},
		{2, 12 * time.Second},
		{3, 24 * time.Second},
		{4, 30 * time.Second},
		{5, 30 * time.Second},
		{20, 30 * time.Second},
	}
	for _, tt := range tests {
		assert.Equal(t, tt.want, nextDelay(tt.fails), "fails=%d", tt.fails)
	}
}

func TestClient_PreRunDepsEmptySnapshot(t *testing.T) {
	t.Parallel()
	_, srv, c := startDaemon(t)
	defer srv.Close()

	deps := c.Deps()
	assert.Empty(t, deps.ProcStatus())
	assert.Empty(t, deps.HealthResults())
	assert.Empty(t, deps.InstallStatus())
	assert.Equal(t, tui.QueueSnapshot{}, deps.QueueSnapshot())
	assert.Equal(t, tui.SysStats{}, deps.PollStats())

	conn := deps.ConnState()
	require.NotEmpty(t, conn)
	assert.Contains(t, conn, "connecting")
	assert.Contains(t, conn, srv.URL)
}

func TestClient_NotifyOnStateTransition(t *testing.T) {
	t.Parallel()
	f, srv, c := startDaemon(t)
	defer srv.Close()

	msgs := make(chan tea.Msg, 8)
	c.Notify(func(m tea.Msg) { msgs <- m })

	ctx := context.Background()
	c.refreshAndMark(ctx)
	expectMsg(t, msgs, tui.ServicesChangeMsg{})

	c.refreshAndMark(ctx)
	expectNoMsg(t, msgs)

	f.setStatus(http.StatusInternalServerError, "{}")
	c.refreshAndMark(ctx)
	expectMsg(t, msgs, tui.ServicesChangeMsg{})

	c.refreshAndMark(ctx)
	expectNoMsg(t, msgs)
}

func TestClient_Run(t *testing.T) {
	_, srv, c := startDaemon(t)
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		c.Run(ctx)
		close(done)
	}()

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && c.stateNow() != StateOK {
		time.Sleep(5 * time.Millisecond)
	}
	require.Equal(t, StateOK, c.stateNow())
	assert.Len(t, c.Deps().ProcStatus(), 2)

	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not stop after cancel")
	}
}

func TestClient_SanitizesDaemonStrings(t *testing.T) {
	t.Parallel()
	f, srv, c := startDaemon(t)
	defer srv.Close()

	f.setStatus(http.StatusOK, `{
	  "processes": {"sd": {"name": "p\u001b]52;c;boom\u0007x", "status": "run\u001b[2Jning", "uptime": "1m", "category": "service"}},
	  "health": {"sd": {"healthy": false, "error": "err\u001b[9Xor"}},
	  "gpu": {"name": "g\u001b[2Jpu", "memory_total_mb": 100, "available": true},
	  "installs": {"git": {"key": "git", "installed": true, "progress": "done", "version": "2.43\u001bM"}}
	}`)
	require.NoError(t, c.refreshOnce(context.Background()))

	deps := c.Deps()
	for _, s := range []string{
		deps.ProcStatus()["sd"].Name,
		deps.ProcStatus()["sd"].Status,
		deps.HealthResults()["sd"].Error,
		deps.GPUInfo().Name,
		deps.InstallStatus()["git"].Version,
	} {
		assert.NotContains(t, s, "\x1b")
		assert.NotContains(t, s, "\x07")
	}

	f.setLogs(http.StatusOK, `{"logs": ["l1\u001b]52;c;boom\u0007", "l2\u001b[2J", "l3\u0007plain"]}`)
	c.markOK()
	logs := c.ProcLogs("sd", 200)
	require.Len(t, logs, 3)
	for _, line := range logs {
		assert.NotContains(t, line, "\x1b")
		assert.NotContains(t, line, "\x07")
	}
	assert.Equal(t, "l3plain", logs[2])
}

func TestSanitizeString_NewlinesTabsBecomeSpaces(t *testing.T) {
	t.Parallel()
	tests := []struct {
		in   string
		want string
	}{
		{"a\nb", "a b"},
		{"a\rb", "a b"},
		{"a\tb", "a b"},
		{"line1\nline2\tend", "line1 line2 end"},
		{"plain", "plain"},
		{"esc\u001b[2J", "esc[2J"},
		{"bel\u0007", "bel"},
		{"del\u007f", "del"},
	}
	for _, tt := range tests {
		assert.Equal(t, tt.want, sanitizeString(tt.in), tt.in)
	}
}

func TestSanitizeString_C1ControlsStripped(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "nelx", sanitizeString("nel\u0085x"))
	assert.Equal(t, "csix", sanitizeString("csi\u009bx"))
	assert.Equal(t, "ok\u00a0x", sanitizeString("ok\u00a0x"))
}

func TestClient_BodySizeLimit(t *testing.T) {
	t.Parallel()
	f, srv, c := startDaemon(t)
	defer srv.Close()

	f.setStatus(http.StatusOK, `{"gpu":{"name":"`+strings.Repeat("A", 3<<20)+`"}}`)
	err := c.refreshOnce(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "decode")

	err = c.Preflight()
	require.Error(t, err)

	f.setLogs(http.StatusOK, `{"logs": ["`+strings.Repeat("B", 3<<20)+`"]}`)
	c.markOK()
	assert.Nil(t, c.ProcLogs("sd", 200))
}
