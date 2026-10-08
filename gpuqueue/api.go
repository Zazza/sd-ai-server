package gpuqueue

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"sd-studio-server/api"
)

type API struct {
	q *Queue
}

func NewAPI(q *Queue) *API {
	return &API{q: q}
}

func (a *API) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/gpu/lease", a.handleLeaseCreate)
	mux.HandleFunc("GET /api/gpu/lease/{id}", a.handleLeaseGet)
	mux.HandleFunc("POST /api/gpu/lease/{id}/heartbeat", a.handleLeaseHeartbeat)
	mux.HandleFunc("DELETE /api/gpu/lease/{id}", a.handleLeaseDelete)
	mux.HandleFunc("PATCH /api/gpu/queue/{id}", a.handleQueueMove)
	mux.HandleFunc("GET /api/gpu/status", a.handleStatus)
}

type leaseRequest struct {
	Kind        Kind   `json:"kind"`
	Client      string `json:"client"`
	WeightMB    int    `json:"weight_mb"`
	Priority    int    `json:"priority"`
	WaitSeconds int    `json:"wait_seconds"`
}

type leaseResponse struct {
	ID         string `json:"id"`
	Acquired   bool   `json:"acquired"`
	TTLSeconds int    `json:"ttl_seconds,omitempty"`
	Position   int    `json:"position,omitempty"`
}

const (
	maxRequestBody = 1 << 20
	maxClientRunes = 32
)

func (a *API) handleLeaseCreate(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBody)
	var req leaseRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		api.WriteError(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if !req.Kind.Valid() {
		api.WriteError(w, "invalid kind", http.StatusBadRequest)
		return
	}
	if !a.q.Enabled() {
		api.WriteJSON(w, leaseResponse{Acquired: true})
		return
	}
	st := a.q.Status()
	if req.WeightMB <= 0 || req.WeightMB > st.Budget {
		api.WriteError(w, "invalid weight_mb", http.StatusBadRequest)
		return
	}

	spec := Spec{
		Kind:     req.Kind,
		Client:   truncateRunes(SanitizeClient(req.Client), maxClientRunes),
		WeightMB: req.WeightMB,
		Priority: req.Priority,
	}

	if req.WaitSeconds > 0 {
		waitSeconds := a.clampWaitSeconds(req.WaitSeconds)
		ctx, cancel := context.WithTimeout(r.Context(), time.Duration(waitSeconds)*time.Second)
		defer cancel()
		lease, err := a.q.Acquire(ctx, spec)
		if err != nil {
			a.writeAcquireError(w, ctx, err, waitSeconds)
			return
		}
		api.WriteJSON(w, leaseResponse{ID: lease.ID(), Acquired: true, TTLSeconds: int(a.q.TTL().Seconds())})
		return
	}

	if a.q.QueueFull() {
		api.WriteError(w, ErrQueueFull.Error(), http.StatusServiceUnavailable)
		return
	}
	lease, acquired, position := a.q.TryAcquire(spec)
	if !acquired {
		if position <= 0 {
			api.WriteError(w, ErrQueueFull.Error(), http.StatusServiceUnavailable)
			return
		}
		api.WriteJSONStatus(w, http.StatusAccepted, leaseResponse{ID: lease.ID(), Acquired: false, Position: position})
		return
	}
	api.WriteJSON(w, leaseResponse{ID: lease.ID(), Acquired: true, TTLSeconds: int(a.q.TTL().Seconds())})
}

func (a *API) clampWaitSeconds(n int) int {
	if n < 0 {
		return 0
	}
	maxWait := int(a.q.MaxWait().Seconds())
	if maxWait > 0 && n > maxWait {
		return maxWait
	}
	return n
}

func (a *API) writeAcquireError(w http.ResponseWriter, ctx context.Context, err error, retryAfter int) {
	if errors.Is(err, ErrQueueFull) {
		api.WriteError(w, ErrQueueFull.Error(), http.StatusServiceUnavailable)
		return
	}
	if errors.Is(err, ErrTimeout) ||
		(errors.Is(err, ErrCancelled) && errors.Is(ctx.Err(), context.DeadlineExceeded)) {
		if retryAfter > 0 {
			w.Header().Set("Retry-After", strconv.Itoa(retryAfter))
		}
		api.WriteError(w, ErrTimeout.Error(), http.StatusServiceUnavailable)
		return
	}
	if errors.Is(err, ErrCancelled) {
		api.WriteError(w, ErrCancelled.Error(), http.StatusServiceUnavailable)
		return
	}
	api.WriteError(w, err.Error(), http.StatusInternalServerError)
}

func truncateRunes(s string, max int) string {
	if len(s) <= max {
		return s
	}
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	return string(runes[:max])
}

type leaseStatusResponse struct {
	Status        string     `json:"status"`
	Position      int        `json:"position"`
	LeaseDeadline *time.Time `json:"lease_deadline,omitempty"`
}

func (a *API) handleLeaseGet(w http.ResponseWriter, r *http.Request) {
	job, ok := a.q.Get(r.PathValue("id"))
	if !ok {
		api.WriteJSON(w, leaseStatusResponse{Status: "unknown"})
		return
	}
	resp := leaseStatusResponse{Status: "queued", Position: job.Position}
	if !job.StartedAt.IsZero() {
		resp.Status = "active"
		resp.Position = 0
		resp.LeaseDeadline = &job.LeaseDeadline
	}
	api.WriteJSON(w, resp)
}

func (a *API) handleLeaseHeartbeat(w http.ResponseWriter, r *http.Request) {
	if !a.q.Heartbeat(r.PathValue("id")) {
		api.WriteError(w, ErrNotFound.Error(), http.StatusNotFound)
		return
	}
	api.WriteJSON(w, map[string]string{"status": "ok"})
}

func (a *API) handleLeaseDelete(w http.ResponseWriter, r *http.Request) {
	if !a.q.Cancel(r.PathValue("id")) {
		api.WriteError(w, ErrNotFound.Error(), http.StatusNotFound)
		return
	}
	api.WriteJSON(w, map[string]string{"status": "cancelled"})
}

func (a *API) handleQueueMove(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBody)
	var req struct {
		Move string `json:"move"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		api.WriteError(w, "invalid request body", http.StatusBadRequest)
		return
	}
	var dir int
	switch req.Move {
	case "up":
		dir = -1
	case "down":
		dir = 1
	default:
		api.WriteError(w, "invalid move", http.StatusBadRequest)
		return
	}
	if !a.q.Move(r.PathValue("id"), dir) {
		api.WriteError(w, ErrNotFound.Error(), http.StatusNotFound)
		return
	}
	api.WriteJSON(w, map[string]string{"status": "ok"})
}

func (a *API) handleStatus(w http.ResponseWriter, r *http.Request) {
	api.WriteJSON(w, a.q.Status())
}
