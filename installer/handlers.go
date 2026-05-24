package installer

import (
	"fmt"
	"log"
	"net/http"
	"strings"

	"sd-studio-server/api"
)

func (inst *Installer) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/server/install/status", inst.handleAllStatus)
	mux.HandleFunc("/api/server/install/status/", inst.handleOneStatus)
	mux.HandleFunc("/api/server/install/", inst.handleInstall)
	mux.HandleFunc("/api/server/install/logs/", inst.handleLogs)
}

func (inst *Installer) handleAllStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	api.WriteJSON(w, inst.Status())
}

func (inst *Installer) handleOneStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	key := strings.TrimPrefix(r.URL.Path, "/api/server/install/status/")
	key = strings.TrimSuffix(key, "/")
	if key == "" {
		http.Error(w, "key required", http.StatusBadRequest)
		return
	}

	inst.mu.RLock()
	s, ok := inst.statuses[key]
	inst.mu.RUnlock()
	if !ok {
		api.WriteError(w, "unknown key: "+key, http.StatusNotFound)
		return
	}
	api.WriteJSON(w, s)
}

func (inst *Installer) handleInstall(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	path := strings.TrimPrefix(r.URL.Path, "/api/server/install/")
	key := strings.TrimSuffix(path, "/")
	if key == "" || strings.Contains(key, "/") {
		http.Error(w, "key required", http.StatusBadRequest)
		return
	}

	if inst.getConfig(key) == nil {
		api.WriteError(w, "unknown install target: "+key, http.StatusNotFound)
		return
	}

	go func() {
		if err := inst.Install(key); err != nil {
			log.Printf("Install %s failed: %v", key, err)
		}
	}()

	api.WriteJSON(w, map[string]string{"status": "installing", "key": key})
}

func (inst *Installer) handleLogs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	path := strings.TrimPrefix(r.URL.Path, "/api/server/install/logs/")
	key := strings.TrimSuffix(path, "/")
	if key == "" {
		http.Error(w, "key required", http.StatusBadRequest)
		return
	}

	inst.mu.RLock()
	lb, ok := inst.logs[key]
	inst.mu.RUnlock()
	if !ok {
		api.WriteError(w, "unknown key: "+key, http.StatusNotFound)
		return
	}

	lines := 50
	if v := r.URL.Query().Get("lines"); v != "" {
		if n, err := parsePositiveInt(v); err == nil {
			lines = n
		}
	}

	api.WriteJSON(w, map[string]interface{}{
		"key":   key,
		"lines": lines,
		"logs":  lb.Lines(lines),
	})
}

func parsePositiveInt(s string) (int, error) {
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, fmt.Errorf("invalid")
		}
		n = n*10 + int(c-'0')
	}
	if n <= 0 {
		return 0, fmt.Errorf("must be positive")
	}
	return n, nil
}
