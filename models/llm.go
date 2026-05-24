package models

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os/exec"
	"strings"

	"sd-studio-server/api"
)

func (m *ModelManager) handleLLMModels(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		models, err := m.ListLLMModels()
		if err != nil {
			api.WriteError(w, err.Error(), http.StatusInternalServerError)
			return
		}
		api.WriteJSON(w, models)

	case http.MethodPost:
		var req struct {
			Name string `json:"name"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			api.WriteError(w, "invalid request body", http.StatusBadRequest)
			return
		}
		if req.Name == "" {
			api.WriteError(w, "name is required", http.StatusBadRequest)
			return
		}
		if err := m.PullLLMModel(req.Name); err != nil {
			api.WriteError(w, err.Error(), http.StatusInternalServerError)
			return
		}
		api.WriteJSON(w, map[string]string{"status": "pulled", "name": req.Name})

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
		api.WriteError(w, "name is required", http.StatusBadRequest)
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		api.WriteError(w, "streaming not supported", http.StatusInternalServerError)
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
			continue
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
