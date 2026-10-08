package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"sd-studio-server/config"
	"sd-studio-server/gpuqueue"
	"sd-studio-server/models"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestGate(budgetMB, maxWaitSec int, cfg GateConfig, modelsDir string) (*Gate, *gpuqueue.Queue) {
	q := gpuqueue.New(gpuqueue.Config{
		TotalBudgetMB: budgetMB,
		MaxWait:       time.Duration(maxWaitSec) * time.Second,
		LeaseTTL:      30 * time.Second,
	})
	mm := models.NewModelManager(&config.Config{})
	sdw := newSDWeight(cfg, mm, modelsDir, "")
	return NewGate(q, cfg, sdw), q
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

func newGateServer(t *testing.T, gate *Gate, prefixes ...string) *httptest.Server {
	t.Helper()
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	h := gate.Middleware(next)
	mux := http.NewServeMux()
	for _, p := range prefixes {
		mux.Handle(p, h)
	}
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestGate_Enabled(t *testing.T) {
	t.Parallel()
	cfg := GateConfig{SDDefaultWeightMB: 3000}
	sdw := newSDWeight(cfg, nil, "", "")

	assert.True(t, NewGate(gpuqueue.New(gpuqueue.Config{TotalBudgetMB: 100}), cfg, sdw).Enabled())
	assert.False(t, NewGate(gpuqueue.New(gpuqueue.Config{TotalBudgetMB: 0}), cfg, sdw).Enabled())

	var nilGate *Gate
	assert.False(t, nilGate.Enabled())
}

func TestGateKind(t *testing.T) {
	t.Parallel()
	assert.Equal(t, gpuqueue.KindLLM, gateKind("/api/llm/api/chat"))
	assert.Equal(t, gpuqueue.KindLLM, gateKind("/api/llm/api/generate"))
	assert.Equal(t, gpuqueue.KindSD, gateKind("/api/sd/sdapi/v1/txt2img"))
	assert.Equal(t, gpuqueue.KindSD, gateKind("/api/other"))
}

func TestClientTag(t *testing.T) {
	t.Parallel()
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.Header.Set("User-Agent", strings.Repeat("a", 50))
	assert.Len(t, []rune(clientTag(req)), 32)

	req.Header.Del("User-Agent")
	assert.Equal(t, "proxy", clientTag(req))

	req.Header.Set("User-Agent", "csitag")
	assert.Equal(t, "csitag", clientTag(req))

	req.Header.Set("User-Agent", "")
	assert.Equal(t, "proxy", clientTag(req))
}

func TestCleanCheckpoint(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "m.safetensors", cleanCheckpoint("m.safetensors [abc123]"))
	assert.Equal(t, "m.safetensors", cleanCheckpoint("  m.safetensors  "))
	assert.Equal(t, "m.safetensors", cleanCheckpoint("m.safetensors"))
}

func TestGate_WeightClassification(t *testing.T) {
	t.Parallel()
	cfg := GateConfig{SDDefaultWeightMB: 3000, SDOverheadMB: 100, LLMWeightMB: 2000, MaxWaitSeconds: 1}
	gate, _ := newTestGate(5000, 1, cfg, "")

	tests := []struct {
		name   string
		method string
		path   string
		want   int
	}{
		{"txt2img", http.MethodPost, "/api/sd/sdapi/v1/txt2img", 3000},
		{"img2img", http.MethodPost, "/api/sd/sdapi/v1/img2img", 3000},
		{"interrogate", http.MethodPost, "/api/sd/sdapi/v1/interrogate", 3000},
		{"txt2img trailing slash", http.MethodPost, "/api/sd/sdapi/v1/txt2img/", 3000},
		{"img2img trailing slash", http.MethodPost, "/api/sd/sdapi/v1/img2img/", 3000},
		{"llm chat trailing slash", http.MethodPost, "/api/llm/api/chat/", 2000},
		{"txt2img get", http.MethodGet, "/api/sd/sdapi/v1/txt2img", 0},
		{"txt2img get trailing slash", http.MethodGet, "/api/sd/sdapi/v1/txt2img/", 0},
		{"options post", http.MethodPost, "/api/sd/sdapi/v1/options", 0},
		{"progress", http.MethodGet, "/api/sd/sdapi/v1/progress", 0},
		{"llm generate", http.MethodPost, "/api/llm/api/generate", 2000},
		{"llm chat", http.MethodPost, "/api/llm/api/chat", 2000},
		{"llm chat get", http.MethodGet, "/api/llm/api/chat", 0},
		{"llm tags", http.MethodPost, "/api/llm/api/tags", 0},
		{"llm ps", http.MethodGet, "/api/llm/api/ps", 0},
		{"other", http.MethodPost, "/api/other/thing", 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			req := httptest.NewRequest(tt.method, "http://test.local"+tt.path, nil)
			assert.Equal(t, tt.want, gate.weight(req))
		})
	}
}

