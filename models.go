package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type ModelInfo struct {
	Name      string `json:"name"`
	Size      int64  `json:"size"`
	Extension string `json:"extension"`
}

type LLMModelInfo struct {
	Name string `json:"name"`
	Size string `json:"size"`
}

type ModelManager struct {
	config *Config
	mu     sync.Mutex
}

func NewModelManager(cfg *Config) *ModelManager {
	return &ModelManager{config: cfg}
}

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
		writeError(w, "no active backend", http.StatusBadRequest)
		return
	}
	m.handleModelDir(w, r, backend.ModelsDir)
}

func (m *ModelManager) handleLoRAModels(w http.ResponseWriter, r *http.Request) {
	backend := m.config.GetActiveBackend()
	if backend == nil {
		writeError(w, "no active backend", http.StatusBadRequest)
		return
	}
	m.handleModelDir(w, r, backend.LoraDir)
}

func (m *ModelManager) handleVAEModels(w http.ResponseWriter, r *http.Request) {
	backend := m.config.GetActiveBackend()
	if backend == nil {
		writeError(w, "no active backend", http.StatusBadRequest)
		return
	}
	m.handleModelDir(w, r, backend.VaeDir)
}

func (m *ModelManager) handleModelDir(w http.ResponseWriter, r *http.Request, dir string) {
	switch r.Method {
	case http.MethodGet:
		models, err := m.ListModels(dir)
		if err != nil {
			writeError(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, models)

	case http.MethodPost:
		var req struct {
			URL      string `json:"url"`
			Filename string `json:"filename"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, "invalid request body", http.StatusBadRequest)
			return
		}
		if req.URL == "" || req.Filename == "" {
			writeError(w, "url and filename are required", http.StatusBadRequest)
			return
		}
		if err := m.DownloadModel(req.URL, dir, req.Filename); err != nil {
			writeError(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, map[string]string{"status": "downloaded", "filename": req.Filename})

	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (m *ModelManager) handleLLMModels(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		models, err := m.ListLLMModels()
		if err != nil {
			writeError(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, models)

	case http.MethodPost:
		var req struct {
			Name string `json:"name"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, "invalid request body", http.StatusBadRequest)
			return
		}
		if req.Name == "" {
			writeError(w, "name is required", http.StatusBadRequest)
			return
		}
		if err := m.PullLLMModel(req.Name); err != nil {
			writeError(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, map[string]string{"status": "pulled", "name": req.Name})

	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (m *ModelManager) handleLLMPullStream(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	name := r.URL.Query().Get("name")
	if name == "" {
		writeError(w, "name is required", http.StatusBadRequest)
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	binary := m.ollamaBinary()
	log.Printf("[llm-pull] starting: %s pull %s (binary=%s)", binary, name, binary)

	cmd := exec.Command(binary, "pull", name)
	cmd.Env = m.ollamaEnv()

	stdout, _ := cmd.StdoutPipe()
	stderr, _ := cmd.StderrPipe()

	if err := cmd.Start(); err != nil {
		log.Printf("[llm-pull] start failed: %v", err)
		fmt.Fprintf(w, "data: [ERROR] %s\n\n", err.Error())
		flusher.Flush()
		return
	}

	done := make(chan struct{})
	go m.streamOutput(stdout, w, flusher, done)
	go m.streamOutput(stderr, w, flusher, done)

	err := cmd.Wait()
	<-done
	<-done

	if err != nil {
		log.Printf("[llm-pull] failed: %v", err)
		fmt.Fprintf(w, "data: [ERROR] pull failed: %s\n\n", err.Error())
	} else {
		log.Printf("[llm-pull] done: %s", name)
		fmt.Fprintf(w, "data: [DONE]\n\n")
	}
	flusher.Flush()
}

func (m *ModelManager) streamOutput(r io.Reader, w http.ResponseWriter, f http.Flusher, done chan struct{}) {
	defer func() { done <- struct{}{} }()
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}
		fmt.Fprintf(w, "data: %s\n\n", line)
		f.Flush()
	}
}

func (m *ModelManager) handleDownloadStream(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	typ := r.URL.Query().Get("type")
	downloadURL := r.URL.Query().Get("url")
	filename := r.URL.Query().Get("filename")

	if typ == "" || downloadURL == "" || filename == "" {
		writeError(w, "type, url and filename are required", http.StatusBadRequest)
		return
	}

	backend := m.config.GetActiveBackend()
	if backend == nil {
		writeError(w, "no active backend", http.StatusBadRequest)
		return
	}

	var dir string
	switch typ {
	case "sd":
		dir = backend.ModelsDir
	case "lora":
		dir = backend.LoraDir
	case "vae":
		dir = backend.VaeDir
	default:
		writeError(w, "invalid type: must be sd, lora or vae", http.StatusBadRequest)
		return
	}

	if dir == "" {
		writeError(w, "directory not configured for type: "+typ, http.StatusBadRequest)
		return
	}

	filename = filepath.Base(filename)

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	if err := os.MkdirAll(dir, 0755); err != nil {
		fmt.Fprintf(w, "data: [ERROR] create dir: %s\n\n", err.Error())
		flusher.Flush()
		return
	}

	resp, err := http.Get(downloadURL)
	if err != nil {
		fmt.Fprintf(w, "data: [ERROR] download: %s\n\n", err.Error())
		flusher.Flush()
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		fmt.Fprintf(w, "data: [ERROR] download failed: status %d\n\n", resp.StatusCode)
		flusher.Flush()
		return
	}

	destPath := filepath.Join(dir, filename)
	f, err := os.Create(destPath)
	if err != nil {
		fmt.Fprintf(w, "data: [ERROR] create file: %s\n\n", err.Error())
		flusher.Flush()
		return
	}
	defer f.Close()

	pw := &sseProgressWriter{
		w:        f,
		total:    resp.ContentLength,
		written:  0,
		lastTime: 0,
		writer:   w,
		flusher:  flusher,
	}

	if _, err := io.Copy(pw, resp.Body); err != nil {
		os.Remove(destPath)
		fmt.Fprintf(w, "data: [ERROR] write file: %s\n\n", err.Error())
		flusher.Flush()
		return
	}

	fmt.Fprintf(w, "data: [DONE]\n\n")
	flusher.Flush()
}

type sseProgressWriter struct {
	w           io.Writer
	total       int64
	written     int64
	lastWritten int64
	lastTime    int64
	writer      http.ResponseWriter
	flusher     http.Flusher
}

func (pw *sseProgressWriter) Write(p []byte) (int, error) {
	n, err := pw.w.Write(p)
	if err != nil {
		return n, err
	}
	pw.written += int64(n)
	now := time.Now().UnixMilli()
	shouldFlush := pw.written-pw.lastWritten >= 1024*1024 || now-pw.lastTime >= 500
	if shouldFlush || pw.written == pw.total {
		pw.lastTime = now
		pw.lastWritten = pw.written
		var percent float64
		if pw.total > 0 {
			percent = float64(pw.written) / float64(pw.total) * 100
		}
		payload, _ := json.Marshal(map[string]interface{}{
			"downloaded": pw.written,
			"total":      pw.total,
			"percent":    percent,
		})
		fmt.Fprintf(pw.writer, "data: %s\n\n", payload)
		pw.flusher.Flush()
	}
	return n, nil
}

func (m *ModelManager) ListModels(dir string) ([]ModelInfo, error) {
	if dir == "" {
		return nil, fmt.Errorf("directory not configured")
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return []ModelInfo{}, nil
		}
		return nil, fmt.Errorf("read dir %s: %w", dir, err)
	}

	var models []ModelInfo
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		ext := strings.ToLower(filepath.Ext(entry.Name()))
		if ext != ".safetensors" && ext != ".ckpt" && ext != ".pt" && ext != ".bin" && ext != ".pth" {
			continue
		}
		models = append(models, ModelInfo{
			Name:      entry.Name(),
			Size:      info.Size(),
			Extension: ext,
		})
	}
	return models, nil
}

