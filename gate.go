package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"sd-studio-server/api"
	"sd-studio-server/gpuqueue"
	"sd-studio-server/models"
)

const (
	maxSniffBody = 64 * 1024
	mbBytes      = 1024 * 1024
	maxClientTag = 32
)

type GateConfig struct {
	SDDefaultWeightMB int
	SDOverheadMB      int
	LLMWeightMB       int
	MaxWaitSeconds    int
}

type Gate struct {
	q              *gpuqueue.Queue
	cfg            GateConfig
	sdw            *sdWeightTracker
	clampMu        sync.Mutex
	lastClampW     int
	lastClampTotal int
}

func NewGate(q *gpuqueue.Queue, cfg GateConfig, sdw *sdWeightTracker) *Gate {
	return &Gate{q: q, cfg: cfg, sdw: sdw}
}

func (g *Gate) Enabled() bool {
	return g != nil && g.q != nil && g.q.Enabled()
}

func (g *Gate) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !g.Enabled() {
			next.ServeHTTP(w, r)
			return
		}
		g.sdw.sniff(r)
		weight := g.weight(r)
		if weight <= 0 {
			next.ServeHTTP(w, r)
			return
		}
		lease, err := g.q.Acquire(r.Context(), gpuqueue.Spec{
			Kind:      gateKind(r.URL.Path),
			Client:    clientTag(r),
			WeightMB:  weight,
			AutoRenew: true,
		})
		if err != nil {
			if errors.Is(err, gpuqueue.ErrTimeout) {
				w.Header().Set("Retry-After", retryAfterSeconds(g.q))
				api.WriteError(w, gpuqueue.ErrTimeout.Error(), http.StatusServiceUnavailable)
				return
			}
			if errors.Is(err, gpuqueue.ErrCancelled) {
				if r.Context().Err() != nil {
					return
				}
				w.Header().Set("Retry-After", retryAfterSeconds(g.q))
				api.WriteError(w, gpuqueue.ErrTimeout.Error(), http.StatusServiceUnavailable)
				return
			}
			next.ServeHTTP(w, r)
			return
		}
		defer lease.Release()
		next.ServeHTTP(w, r)
	})
}

func (g *Gate) weight(r *http.Request) int {
	p := strings.TrimSuffix(r.URL.Path, "/")
	if strings.HasPrefix(p, "/api/sd/") {
		if r.Method != http.MethodPost {
			return 0
		}
		rest := strings.TrimPrefix(p, "/api/sd/")
		if strings.HasPrefix(rest, "sdapi/v1/") {
			switch {
			case strings.HasSuffix(rest, "/txt2img"),
				strings.HasSuffix(rest, "/img2img"),
				strings.HasSuffix(rest, "/interrogate"):
				_, w := g.sdw.current()
				return g.clampWeight(w)
			}
		}
		return 0
	}
	if strings.HasPrefix(p, "/api/llm/") {
		if r.Method != http.MethodPost {
			return 0
		}
		switch strings.TrimPrefix(p, "/api/llm/") {
		case "api/generate", "api/chat":
			return g.clampWeight(g.cfg.LLMWeightMB)
		}
	}
	return 0
}

func (g *Gate) clampWeight(weight int) int {
	total := g.q.Budget()
	if total <= 0 || weight <= total {
		return weight
	}
	g.noteClamp(weight, total)
	return total
}

func (g *Gate) noteClamp(weight, total int) {
	g.clampMu.Lock()
	defer g.clampMu.Unlock()
	if g.lastClampW == weight && g.lastClampTotal == total {
		return
	}
	g.lastClampW = weight
	g.lastClampTotal = total
	g.q.Warn(fmt.Sprintf("gate weight %dMB exceeds budget %dMB, clamped", weight, total))
}

func retryAfterSeconds(q *gpuqueue.Queue) string {
	return strconv.Itoa(int(q.MaxWait().Seconds()))
}

func gateKind(path string) gpuqueue.Kind {
	if strings.HasPrefix(path, "/api/llm/") {
		return gpuqueue.KindLLM
	}
	return gpuqueue.KindSD
}

