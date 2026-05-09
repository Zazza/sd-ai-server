package main

import (
	"net/http"
)

type BackendManager struct {
	config *Config
}

func NewBackendManager(cfg *Config) *BackendManager {
	return &BackendManager{config: cfg}
}

func (bm *BackendManager) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/server/backends/active", bm.handleActive)
}

func (bm *BackendManager) handleActive(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	backend := bm.config.GetActiveBackend()
	if backend == nil {
		writeError(w, "no active backend", http.StatusNotFound)
		return
	}

	writeJSON(w, map[string]interface{}{
		"key":     bm.config.ActiveSD,
		"name":    backend.Name,
		"process": backend.ProcessKey,
	})
}