func TestGate_LightPathsBypassQueue(t *testing.T) {
	t.Parallel()
	cfg := GateConfig{SDDefaultWeightMB: 3000, SDOverheadMB: 100, LLMWeightMB: 2000, MaxWaitSeconds: 1}
	gate, q := newTestGate(5000, 1, cfg, "")

	var hits int32
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.WriteHeader(http.StatusOK)
	})
	h := gate.Middleware(next)
	mux := http.NewServeMux()
	mux.Handle("/api/sd/", h)
	mux.Handle("/api/llm/", h)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	requests := []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/api/sd/sdapi/v1/options"},
		{http.MethodGet, "/api/sd/sdapi/v1/progress"},
		{http.MethodPost, "/api/sd/sdapi/v1/options"},
		{http.MethodGet, "/api/llm/api/tags"},
		{http.MethodPost, "/api/llm/api/tags"},
		{http.MethodGet, "/api/llm/api/ps"},
	}
	for _, r := range requests {
		req, err := http.NewRequest(r.method, srv.URL+r.path, nil)
		require.NoError(t, err)
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err, r.path)
		resp.Body.Close()
		require.Equal(t, http.StatusOK, resp.StatusCode, r.path)
	}

	assert.Equal(t, int32(len(requests)), atomic.LoadInt32(&hits))
	st := q.Status()
	assert.Empty(t, st.Running)
	assert.Empty(t, st.Queue)
}

func TestGate_HeavyPathAcquiresAndReleases(t *testing.T) {
	t.Parallel()
	cfg := GateConfig{SDDefaultWeightMB: 3000, SDOverheadMB: 100, LLMWeightMB: 2000, MaxWaitSeconds: 1}
	gate, q := newTestGate(5000, 1, cfg, "")

	weights := make(chan int, 4)
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		st := q.Status()
		if len(st.Running) == 1 {
			weights <- st.Running[0].WeightMB
		}
		w.WriteHeader(http.StatusOK)
	})
	h := gate.Middleware(next)
	mux := http.NewServeMux()
	mux.Handle("/api/sd/", h)
	mux.Handle("/api/llm/", h)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	tests := []struct {
		name string
		path string
		want int
	}{
		{"txt2img", "/api/sd/sdapi/v1/txt2img", 3000},
		{"img2img", "/api/sd/sdapi/v1/img2img", 3000},
		{"chat", "/api/llm/api/chat", 2000},
		{"generate", "/api/llm/api/generate", 2000},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, err := http.Post(srv.URL+tt.path, "application/json", strings.NewReader("{}"))
			require.NoError(t, err)
			defer resp.Body.Close()
			require.Equal(t, http.StatusOK, resp.StatusCode)

			select {
			case w := <-weights:
				assert.Equal(t, tt.want, w)
			default:
				t.Fatal("expected running lease inside handler")
			}

			require.True(t, waitFor(t, time.Second, func() bool { return len(q.Status().Running) == 0 }))
			assert.Empty(t, q.Status().Queue)
		})
	}
}