func clientTag(r *http.Request) string {
	ua := gpuqueue.SanitizeClient(r.UserAgent())
	if ua == "" {
		return "proxy"
	}
	runes := []rune(ua)
	if len(runes) > maxClientTag {
		runes = runes[:maxClientTag]
	}
	return string(runes)
}

type sdWeightTracker struct {
	mu         sync.Mutex
	cfg        GateConfig
	mm         *models.ModelManager
	modelsDir  string
	optionsURL string
	checkpoint string
	weights    map[string]int
}

func newSDWeight(cfg GateConfig, mm *models.ModelManager, modelsDir, sdTargetURL string) *sdWeightTracker {
	return &sdWeightTracker{
		cfg:        cfg,
		mm:         mm,
		modelsDir:  modelsDir,
		optionsURL: strings.TrimSuffix(sdTargetURL, "/") + "/sdapi/v1/options",
		weights:    make(map[string]int),
	}
}

func (t *sdWeightTracker) setCheckpoint(name string) {
	t.mu.Lock()
	t.checkpoint = cleanCheckpoint(name)
	t.mu.Unlock()
}

func (t *sdWeightTracker) current() (string, int) {
	t.mu.Lock()
	name := t.checkpoint
	t.mu.Unlock()
	if name == "" {
		return "", t.cfg.SDDefaultWeightMB
	}
	return name, t.weightFor(name)
}

func (t *sdWeightTracker) weightFor(name string) int {
	t.mu.Lock()
	if w, ok := t.weights[name]; ok {
		t.mu.Unlock()
		return w
	}
	t.mu.Unlock()

	weight := t.cfg.SDDefaultWeightMB
	if t.mm != nil && t.modelsDir != "" {
		list, err := t.mm.ListModels(t.modelsDir)
		if err == nil {
			for _, m := range list {
				if m.Name == name {
					weight = int((m.Size+mbBytes-1)/mbBytes) + t.cfg.SDOverheadMB
					break
				}
			}
		}
	}

	t.mu.Lock()
	t.weights[name] = weight
	t.mu.Unlock()
	return weight
}

func (t *sdWeightTracker) sniff(r *http.Request) {
	p := strings.TrimSuffix(r.URL.Path, "/")
	if r.Method != http.MethodPost ||
		!strings.HasPrefix(p, "/api/sd/") ||
		!strings.HasSuffix(p, "/sdapi/v1/options") {
		return
	}
	orig := r.Body
	var buf bytes.Buffer
	tmp := make([]byte, 8192)
	for buf.Len() <= maxSniffBody {
		want := maxSniffBody + 1 - buf.Len()
		if want > len(tmp) {
			want = len(tmp)
		}
		n, rerr := orig.Read(tmp[:want])
		if n > 0 {
			buf.Write(tmp[:n])
		}
		if rerr != nil {
			break
		}
	}
	r.Body = &bodyRestore{reader: io.MultiReader(bytes.NewReader(buf.Bytes()), orig), closer: orig}
	if buf.Len() > maxSniffBody {
		return
	}
	var payload struct {
		SDModelCheckpoint string `json:"sd_model_checkpoint"`
	}
	if err := json.Unmarshal(buf.Bytes(), &payload); err != nil {
		return
	}
	if payload.SDModelCheckpoint != "" {
		t.setCheckpoint(payload.SDModelCheckpoint)
	}
}

type bodyRestore struct {
	reader io.Reader
	closer io.Closer
}

func (b *bodyRestore) Read(p []byte) (int, error) {
	return b.reader.Read(p)
}

func (b *bodyRestore) Close() error {
	return b.closer.Close()
}

func (t *sdWeightTracker) refresh(ctx context.Context) {
	if t.optionsURL == "/sdapi/v1/options" {
		return
	}
	reqCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, t.optionsURL, nil)
	if err != nil {
		return
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return
	}
	var payload struct {
		SDModelCheckpoint string `json:"sd_model_checkpoint"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return
	}
	if payload.SDModelCheckpoint != "" {
		t.setCheckpoint(payload.SDModelCheckpoint)
	}
}

func cleanCheckpoint(name string) string {
	if i := strings.Index(name, " ["); i >= 0 {
		name = name[:i]
	}
	return strings.TrimSpace(name)
}
