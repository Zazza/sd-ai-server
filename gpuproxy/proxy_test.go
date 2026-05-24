package gpuproxy

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestProxy(t *testing.T, ollamaBackend, sdBackend *httptest.Server) *Proxy {
	t.Helper()

	cfg := Config{
		Enabled:  true,
		GPUSlots: 1,
		Endpoints: map[string]EndpointConfig{
			"ollama": {
				ListenAddr: "127.0.0.1:0",
				TargetURL:  ollamaBackend.URL,
			},
			"sd": {
				ListenAddr: "127.0.0.1:0",
				TargetURL:  sdBackend.URL,
			},
		},
		StudioHeader: "X-SD-Studio",
	}

	return New(cfg, nil)
}

func TestProxy_ForwardRequest(t *testing.T) {
	var gotPath string
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	}))
	defer backend.Close()

	sdBackend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer sdBackend.Close()

	p := newTestProxy(t, backend, sdBackend)
	require.NoError(t, p.Start())
	defer p.Stop()

	resp, err := http.Get("http://" + p.Addr("ollama") + "/api/generate")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "/api/generate", gotPath)
}

func TestProxy_OneAtATime(t *testing.T) {
	var active int64
	var maxActive int64
	var mu sync.Mutex

	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		current := atomic.AddInt64(&active, 1)
		mu.Lock()
		if current > maxActive {
			maxActive = current
		}
		mu.Unlock()

		time.Sleep(100 * time.Millisecond)

		atomic.AddInt64(&active, -1)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("done"))
	}))
	defer backend.Close()

	sdBackend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		current := atomic.AddInt64(&active, 1)
		mu.Lock()
		if current > maxActive {
			maxActive = current
		}
		mu.Unlock()

		time.Sleep(100 * time.Millisecond)

		atomic.AddInt64(&active, -1)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("done"))
	}))
	defer sdBackend.Close()

	p := newTestProxy(t, backend, sdBackend)
	require.NoError(t, p.Start())
	defer p.Stop()

	addr := p.Addr("ollama")

	var wg sync.WaitGroup
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			resp, err := http.Get("http://" + addr + "/api/generate")
			if err == nil {
				resp.Body.Close()
			}
		}()
	}
	wg.Wait()

	mu.Lock()
	assert.Equal(t, int64(1), maxActive, "only 1 request should run at a time")
	mu.Unlock()
}

func TestProxy_HighPriorityFirst(t *testing.T) {
	var order []string
	var orderMu sync.Mutex
	blockerDone := make(chan struct{})

	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-Req-Id")
		orderMu.Lock()
		order = append(order, id)
		orderMu.Unlock()

		if id == "blocker" {
			<-blockerDone
		}
		time.Sleep(20 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	}))
	defer backend.Close()

	sdBackend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer sdBackend.Close()

	p := newTestProxy(t, backend, sdBackend)
	require.NoError(t, p.Start())
	defer p.Stop()

	addr := p.Addr("ollama")

	go func() {
		req, _ := http.NewRequest("GET", "http://"+addr+"/api/generate", nil)
		req.Header.Set("X-Req-Id", "blocker")
		resp, err := http.DefaultClient.Do(req)
		if err == nil {
			resp.Body.Close()
		}
	}()
	time.Sleep(50 * time.Millisecond)

	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		req, _ := http.NewRequest("GET", "http://"+addr+"/api/generate", nil)
		req.Header.Set("X-SD-Studio", "1")
		req.Header.Set("X-Req-Id", "low")
		resp, err := http.DefaultClient.Do(req)
		if err == nil {
			resp.Body.Close()
		}
	}()
	time.Sleep(20 * time.Millisecond)

	go func() {
		defer wg.Done()
		req, _ := http.NewRequest("GET", "http://"+addr+"/api/generate", nil)
		req.Header.Set("X-Req-Id", "high")
		resp, err := http.DefaultClient.Do(req)
		if err == nil {
			resp.Body.Close()
		}
	}()
	time.Sleep(20 * time.Millisecond)

	close(blockerDone)
	wg.Wait()

	orderMu.Lock()
	require.Len(t, order, 3)
	assert.Equal(t, "blocker", order[0])
	assert.Equal(t, "high", order[1], "high priority should run before low")
	assert.Equal(t, "low", order[2])
	orderMu.Unlock()
}