func TestGate_TimeoutReturns503WithRetryAfter(t *testing.T) {
	cfg := GateConfig{SDDefaultWeightMB: 3000, SDOverheadMB: 100, LLMWeightMB: 2000, MaxWaitSeconds: 1}
	gate, q := newTestGate(3000, 1, cfg, "")

	hold := make(chan struct{})
	firstDone := make(chan struct{})
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-hold
		w.WriteHeader(http.StatusOK)
	})
	h := gate.Middleware(next)
	mux := http.NewServeMux()
	mux.Handle("/api/sd/", h)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	go func() {
		defer close(firstDone)
		resp, err := http.Post(srv.URL+"/api/sd/sdapi/v1/txt2img", "application/json", strings.NewReader("{}"))
		if err == nil {
			resp.Body.Close()
		}
	}()

	require.True(t, waitFor(t, 2*time.Second, func() bool { return len(q.Status().Running) == 1 }))

	start := time.Now()
	resp, err := http.Post(srv.URL+"/api/sd/sdapi/v1/txt2img", "application/json", strings.NewReader("{}"))
	require.NoError(t, err)
	defer resp.Body.Close()

	require.Equal(t, http.StatusServiceUnavailable, resp.StatusCode)
	assert.Equal(t, "1", resp.Header.Get("Retry-After"))
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.Contains(t, string(body), "gpu queue timeout")
	assert.GreaterOrEqual(t, time.Since(start), 900*time.Millisecond)

	close(hold)
	select {
	case <-firstDone:
	case <-time.After(2 * time.Second):
		t.Fatal("first request did not complete")
	}
	require.True(t, waitFor(t, time.Second, func() bool { return len(q.Status().Running) == 0 }))
}

func TestGate_SniffOptionsPreservesBodyAndUpdatesWeight(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "model.safetensors"), make([]byte, 2*1024*1024), 0o644))

	cfg := GateConfig{SDDefaultWeightMB: 5000, SDOverheadMB: 1000, LLMWeightMB: 2000, MaxWaitSeconds: 1}
	gate, q := newTestGate(100000, 1, cfg, dir)

	type capture struct {
		body   string
		length int64
		weight int
	}
	captures := make(chan capture, 4)
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		c := capture{body: string(b), length: r.ContentLength}
		st := q.Status()
		if len(st.Running) == 1 {
			c.weight = st.Running[0].WeightMB
		}
		captures <- c
		w.WriteHeader(http.StatusOK)
	})
	h := gate.Middleware(next)
	mux := http.NewServeMux()
	mux.Handle("/api/sd/", h)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	name, weight := gate.sdw.current()
	assert.Empty(t, name)
	assert.Equal(t, 5000, weight)

	payload := `{"sd_model_checkpoint":"model.safetensors","CLIP_stop_at_last_layers":2}`
	resp, err := http.Post(srv.URL+"/api/sd/sdapi/v1/options", "application/json", strings.NewReader(payload))
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	resp.Body.Close()

	got := <-captures
	assert.Equal(t, payload, got.body)
	assert.Equal(t, int64(len(payload)), got.length)
	assert.Zero(t, got.weight)

	name, weight = gate.sdw.current()
	assert.Equal(t, "model.safetensors", name)
	assert.Equal(t, 1002, weight)

	resp, err = http.Post(srv.URL+"/api/sd/sdapi/v1/txt2img", "application/json", strings.NewReader("{}"))
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	resp.Body.Close()

	got = <-captures
	assert.Equal(t, 1002, got.weight)

	hashed := `{"sd_model_checkpoint":"model.safetensors [abc123]"}`
	resp, err = http.Post(srv.URL+"/api/sd/sdapi/v1/options", "application/json", strings.NewReader(hashed))
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	resp.Body.Close()

	got = <-captures
	assert.Equal(t, hashed, got.body)

	name, weight = gate.sdw.current()
	assert.Equal(t, "model.safetensors", name)
	assert.Equal(t, 1002, weight)
	assert.Empty(t, q.Status().Running)
}

func TestGate_DisabledQueuePassthrough(t *testing.T) {
	t.Parallel()
	cfg := GateConfig{SDDefaultWeightMB: 3000, SDOverheadMB: 100, LLMWeightMB: 2000, MaxWaitSeconds: 1}
	gate, q := newTestGate(0, 1, cfg, "")
	assert.False(t, gate.Enabled())

	srv := newGateServer(t, gate, "/api/sd/", "/api/llm/")
	for _, path := range []string{"/api/sd/sdapi/v1/txt2img", "/api/llm/api/chat"} {
		resp, err := http.Post(srv.URL+path, "application/json", strings.NewReader("{}"))
		require.NoError(t, err)
		resp.Body.Close()
		require.Equal(t, http.StatusOK, resp.StatusCode)
	}

	st := q.Status()
	assert.False(t, st.Enabled)
	assert.Empty(t, st.Running)
	assert.Empty(t, st.Queue)
}

