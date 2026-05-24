package models

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"sd-studio-server/config"
)

type ModelManager struct {
	config *config.Config
	mu     sync.Mutex
}

func NewModelManager(cfg *config.Config) *ModelManager {
	return &ModelManager{config: cfg}
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
	filename = filepath.Base(filename)
	path := filepath.Join(dir, filename)
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("delete model: %w", err)
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