func (m *ModelManager) DownloadModel(url, targetDir, filename string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := os.MkdirAll(targetDir, 0755); err != nil {
		return fmt.Errorf("create dir: %w", err)
	}

	resp, err := http.Get(url)
	if err != nil {
		return fmt.Errorf("download: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download failed: status %d", resp.StatusCode)
	}

	destPath := filepath.Join(targetDir, filename)
	f, err := os.Create(destPath)
	if err != nil {
		return fmt.Errorf("create file: %w", err)
	}
	defer f.Close()

	if _, err := io.Copy(f, resp.Body); err != nil {
		os.Remove(destPath)
		return fmt.Errorf("write file: %w", err)
	}

	return nil
}

func (m *ModelManager) DeleteModel(dir, filename string) error {
	// Security: prevent path traversal
	filename = filepath.Base(filename)
	path := filepath.Join(dir, filename)
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("delete model: %w", err)
	}
	return nil
}

func (m *ModelManager) PullLLMModel(name string) error {
	cmd := exec.Command(m.ollamaBinary(), "pull", name)
	cmd.Env = m.ollamaEnv()
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("ollama pull %s: %s: %w", name, string(output), err)
	}
	return nil
}

func (m *ModelManager) ListLLMModels() ([]LLMModelInfo, error) {
	cmd := exec.Command(m.ollamaBinary(), "list")
	cmd.Env = m.ollamaEnv()
	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("ollama list: %s: %w", string(output), err)
	}

	var models []LLMModelInfo
	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	for i, line := range lines {
		if i == 0 {
			continue // skip header
		}
		fields := strings.Fields(line)
		if len(fields) >= 1 {
			models = append(models, LLMModelInfo{
				Name: fields[0],
			})
		}
	}
	return models, nil
}

func (m *ModelManager) DeleteLLMModel(name string) error {
	cmd := exec.Command(m.ollamaBinary(), "rm", name)
	cmd.Env = m.ollamaEnv()
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("ollama rm %s: %s: %w", name, string(output), err)
	}
	return nil
}

func (m *ModelManager) ollamaBinary() string {
	if pc, ok := m.config.Processes["ollama"]; ok && pc.Binary != "" {
		return pc.Binary
	}
	return "ollama"
}

func (m *ModelManager) ollamaModelsDir() string {
	return filepath.Join(m.config.DataDir, "models", "ollama")
}

func (m *ModelManager) ollamaEnv() []string {
	return append(os.Environ(), "OLLAMA_MODELS="+m.ollamaModelsDir())
}

// RegisterDeleteRoutes registers DELETE endpoints for model deletion
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
		writeError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]string{"status": "deleted", "filename": filename})
}

func (m *ModelManager) handleDeleteLLM(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	name := strings.TrimPrefix(r.URL.Path, "/api/server/models/delete/llm/")
	name = filepath.Base(name)

	if err := m.DeleteLLMModel(name); err != nil {
		writeError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]string{"status": "deleted", "name": name})
}