func TestProxy_NonPreemptive(t *testing.T) {
	completed := make(chan string, 2)
	lowStarted := make(chan struct{})

	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-Req-Id")
		if id == "low-running" {
			close(lowStarted)
		}
		time.Sleep(150 * time.Millisecond)
		completed <- id
		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()

	sdBackend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer sdBackend.Close()

	p := newTestProxy(t, backend, sdBackend)
	require.NoError(t, p.Start())
	defer p.Stop()

	addr := p.Addr("ollama")

	go func() {
		req, _ := http.NewRequest("GET", "http://"+addr+"/api/generate", nil)
		req.Header.Set("X-SD-Studio", "1")
		req.Header.Set("X-Req-Id", "low-running")
		resp, err := http.DefaultClient.Do(req)
		if err == nil {
			resp.Body.Close()
		}
	}()

	<-lowStarted
	time.Sleep(20 * time.Millisecond)

	go func() {
		req, _ := http.NewRequest("GET", "http://"+addr+"/api/generate", nil)
		req.Header.Set("X-Req-Id", "high")
		resp, err := http.DefaultClient.Do(req)
		if err == nil {
			resp.Body.Close()
		}
	}()

	first := <-completed
	assert.Equal(t, "low-running", first, "current request should complete before high priority runs")
}

func TestProxy_StudioHeaderStripped(t *testing.T) {
	var gotHeader string
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeader = r.Header.Get("X-SD-Studio")
		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()

	sdBackend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer sdBackend.Close()

	p := newTestProxy(t, backend, sdBackend)
	require.NoError(t, p.Start())
	defer p.Stop()

	req, _ := http.NewRequest("GET", "http://"+p.Addr("ollama")+"/api/generate", nil)
	req.Header.Set("X-SD-Studio", "1")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	resp.Body.Close()

	assert.Empty(t, gotHeader, "X-SD-Studio header should be stripped before forwarding")
}

func TestProxy_Stats(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()

	sdBackend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer sdBackend.Close()

	p := newTestProxy(t, backend, sdBackend)
	require.NoError(t, p.Start())
	defer p.Stop()

	stats := p.Stats()
	assert.Equal(t, 1, stats.Queue.TotalSlots)
	assert.Len(t, stats.Servers, 2)
}

func TestProxy_StartStop(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()

	sdBackend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer sdBackend.Close()

	p := newTestProxy(t, backend, sdBackend)
	require.NoError(t, p.Start())
	p.Stop()

	done := make(chan struct{})
	go func() {
		p.Stop()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Stop() hung")
	}
}

func TestProxy_MultipleEndpoints(t *testing.T) {
	llmCalled := atomic.Bool{}
	sdCalled := atomic.Bool{}

	ollamaBackend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		llmCalled.Store(true)
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "ollama-ok")
	}))
	defer ollamaBackend.Close()

	sdBackend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sdCalled.Store(true)
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "sd-ok")
	}))
	defer sdBackend.Close()

	p := newTestProxy(t, ollamaBackend, sdBackend)
	require.NoError(t, p.Start())
	defer p.Stop()

	resp, err := http.Get("http://" + p.Addr("ollama") + "/api/generate")
	require.NoError(t, err)
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	assert.Equal(t, "ollama-ok", string(body))

	resp, err = http.Get("http://" + p.Addr("sd") + "/sdapi/v1/txt2img")
	require.NoError(t, err)
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	assert.Equal(t, "sd-ok", string(body))

	assert.True(t, llmCalled.Load())
	assert.True(t, sdCalled.Load())
}

func TestProxy_LLMAndSDShareGPUSlot(t *testing.T) {
	var active int64
	var maxActive int64
	var mu sync.Mutex

	ollamaBackend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		current := atomic.AddInt64(&active, 1)
		mu.Lock()
		if current > maxActive {
			maxActive = current
		}
		mu.Unlock()
		time.Sleep(100 * time.Millisecond)
		atomic.AddInt64(&active, -1)
		w.WriteHeader(http.StatusOK)
	}))
	defer ollamaBackend.Close()

	sdBackend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		current := atomic.AddInt64(&active, 1)
		mu.Lock()
		if current > maxActive {
			maxActive = current
		}
		mu.Unlock()
		time.Sleep(100 * time.Millisecond)
		atomic.AddInt64(&active, -1)
		w.WriteHeader(http.StatusOK)
	}))
	defer sdBackend.Close()

	p := newTestProxy(t, ollamaBackend, sdBackend)
	require.NoError(t, p.Start())
	defer p.Stop()

	llmAddr := p.Addr("ollama")
	sdAddr := p.Addr("sd")

	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		resp, _ := http.Get("http://" + llmAddr + "/api/generate")
		if resp != nil {
			resp.Body.Close()
		}
	}()

	time.Sleep(20 * time.Millisecond)

	go func() {
		defer wg.Done()
		resp, _ := http.Get("http://" + sdAddr + "/sdapi/v1/txt2img")
		if resp != nil {
			resp.Body.Close()
		}
	}()

	wg.Wait()

	mu.Lock()
	assert.Equal(t, int64(1), maxActive, "LLM and SD requests must not run simultaneously")
	mu.Unlock()
}
