package main

import (
	"encoding/json"
	"net/http"
	"strings"
)

type BackendManager struct {
	config  *Config
	manager *ProcessManager
}

func NewBackendManager(cfg *Config, pm *ProcessManager) *BackendManager {
	return &BackendManager{
		config:  cfg,
		manager: pm,
	}
}

func (bm *BackendManager) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/server/backends", bm.handleList)
	mux.HandleFunc("/api/server/backends/active", bm.handleActive)
	mux.HandleFunc("/api/server/backends/switch", bm.handleSwitch)
}

func (bm *BackendManager) handleList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	type backendInfo struct {
		Key  string `json:"key"`
		Name string `json:"name"`
	}

	var backends []backendInfo
	for key, b := range bm.config.Backends {
		backends = append(backends, backendInfo{Key: key, Name: b.Name})
	}
	writeJSON(w, backends)
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

func (bm *BackendManager) handleSwitch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Backend string `json:"backend"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, "invalid request body", http.StatusBadRequest)
		return
	}

	req.Backend = strings.TrimSpace(req.Backend)
	if req.Backend == "" {
		writeError(w, "backend is required", http.StatusBadRequest)
		return
	}

	backend, ok := bm.config.Backends[req.Backend]
	if !ok {
		writeError(w, "unknown backend: "+req.Backend, http.StatusBadRequest)
		return
	}

	// Stop current SD process
	_ = bm.manager.Stop(backend.ProcessKey)

	// Update active backend
	bm.config.ActiveSD = req.Backend

	// Update process config for SD
	bm.manager.UpdateProcessConfig(backend.ProcessKey, backend.Binary, backend.Args, backend.WorkDir)

	// Start with new backend
	if err := bm.manager.Start(backend.ProcessKey); err != nil {
		writeError(w, "failed to start backend: "+err.Error(), http.StatusInternalServerError)
		return
	}

	writeJSON(w, map[string]interface{}{
		"success": true,
		"backend": req.Backend,
		"name":    backend.Name,
	})
}
