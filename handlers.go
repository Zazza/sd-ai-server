package main

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
)

type Handlers struct {
	manager   *ProcessManager
	health    *HealthMonitor
	gpu       *GPUMonitor
	config    *Config
	installer *Installer
}

func NewHandlers(pm *ProcessManager, hm *HealthMonitor, gm *GPUMonitor, cfg *Config, inst *Installer) *Handlers {
	return &Handlers{
		manager:   pm,
		health:    hm,
		gpu:       gm,
		config:    cfg,
		installer: inst,
	}
}

func (h *Handlers) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/server/status", h.handleStatus)
	mux.HandleFunc("/api/server/start/", h.handleStart)
	mux.HandleFunc("/api/server/stop/", h.handleStop)
	mux.HandleFunc("/api/server/restart/", h.handleRestart)
	mux.HandleFunc("/api/server/logs/", h.handleLogs)
}

func (h *Handlers) handleStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	statuses := h.manager.Status()
	healthResults := h.health.Results()
	gpuInfo := h.gpu.Info()

	resp := map[string]interface{}{
		"processes": statuses,
		"health":    healthResults,
		"gpu":       gpuInfo,
		"installs":  h.installer.Status(),
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func (h *Handlers) handleStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	name := strings.TrimPrefix(r.URL.Path, "/api/server/start/")
	if name == "" {
		http.Error(w, "process name required", http.StatusBadRequest)
		return
	}

	if err := h.manager.Start(name); err != nil {
		writeError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	writeJSON(w, map[string]string{"status": "started", "process": name})
}

func (h *Handlers) handleStop(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	name := strings.TrimPrefix(r.URL.Path, "/api/server/stop/")
	if name == "" {
		http.Error(w, "process name required", http.StatusBadRequest)
		return
	}

	if err := h.manager.Stop(name); err != nil {
		writeError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	writeJSON(w, map[string]string{"status": "stopped", "process": name})
}

func (h *Handlers) handleRestart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	name := strings.TrimPrefix(r.URL.Path, "/api/server/restart/")
	if name == "" {
		http.Error(w, "process name required", http.StatusBadRequest)
		return
	}

	if err := h.manager.Restart(name); err != nil {
		writeError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	writeJSON(w, map[string]string{"status": "restarted", "process": name})
}

func (h *Handlers) handleLogs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	path := strings.TrimPrefix(r.URL.Path, "/api/server/logs/")
	name := strings.TrimSuffix(path, "/")
	if name == "" {
		http.Error(w, "process name required", http.StatusBadRequest)
		return
	}

	lines := 50
	if v := r.URL.Query().Get("lines"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			lines = n
		}
	}

	logs, err := h.manager.Logs(name, lines)
	if err != nil {
		writeError(w, err.Error(), http.StatusNotFound)
		return
	}

	writeJSON(w, map[string]interface{}{
		"process": name,
		"lines":   lines,
		"logs":    logs,
	})
}

func writeJSON(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, msg string, code int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
