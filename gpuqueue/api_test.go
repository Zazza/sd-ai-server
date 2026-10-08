package gpuqueue

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestAPI(budgetMB int, maxWait time.Duration) (*API, *Queue) {
	q := New(Config{TotalBudgetMB: budgetMB, MaxWait: maxWait, LeaseTTL: 30 * time.Second})
	return NewAPI(q), q
}

func perform(t *testing.T, mux *http.ServeMux, method, target string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var buf strings.Builder
	if body != nil {
		require.NoError(t, json.NewEncoder(&buf).Encode(body))
	}
	req := httptest.NewRequest(method, target, strings.NewReader(buf.String()))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func performRaw(t *testing.T, mux *http.ServeMux, method, target, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func TestAPI_LeaseAcquired(t *testing.T) {
	t.Parallel()
	a, q := newTestAPI(10000, 5*time.Second)
	mux := http.NewServeMux()
	a.RegisterRoutes(mux)

	rec := perform(t, mux, http.MethodPost, "/api/gpu/lease", map[string]any{
		"kind": "sd", "client": "tester", "weight_mb": 4000,
	})
	require.Equal(t, http.StatusOK, rec.Code)

	var resp leaseResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	assert.True(t, resp.Acquired)
	assert.NotEmpty(t, resp.ID)
	assert.Equal(t, 30, resp.TTLSeconds)
	require.Len(t, q.Status().Running, 1)
}

func TestAPI_LeaseQueued(t *testing.T) {
	t.Parallel()
	a, q := newTestAPI(5000, 5*time.Second)
	mux := http.NewServeMux()
	a.RegisterRoutes(mux)

	rec := perform(t, mux, http.MethodPost, "/api/gpu/lease", map[string]any{"kind": "sd", "weight_mb": 5000})
	require.Equal(t, http.StatusOK, rec.Code)

	rec = perform(t, mux, http.MethodPost, "/api/gpu/lease", map[string]any{"kind": "llm", "weight_mb": 4000})
	require.Equal(t, http.StatusAccepted, rec.Code)

	var resp leaseResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	assert.False(t, resp.Acquired)
	assert.NotEmpty(t, resp.ID)
	assert.Equal(t, 1, resp.Position)

	require.Len(t, q.Status().Queue, 1)
	assert.Equal(t, resp.ID, q.Status().Queue[0].ID)
}

func TestAPI_LeaseValidation(t *testing.T) {
	t.Parallel()
	a, _ := newTestAPI(5000, 5*time.Second)
	mux := http.NewServeMux()
	a.RegisterRoutes(mux)

	tests := []struct {
		name string
		body map[string]any
	}{
		{"invalid kind", map[string]any{"kind": "foo", "weight_mb": 100}},
		{"empty kind", map[string]any{"kind": "", "weight_mb": 100}},
		{"zero weight", map[string]any{"kind": "sd", "weight_mb": 0}},
		{"negative weight", map[string]any{"kind": "sd", "weight_mb": -10}},
		{"weight over budget", map[string]any{"kind": "sd", "weight_mb": 5001}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			rec := perform(t, mux, http.MethodPost, "/api/gpu/lease", tt.body)
			assert.Equal(t, http.StatusBadRequest, rec.Code)
			assert.Contains(t, rec.Body.String(), "error")
		})
	}
}

func TestAPI_LeaseInvalidJSON(t *testing.T) {
	t.Parallel()
	a, _ := newTestAPI(5000, 5*time.Second)
	mux := http.NewServeMux()
	a.RegisterRoutes(mux)

	rec := performRaw(t, mux, http.MethodPost, "/api/gpu/lease", "{not-json")
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestAPI_LeaseDisabledQueue(t *testing.T) {
	t.Parallel()
	a, q := newTestAPI(0, 5*time.Second)
	mux := http.NewServeMux()
	a.RegisterRoutes(mux)

	rec := perform(t, mux, http.MethodPost, "/api/gpu/lease", map[string]any{"kind": "sd", "weight_mb": 999999})
	require.Equal(t, http.StatusOK, rec.Code)

	var resp leaseResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	assert.True(t, resp.Acquired)
	assert.Empty(t, resp.ID)
	assert.False(t, q.Enabled())
}

func TestAPI_Heartbeat(t *testing.T) {
	t.Parallel()
	a, q := newTestAPI(5000, 5*time.Second)
	mux := http.NewServeMux()
	a.RegisterRoutes(mux)

	rec := perform(t, mux, http.MethodPost, "/api/gpu/lease", map[string]any{"kind": "sd", "weight_mb": 3000})
	require.Equal(t, http.StatusOK, rec.Code)
	var created leaseResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&created))

	before, ok := q.Get(created.ID)
	require.True(t, ok)

	rec = perform(t, mux, http.MethodPost, "/api/gpu/lease/"+created.ID+"/heartbeat", nil)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "ok")

	after, ok := q.Get(created.ID)
	require.True(t, ok)
	assert.True(t, after.LeaseDeadline.After(before.LeaseDeadline))

	rec = perform(t, mux, http.MethodPost, "/api/gpu/lease/sd-777/heartbeat", nil)
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestAPI_LeaseDelete(t *testing.T) {
	t.Parallel()
	a, q := newTestAPI(5000, 5*time.Second)
	mux := http.NewServeMux()
	a.RegisterRoutes(mux)

	rec := perform(t, mux, http.MethodPost, "/api/gpu/lease", map[string]any{"kind": "sd", "weight_mb": 5000})
	require.Equal(t, http.StatusOK, rec.Code)

	rec = perform(t, mux, http.MethodPost, "/api/gpu/lease", map[string]any{"kind": "llm", "weight_mb": 4000})
	require.Equal(t, http.StatusAccepted, rec.Code)
	var queued leaseResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&queued))

	rec = perform(t, mux, http.MethodDelete, "/api/gpu/lease/"+queued.ID, nil)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "cancelled")
	assert.Empty(t, q.Status().Queue)

	rec = perform(t, mux, http.MethodDelete, "/api/gpu/lease/llm-777", nil)
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestAPI_QueueMove(t *testing.T) {
	t.Parallel()
	a, q := newTestAPI(1000, 5*time.Second)
	mux := http.NewServeMux()
	a.RegisterRoutes(mux)

	rec := perform(t, mux, http.MethodPost, "/api/gpu/lease", map[string]any{"kind": "sd", "weight_mb": 1000})
	require.Equal(t, http.StatusOK, rec.Code)

	rec = perform(t, mux, http.MethodPost, "/api/gpu/lease", map[string]any{"kind": "sd", "weight_mb": 1000})
	require.Equal(t, http.StatusAccepted, rec.Code)
	var aID leaseResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&aID))

	rec = perform(t, mux, http.MethodPost, "/api/gpu/lease", map[string]any{"kind": "llm", "weight_mb": 1000})
	require.Equal(t, http.StatusAccepted, rec.Code)
	var bID leaseResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&bID))

	rec = perform(t, mux, http.MethodPatch, "/api/gpu/queue/"+bID.ID, map[string]string{"move": "up"})
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, bID.ID, q.Status().Queue[0].ID)

	rec = perform(t, mux, http.MethodPatch, "/api/gpu/queue/"+aID.ID, map[string]string{"move": "down"})
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, aID.ID, q.Status().Queue[len(q.Status().Queue)-1].ID)

	rec = perform(t, mux, http.MethodPatch, "/api/gpu/queue/llm-999", map[string]string{"move": "up"})
	assert.Equal(t, http.StatusNotFound, rec.Code)

	rec = perform(t, mux, http.MethodPatch, "/api/gpu/queue/"+aID.ID, map[string]string{"move": "sideways"})
	assert.Equal(t, http.StatusBadRequest, rec.Code)

	rec = performRaw(t, mux, http.MethodPatch, "/api/gpu/queue/"+aID.ID, "{bad")
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestAPI_Status(t *testing.T) {
	t.Parallel()
	a, _ := newTestAPI(8000, 5*time.Second)
	mux := http.NewServeMux()
	a.RegisterRoutes(mux)

	rec := perform(t, mux, http.MethodGet, "/api/gpu/status", nil)
	require.Equal(t, http.StatusOK, rec.Code)

	var raw map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &raw))
	for _, key := range []string{"enabled", "budget", "running", "queue", "warnings"} {
		require.Contains(t, raw, key)
	}

	var st Status
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &st))
	assert.True(t, st.Enabled)
	assert.Equal(t, 8000, st.Budget)
	assert.Empty(t, st.Running)
	assert.Empty(t, st.Queue)
	assert.Empty(t, st.Warnings)
}