func TestSDWeightTracker_ModelWeights(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "known.safetensors"), make([]byte, 1024*1024), 0o644))

	cfg := GateConfig{SDDefaultWeightMB: 5000, SDOverheadMB: 1000}
	sdw := newSDWeight(cfg, models.NewModelManager(&config.Config{}), dir, "")

	sdw.setCheckpoint("known.safetensors")
	name, weight := sdw.current()
	assert.Equal(t, "known.safetensors", name)
	assert.Equal(t, 1001, weight)

	sdw.setCheckpoint("missing.safetensors")
	name, weight = sdw.current()
	assert.Equal(t, "missing.safetensors", name)
	assert.Equal(t, 5000, weight)
}

func TestSDWeightTracker_Refresh(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"sd_model_checkpoint":"refreshed.safetensors"}`))
	}))
	defer srv.Close()

	cfg := GateConfig{SDDefaultWeightMB: 5000, SDOverheadMB: 100}
	sdw := newSDWeight(cfg, nil, "", srv.URL)

	name, weight := sdw.current()
	assert.Empty(t, name)
	assert.Equal(t, 5000, weight)

	sdw.refresh(context.Background())

	name, _ = sdw.current()
	assert.Equal(t, "refreshed.safetensors", name)
}

func TestSDWeightTracker_RefreshErrorKeepsState(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	cfg := GateConfig{SDDefaultWeightMB: 5000}
	sdw := newSDWeight(cfg, nil, "", srv.URL)
	sdw.setCheckpoint("kept.safetensors")

	sdw.refresh(context.Background())

	name, weight := sdw.current()
	assert.Equal(t, "kept.safetensors", name)
	assert.Equal(t, 5000, weight)
}

func TestGate_WeightClampedToBudgetWithDebouncedWarning(t *testing.T) {
	t.Parallel()
	cfg := GateConfig{SDDefaultWeightMB: 12000, SDOverheadMB: 4500, LLMWeightMB: 2000, MaxWaitSeconds: 1}
	gate, q := newTestGate(10000, 1, cfg, "")

	req := httptest.NewRequest(http.MethodPost, "http://test.local/api/sd/sdapi/v1/txt2img", nil)
	assert.Equal(t, 10000, gate.weight(req))
	assert.Equal(t, 10000, gate.weight(req))

	llmReq := httptest.NewRequest(http.MethodPost, "http://test.local/api/llm/api/chat", nil)
	assert.Equal(t, 2000, gate.weight(llmReq))

	st := q.Status()
	count := 0
	for _, w := range st.Warnings {
		if strings.Contains(w, "clamped") {
			count++
		}
	}
	assert.Equal(t, 1, count, "clamp warning must fire once per weight value, got %v", st.Warnings)

	smallCfg := GateConfig{SDDefaultWeightMB: 2000, LLMWeightMB: 2000}
	smallGate, smallQ := newTestGate(10000, 1, smallCfg, "")
	smallReq := httptest.NewRequest(http.MethodPost, "http://test.local/api/sd/sdapi/v1/txt2img", nil)
	assert.Equal(t, 2000, smallGate.weight(smallReq))
	assert.Empty(t, smallQ.Status().Warnings)
}

func TestGate_RetryAfterFromQueueMaxWait(t *testing.T) {
	cfg := GateConfig{SDDefaultWeightMB: 3000, SDOverheadMB: 100, LLMWeightMB: 2000, MaxWaitSeconds: 99}
	gate, q := newTestGate(3000, 1, cfg, "")

	hold := make(chan struct{})
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-hold
		w.WriteHeader(http.StatusOK)
	})
	h := gate.Middleware(next)
	mux := http.NewServeMux()
	mux.Handle("/api/sd/", h)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	go func() {
		resp, err := http.Post(srv.URL+"/api/sd/sdapi/v1/txt2img", "application/json", strings.NewReader("{}"))
		if err == nil {
			resp.Body.Close()
		}
	}()

	require.True(t, waitFor(t, 2*time.Second, func() bool { return len(q.Status().Running) == 1 }))

	resp, err := http.Post(srv.URL+"/api/sd/sdapi/v1/txt2img", "application/json", strings.NewReader("{}"))
	require.NoError(t, err)
	defer resp.Body.Close()

	require.Equal(t, http.StatusServiceUnavailable, resp.StatusCode)
	assert.Equal(t, "1", resp.Header.Get("Retry-After"))

	close(hold)
	require.True(t, waitFor(t, time.Second, func() bool { return len(q.Status().Running) == 0 }))
}

func TestGate_CancelledFromQueueReturns503NotEmpty200(t *testing.T) {
	cfg := GateConfig{SDDefaultWeightMB: 3000, SDOverheadMB: 100, LLMWeightMB: 2000, MaxWaitSeconds: 1}
	gate, q := newTestGate(3000, 5, cfg, "")

	hold := make(chan struct{})
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-hold
		w.WriteHeader(http.StatusOK)
	})
	h := gate.Middleware(next)
	mux := http.NewServeMux()
	mux.Handle("/api/sd/", h)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	go func() {
		resp, err := http.Post(srv.URL+"/api/sd/sdapi/v1/txt2img", "application/json", strings.NewReader("{}"))
		if err == nil {
			resp.Body.Close()
		}
	}()
	require.True(t, waitFor(t, 2*time.Second, func() bool { return len(q.Status().Running) == 1 }))

	type result struct {
		resp *http.Response
		err  error
	}
	resCh := make(chan result, 1)
	go func() {
		resp, err := http.Post(srv.URL+"/api/sd/sdapi/v1/txt2img", "application/json", strings.NewReader("{}"))
		resCh <- result{resp: resp, err: err}
	}()

	require.True(t, waitFor(t, 2*time.Second, func() bool { return len(q.Status().Queue) == 1 }))
	queuedID := q.Status().Queue[0].ID
	require.True(t, q.Cancel(queuedID))

	select {
	case res := <-resCh:
		require.NoError(t, res.err)
		defer res.resp.Body.Close()
		require.Equal(t, http.StatusServiceUnavailable, res.resp.StatusCode)
		assert.Equal(t, "5", res.resp.Header.Get("Retry-After"))
		body, err := io.ReadAll(res.resp.Body)
		require.NoError(t, err)
		assert.Contains(t, string(body), "gpu queue timeout")
	case <-time.After(3 * time.Second):
		t.Fatal("cancelled request did not return")
	}

	close(hold)
	require.True(t, waitFor(t, time.Second, func() bool { return len(q.Status().Running) == 0 }))
}

func TestGate_SniffOversizedOptionsPassthrough(t *testing.T) {
	t.Parallel()
	cfg := GateConfig{SDDefaultWeightMB: 5000, SDOverheadMB: 100, MaxWaitSeconds: 1}
	gate, _ := newTestGate(100000, 1, cfg, "")

	type capture struct {
		bodyLen int
		length  int64
	}
	captures := make(chan capture, 1)
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("downstream body read: %v", err)
		}
		captures <- capture{bodyLen: len(b), length: r.ContentLength}
		w.WriteHeader(http.StatusOK)
	})
	h := gate.Middleware(next)
	mux := http.NewServeMux()
	mux.Handle("/api/sd/", h)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	big := `{"sd_model_checkpoint":"evil.safetensors","pad":"` + strings.Repeat("x", 70*1024) + `"}`
	resp, err := http.Post(srv.URL+"/api/sd/sdapi/v1/options", "application/json", strings.NewReader(big))
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	resp.Body.Close()

	got := <-captures
	assert.Equal(t, len(big), got.bodyLen)
	assert.Equal(t, int64(len(big)), got.length)

	name, _ := gate.sdw.current()
	assert.Empty(t, name, "oversized options body must not be parsed")
}
