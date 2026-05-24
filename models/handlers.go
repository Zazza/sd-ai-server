package models

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"

	"sd-studio-server/api"
)

func (m *ModelManager) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/server/models/sd", m.handleSDModels)
	mux.HandleFunc("/api/server/models/lora", m.handleLoRAModels)
	mux.HandleFunc("/api/server/models/vae", m.handleVAEModels)
	mux.HandleFunc("/api/server/models/llm", m.handleLLMModels)
	mux.HandleFunc("/api/server/models/llm/pull", m.handleLLMPullStream)
	mux.HandleFunc("/api/server/models/download/stream", m.handleDownloadStream)
}

func (m *ModelManager) handleSDModels(w http.ResponseWriter, r *http.Request) {
	backend := m.config.GetActiveBackend()
	if backend == nil {
		api.WriteError(w, "no active backend", http.StatusBadRequest)
		return
	}
	m.handleModelDir(w, r, backend.ModelsDir)
}

func (m *ModelManager) handleLoRAModels(w http.ResponseWriter, r *http.Request) {
	backend := m.config.GetActiveBackend()
	if backend == nil {
		api.WriteError(w, "no active backend", http.StatusBadRequest)
		return
	}
	m.handleModelDir(w, r, backend.LoraDir)
}

func (m *ModelManager) handleVAEModels(w http.ResponseWriter, r *http.Request) {
	backend := m.config.GetActiveBackend()
	if backend == nil {
		api.WriteError(w, "no active backend", http.StatusBadRequest)
		return
	}
	m.handleModelDir(w, r, backend.VaeDir)
}

func (m *ModelManager) handleModelDir(w http.ResponseWriter, r *http.Request, dir string) {
	switch r.Method {
	case http.MethodGet:
		models, err := m.ListModels(dir)
		if err != nil {
			api.WriteError(w, err.Error(), http.StatusInternalServerError)
			return
		}
		api.WriteJSON(w, models)

	case http.MethodPost:
		var req struct {
			URL      string `json:"url"`
			Filename string `json:"filename"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			api.WriteError(w, "invalid request body", http.StatusBadRequest)
			return
		}
		if req.URL == "" || req.Filename == "" {
			api.WriteError(w, "url and filename are required", http.StatusBadRequest)
			return
		}
		if err := m.DownloadModel(req.URL, dir, req.Filename); err != nil {
			api.WriteError(w, err.Error(), http.StatusInternalServerError)
			return
		}
		api.WriteJSON(w, map[string]string{"status": "downloaded", "filename": req.Filename})

	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (m *ModelManager) RegisterDeleteRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/server/models/delete/sd/", m.handleDeleteSD)
	mux.HandleFunc("/api/server/models/delete/lora/", m.handleDeleteLoRA)
	mux.HandleFunc("/api/server/models/delete/vae/", m.handleDeleteVAE)
	mux.HandleFunc("/api/server/models/delete/llm/", m.handleDeleteLLM)
}

func (m *ModelManager) handleDeleteSD(w http.ResponseWriter, r *http.Request) {
	m.handleDeleteFromDir(w, r, m.config.GetActiveBackend().ModelsDir)
}

func (m *ModelManager) handleDeleteLoRA(w http.ResponseWriter, r *http.Request) {
	m.handleDeleteFromDir(w, r, m.config.GetActiveBackend().LoraDir)
}

func (m *ModelManager) handleDeleteVAE(w http.ResponseWriter, r *http.Request) {
	m.handleDeleteFromDir(w, r, m.config.GetActiveBackend().VaeDir)
}

func (m *ModelManager) handleDeleteFromDir(w http.ResponseWriter, r *http.Request, dir string) {
	if r.Method != http.MethodDelete {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	filename := strings.TrimPrefix(r.URL.Path, "/api/server/models/delete/")
	filename = strings.TrimPrefix(filename, "sd/")
	filename = strings.TrimPrefix(filename, "lora/")
	filename = strings.TrimPrefix(filename, "vae/")
	filename = filepath.Base(filename)

	if err := m.DeleteModel(dir, filename); err != nil {
		api.WriteError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	api.WriteJSON(w, map[string]string{"status": "deleted", "filename": filename})
}

func (m *ModelManager) handleDeleteLLM(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	name := strings.TrimPrefix(r.URL.Path, "/api/server/models/delete/llm/")
	name = filepath.Base(name)

	if err := m.DeleteLLMModel(name); err != nil {
		api.WriteError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	api.WriteJSON(w, map[string]string{"status": "deleted", "name": name})
}