func TestAPI_LeaseWaitTimeout(t *testing.T) {
	a, _ := newTestAPI(5000, 2*time.Second)
	mux := http.NewServeMux()
	a.RegisterRoutes(mux)

	rec := perform(t, mux, http.MethodPost, "/api/gpu/lease", map[string]any{"kind": "sd", "weight_mb": 5000})
	require.Equal(t, http.StatusOK, rec.Code)

	start := time.Now()
	rec = perform(t, mux, http.MethodPost, "/api/gpu/lease", map[string]any{
		"kind": "sd", "weight_mb": 4000, "wait_seconds": 1,
	})
	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
	assert.Equal(t, "1", rec.Header().Get("Retry-After"))
	assert.Contains(t, rec.Body.String(), ErrTimeout.Error())
	assert.GreaterOrEqual(t, time.Since(start), 900*time.Millisecond)

	var errResp map[string]string
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&errResp))
	assert.Equal(t, ErrTimeout.Error(), errResp["error"])
}

func TestAPI_LeaseGet(t *testing.T) {
	t.Parallel()
	a, _ := newTestAPI(5000, 5*time.Second)
	mux := http.NewServeMux()
	a.RegisterRoutes(mux)

	rec := perform(t, mux, http.MethodPost, "/api/gpu/lease", map[string]any{"kind": "sd", "weight_mb": 5000})
	require.Equal(t, http.StatusOK, rec.Code)
	var running leaseResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&running))

	rec = perform(t, mux, http.MethodPost, "/api/gpu/lease", map[string]any{"kind": "llm", "weight_mb": 4000})
	require.Equal(t, http.StatusAccepted, rec.Code)
	var queued leaseResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&queued))

	rec = perform(t, mux, http.MethodGet, "/api/gpu/lease/"+running.ID, nil)
	require.Equal(t, http.StatusOK, rec.Code)
	var activeResp leaseStatusResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&activeResp))
	assert.Equal(t, "active", activeResp.Status)
	assert.Zero(t, activeResp.Position)
	require.NotNil(t, activeResp.LeaseDeadline)

	rec = perform(t, mux, http.MethodGet, "/api/gpu/lease/"+queued.ID, nil)
	require.Equal(t, http.StatusOK, rec.Code)
	var queuedResp leaseStatusResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&queuedResp))
	assert.Equal(t, "queued", queuedResp.Status)
	assert.Equal(t, 1, queuedResp.Position)
	assert.Nil(t, queuedResp.LeaseDeadline)

	rec = perform(t, mux, http.MethodGet, "/api/gpu/lease/unknown-id", nil)
	require.Equal(t, http.StatusOK, rec.Code)
	var unknownResp leaseStatusResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&unknownResp))
	assert.Equal(t, "unknown", unknownResp.Status)
}

