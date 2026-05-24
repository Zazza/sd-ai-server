package main

import (
	"net/http"

	"sd-studio-server/api"
	"sd-studio-server/config"
)

type BackendManager struct {
	config *config.Config
}

func NewBackendManager(cfg *config.Config) *BackendManager {
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
		api.WriteError(w, "no active backend", http.StatusNotFound)
		return
	}

	api.WriteJSON(w, map[string]interface{}{
		"key":     bm.config.ActiveSD,
		"name":    backend.Name,
		"process": backend.ProcessKey,
	})
}