func TestAPI_LeaseQueuedContentType(t *testing.T) {
	t.Parallel()
	a, _ := newTestAPI(5000, 5*time.Second)
	mux := http.NewServeMux()
	a.RegisterRoutes(mux)

	rec := perform(t, mux, http.MethodPost, "/api/gpu/lease", map[string]any{"kind": "sd", "weight_mb": 5000})
	require.Equal(t, http.StatusOK, rec.Code)

	rec = perform(t, mux, http.MethodPost, "/api/gpu/lease", map[string]any{"kind": "llm", "weight_mb": 4000})
	require.Equal(t, http.StatusAccepted, rec.Code)
	assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))

	var resp leaseResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	assert.False(t, resp.Acquired)
	assert.Equal(t, 1, resp.Position)
}

func TestAPI_LeaseWaitClampedToMaxWait(t *testing.T) {
	a, _ := newTestAPI(5000, 1*time.Second)
	mux := http.NewServeMux()
	a.RegisterRoutes(mux)

	rec := perform(t, mux, http.MethodPost, "/api/gpu/lease", map[string]any{"kind": "sd", "weight_mb": 5000})
	require.Equal(t, http.StatusOK, rec.Code)

	start := time.Now()
	rec = perform(t, mux, http.MethodPost, "/api/gpu/lease", map[string]any{
		"kind": "sd", "weight_mb": 4000, "wait_seconds": 3600,
	})
	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
	assert.Equal(t, "1", rec.Header().Get("Retry-After"))
	assert.Contains(t, rec.Body.String(), ErrTimeout.Error())
	assert.Less(t, time.Since(start), 5*time.Second)
}

func TestAPI_NegativeWaitSecondsTreatedAsZero(t *testing.T) {
	t.Parallel()
	a, q := newTestAPI(5000, 5*time.Second)
	mux := http.NewServeMux()
	a.RegisterRoutes(mux)

	rec := perform(t, mux, http.MethodPost, "/api/gpu/lease", map[string]any{"kind": "sd", "weight_mb": 5000})
	require.Equal(t, http.StatusOK, rec.Code)

	rec = perform(t, mux, http.MethodPost, "/api/gpu/lease", map[string]any{
		"kind": "llm", "weight_mb": 4000, "wait_seconds": -5,
	})
	require.Equal(t, http.StatusAccepted, rec.Code)
	assert.Len(t, q.Status().Queue, 1)
}

func TestAPI_ClientTruncatedTo32Runes(t *testing.T) {
	t.Parallel()
	a, q := newTestAPI(5000, 5*time.Second)
	mux := http.NewServeMux()
	a.RegisterRoutes(mux)

	rec := perform(t, mux, http.MethodPost, "/api/gpu/lease", map[string]any{
		"kind": "sd", "client": strings.Repeat("к", 60), "weight_mb": 1000,
	})
	require.Equal(t, http.StatusOK, rec.Code)

	st := q.Status()
	require.Len(t, st.Running, 1)
	assert.Len(t, []rune(st.Running[0].Client), 32)
}

func TestAPI_LeaseBodyTooLarge(t *testing.T) {
	t.Parallel()
	a, _ := newTestAPI(5000, 5*time.Second)
	mux := http.NewServeMux()
	a.RegisterRoutes(mux)

	big := `{"kind":"sd","weight_mb":1000,"pad":"` + strings.Repeat("x", 2<<20) + `"}`
	rec := performRaw(t, mux, http.MethodPost, "/api/gpu/lease", big)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestAPI_QueueFull503(t *testing.T) {
	t.Parallel()
	q := New(Config{TotalBudgetMB: 1000, MaxWait: 2 * time.Second, LeaseTTL: time.Second, MaxQueueLen: 1})
	a := NewAPI(q)
	mux := http.NewServeMux()
	a.RegisterRoutes(mux)

	holder, err := q.Acquire(context.Background(), Spec{Kind: KindSD, WeightMB: 1000})
	require.NoError(t, err)
	defer holder.Release()

	rec := perform(t, mux, http.MethodPost, "/api/gpu/lease", map[string]any{"kind": "llm", "weight_mb": 500})
	require.Equal(t, http.StatusAccepted, rec.Code)

	rec = perform(t, mux, http.MethodPost, "/api/gpu/lease", map[string]any{
		"kind": "llm", "weight_mb": 500, "wait_seconds": 1,
	})
	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
	assert.Contains(t, rec.Body.String(), ErrQueueFull.Error())

	rec = perform(t, mux, http.MethodPost, "/api/gpu/lease", map[string]any{"kind": "llm", "weight_mb": 500})
	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
	assert.Contains(t, rec.Body.String(), ErrQueueFull.Error())
	assert.Len(t, q.Status().Queue, 1)
}

func TestAPI_ClientControlCharsStrippedOnIngress(t *testing.T) {
	t.Parallel()
	a, q := newTestAPI(1000, 5*time.Second)
	mux := http.NewServeMux()
	a.RegisterRoutes(mux)

	rec := perform(t, mux, http.MethodPost, "/api/gpu/lease", map[string]any{
		"kind": "sd", "client": "\u001b]52;c;boom\u0007x\ty\nz\u009bm", "weight_mb": 1000,
	})
	require.Equal(t, http.StatusOK, rec.Code)

	rec = perform(t, mux, http.MethodPost, "/api/gpu/lease", map[string]any{
		"kind": "llm", "client": "a\u001b[2J\tb", "weight_mb": 1000,
	})
	require.Equal(t, http.StatusAccepted, rec.Code)

	st := q.Status()
	require.Len(t, st.Running, 1)
	require.Len(t, st.Queue, 1)
	for _, client := range []string{st.Running[0].Client, st.Queue[0].Client} {
		for _, bad := range []string{"\u001b", "\u0007", "\t", "\n", "\u009b"} {
			assert.NotContains(t, client, bad)
		}
	}
	assert.Equal(t, "]52;c;boomxyzm", st.Running[0].Client)
	assert.Equal(t, "a[2Jb", st.Queue[0].Client)
}
