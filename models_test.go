package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestModelManager(t *testing.T, modelsDir string) *ModelManager {
	t.Helper()
	cfg := &Config{
		ActiveSD: "test",
		Backends: map[string]BackendConfig{
			"test": {
				Name:      "Test",
				ModelsDir: modelsDir,
				LoraDir:   filepath.Join(modelsDir, "lora"),
				VaeDir:    filepath.Join(modelsDir, "vae"),
			},
		},
	}
	return NewModelManager(cfg)
}

func TestHandleDownloadStream_MissingParams(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		url  string
	}{
		{
			name: "missing_all",
			url:  "/api/server/models/download/stream",
		},
		{
			name: "missing_url_and_filename",
			url:  "/api/server/models/download/stream?type=sd",
		},
		{
			name: "missing_type",
			url:  "/api/server/models/download/stream?url=http://x&filename=test.bin",
		},
		{
			name: "missing_filename",
			url:  "/api/server/models/download/stream?type=sd&url=http://x",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			mm := newTestModelManager(t, t.TempDir())
			req := httptest.NewRequest(http.MethodGet, tt.url, nil)
			rec := httptest.NewRecorder()

			mm.handleDownloadStream(rec, req)

			assert.Equal(t, http.StatusBadRequest, rec.Code)
			assert.Contains(t, rec.Body.String(), "error")
		})
	}
}

func TestHandleDownloadStream_InvalidType(t *testing.T) {
	t.Parallel()

	mm := newTestModelManager(t, t.TempDir())
	req := httptest.NewRequest(http.MethodGet,
		"/api/server/models/download/stream?type=invalid&url=http://x&filename=test.bin", nil)
	rec := httptest.NewRecorder()

	mm.handleDownloadStream(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)

	var body map[string]string
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Contains(t, body["error"], "invalid type")
}

func TestHandleDownloadStream_MethodNotAllowed(t *testing.T) {
	t.Parallel()

	mm := newTestModelManager(t, t.TempDir())
	req := httptest.NewRequest(http.MethodPost,
		"/api/server/models/download/stream?type=sd&url=http://x&filename=test.bin", nil)
	rec := httptest.NewRecorder()

	mm.handleDownloadStream(rec, req)

	assert.Equal(t, http.StatusMethodNotAllowed, rec.Code)
}

func TestHandleDownloadStream_NoActiveBackend(t *testing.T) {
	t.Parallel()

	cfg := &Config{
		ActiveSD: "nonexistent",
		Backends: map[string]BackendConfig{},
	}
	mm := NewModelManager(cfg)

	req := httptest.NewRequest(http.MethodGet,
		"/api/server/models/download/stream?type=sd&url=http://x&filename=test.bin", nil)
	rec := httptest.NewRecorder()

	mm.handleDownloadStream(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)

	var body map[string]string
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Contains(t, body["error"], "no active backend")
}

func TestHandleDownloadStream_EmptyDir(t *testing.T) {
	t.Parallel()

	cfg := &Config{
		ActiveSD: "test",
		Backends: map[string]BackendConfig{
			"test": {
				Name:      "Test",
				ModelsDir: "",
			},
		},
	}
	mm := NewModelManager(cfg)

	req := httptest.NewRequest(http.MethodGet,
		"/api/server/models/download/stream?type=sd&url=http://x&filename=test.bin", nil)
	rec := httptest.NewRecorder()

	mm.handleDownloadStream(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)

	var body map[string]string
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Contains(t, body["error"], "directory not configured")
}

func TestHandleDownloadStream_Success(t *testing.T) {
	t.Parallel()

	contentSize := 5 * 1024 * 1024
	content := bytes.Repeat([]byte("A"), contentSize)

	fileServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", fmt.Sprintf("%d", contentSize))
		w.Write(content)
	}))
	defer fileServer.Close()

	tmpDir := t.TempDir()
	mm := newTestModelManager(t, tmpDir)

	url := fmt.Sprintf("%s/testfile", fileServer.URL)
	req := httptest.NewRequest(http.MethodGet,
		fmt.Sprintf("/api/server/models/download/stream?type=sd&url=%s&filename=test.safetensors", url), nil)
	rec := httptest.NewRecorder()

	mm.handleDownloadStream(rec, req)

	body := rec.Body.String()
	assert.Contains(t, body, "data: [DONE]")

	lines := strings.Split(strings.TrimSpace(body), "\n")
	var progressFound bool
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		payload := strings.TrimPrefix(line, "data: ")
		if payload == "[DONE]" || strings.HasPrefix(payload, "[ERROR]") {
			continue
		}
		var progress map[string]interface{}
		if err := json.Unmarshal([]byte(payload), &progress); err == nil {
			progressFound = true
			assert.Contains(t, progress, "downloaded")
			assert.Contains(t, progress, "total")
			assert.Contains(t, progress, "percent")
			downloaded, _ := progress["downloaded"].(float64)
			total, _ := progress["total"].(float64)
			percent, _ := progress["percent"].(float64)
			assert.GreaterOrEqual(t, downloaded, float64(0))
			assert.Equal(t, float64(contentSize), total)
			assert.GreaterOrEqual(t, percent, float64(0))
			assert.LessOrEqual(t, percent, float64(100))
		}
	}
	assert.True(t, progressFound, "expected at least one progress SSE event")

	destPath := filepath.Join(tmpDir, "test.safetensors")
	info, err := os.Stat(destPath)
	require.NoError(t, err)
	assert.Equal(t, int64(contentSize), info.Size())
}

func TestHandleDownloadStream_LoraType(t *testing.T) {
	t.Parallel()

	content := bytes.Repeat([]byte("B"), 1024)

	fileServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "1024")
		w.Write(content)
	}))
	defer fileServer.Close()

	tmpDir := t.TempDir()
	mm := newTestModelManager(t, tmpDir)

	url := fmt.Sprintf("%s/lorafile", fileServer.URL)
	req := httptest.NewRequest(http.MethodGet,
		fmt.Sprintf("/api/server/models/download/stream?type=lora&url=%s&filename=lora.safetensors", url), nil)
	rec := httptest.NewRecorder()

	mm.handleDownloadStream(rec, req)

	body := rec.Body.String()
	assert.Contains(t, body, "data: [DONE]")

	destPath := filepath.Join(tmpDir, "lora", "lora.safetensors")
	info, err := os.Stat(destPath)
	require.NoError(t, err)
	assert.Equal(t, int64(1024), info.Size())
}

func TestHandleDownloadStream_VaeType(t *testing.T) {
	t.Parallel()

	content := bytes.Repeat([]byte("C"), 512)

	fileServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "512")
		w.Write(content)
	}))
	defer fileServer.Close()

	tmpDir := t.TempDir()
	mm := newTestModelManager(t, tmpDir)

	url := fmt.Sprintf("%s/vaefile", fileServer.URL)
	req := httptest.NewRequest(http.MethodGet,
		fmt.Sprintf("/api/server/models/download/stream?type=vae&url=%s&filename=vae.safetensors", url), nil)
	rec := httptest.NewRecorder()

	mm.handleDownloadStream(rec, req)

	body := rec.Body.String()
	assert.Contains(t, body, "data: [DONE]")

	destPath := filepath.Join(tmpDir, "vae", "vae.safetensors")
	info, err := os.Stat(destPath)
	require.NoError(t, err)
	assert.Equal(t, int64(512), info.Size())
}

func TestHandleDownloadStream_DownloadFails_404(t *testing.T) {
	t.Parallel()

	fileServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer fileServer.Close()

	tmpDir := t.TempDir()
	mm := newTestModelManager(t, tmpDir)

	url := fmt.Sprintf("%s/nonexistent", fileServer.URL)
	req := httptest.NewRequest(http.MethodGet,
		fmt.Sprintf("/api/server/models/download/stream?type=sd&url=%s&filename=test.bin", url), nil)
	rec := httptest.NewRecorder()

	mm.handleDownloadStream(rec, req)

	body := rec.Body.String()
	assert.Contains(t, body, "[ERROR]")
	assert.Contains(t, body, "404")
}

func TestHandleDownloadStream_DownloadFails_InvalidURL(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	mm := newTestModelManager(t, tmpDir)

	req := httptest.NewRequest(http.MethodGet,
		"/api/server/models/download/stream?type=sd&url=http://127.0.0.1:1/nonexistent&filename=test.bin", nil)
	rec := httptest.NewRecorder()

	mm.handleDownloadStream(rec, req)

	body := rec.Body.String()
	assert.Contains(t, body, "[ERROR]")
}

func TestHandleDownloadStream_FilenameSanitization(t *testing.T) {
	t.Parallel()

	content := bytes.Repeat([]byte("D"), 256)

	fileServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "256")
		w.Write(content)
	}))
	defer fileServer.Close()

	tmpDir := t.TempDir()
	mm := newTestModelManager(t, tmpDir)

	url := fmt.Sprintf("%s/file", fileServer.URL)
	req := httptest.NewRequest(http.MethodGet,
		fmt.Sprintf("/api/server/models/download/stream?type=sd&url=%s&filename=../../etc/passwd", url), nil)
	rec := httptest.NewRecorder()

	mm.handleDownloadStream(rec, req)

	body := rec.Body.String()
	assert.Contains(t, body, "data: [DONE]")

	destPath := filepath.Join(tmpDir, "passwd")
	info, err := os.Stat(destPath)
	require.NoError(t, err)
	assert.Equal(t, int64(256), info.Size())

	traversalPath := filepath.Join(tmpDir, "..", "..", "etc", "passwd")
	_, err = os.Stat(traversalPath)
	assert.True(t, os.IsNotExist(err), "path traversal should be prevented")
}

type mockFlusher struct {
	bytes.Buffer
	flushed []byte
}

func (m *mockFlusher) Flush() {}

type trackingFlusher struct {
	http.ResponseWriter
	flushCount int
}

func (t *trackingFlusher) Flush() {
	t.flushCount++
}

func TestSSEProgressWriter_SmallWrite(t *testing.T) {
	rec := httptest.NewRecorder()
	flusher := &trackingFlusher{ResponseWriter: rec}

	var fileBuf bytes.Buffer
	pw := &sseProgressWriter{
		w:       &fileBuf,
		total:   1024 * 1024,
		writer:  flusher,
		flusher: flusher,
	}

	chunk := make([]byte, 512)
	for i := range chunk {
		chunk[i] = 'X'
	}

	n, err := pw.Write(chunk)
	assert.NoError(t, err)
	assert.Equal(t, 512, n)
	assert.Equal(t, int64(512), pw.written)
	assert.Equal(t, 512, fileBuf.Len())
}

func TestSSEProgressWriter_LargeWrite_FlushesProgress(t *testing.T) {
	rec := httptest.NewRecorder()
	flusher := &trackingFlusher{ResponseWriter: rec}

	var fileBuf bytes.Buffer
	total := int64(5 * 1024 * 1024)
	pw := &sseProgressWriter{
		w:       &fileBuf,
		total:   total,
		writer:  flusher,
		flusher: flusher,
	}

	chunk := make([]byte, 2*1024*1024)
	for i := range chunk {
		chunk[i] = 'Y'
	}

	n, err := pw.Write(chunk)
	require.NoError(t, err)
	assert.Equal(t, 2*1024*1024, n)

	sseOutput := rec.Body.String()
	assert.Contains(t, sseOutput, "data: ")

	var progress map[string]interface{}
	lines := strings.Split(sseOutput, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "data: ") {
			payload := strings.TrimPrefix(line, "data: ")
			require.NoError(t, json.Unmarshal([]byte(payload), &progress))
			break
		}
	}
	require.NotNil(t, progress)
	assert.Equal(t, float64(2*1024*1024), progress["downloaded"])
	assert.Equal(t, float64(total), progress["total"])
}

func TestSSEProgressWriter_CompleteWrite_SendsFinalProgress(t *testing.T) {
	rec := httptest.NewRecorder()
	flusher := &trackingFlusher{ResponseWriter: rec}

	var fileBuf bytes.Buffer
	total := int64(1024)
	pw := &sseProgressWriter{
		w:       &fileBuf,
		total:   total,
		writer:  flusher,
		flusher: flusher,
	}

	data := bytes.Repeat([]byte("Z"), 1024)
	n, err := pw.Write(data)
	require.NoError(t, err)
	assert.Equal(t, 1024, n)

	assert.True(t, flusher.flushCount > 0)

	sseOutput := rec.Body.String()
	var progress map[string]interface{}
	lines := strings.Split(sseOutput, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "data: ") {
			payload := strings.TrimPrefix(line, "data: ")
			require.NoError(t, json.Unmarshal([]byte(payload), &progress))
		}
	}
	require.NotNil(t, progress)
	assert.Equal(t, float64(100), progress["percent"])
	assert.Equal(t, float64(total), progress["downloaded"])
	assert.Equal(t, float64(total), progress["total"])
}

func TestSSEProgressWriter_WriteError(t *testing.T) {
	rec := httptest.NewRecorder()
	flusher := &trackingFlusher{ResponseWriter: rec}

	pw := &sseProgressWriter{
		w:       &errorWriter{},
		total:   1024,
		writer:  flusher,
		flusher: flusher,
	}

	_, err := pw.Write([]byte("data"))
	assert.Error(t, err)
}

type errorWriter struct{}

func (e *errorWriter) Write(_ []byte) (int, error) {
	return 0, io.ErrUnexpectedEOF
}

func TestHandleDownloadStream_LargeFile_MultipleProgressEvents(t *testing.T) {
	t.Parallel()

	contentSize := 3 * 1024 * 1024
	content := bytes.Repeat([]byte("M"), contentSize)

	fileServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", fmt.Sprintf("%d", contentSize))
		w.Write(content)
	}))
	defer fileServer.Close()

	tmpDir := t.TempDir()
	mm := newTestModelManager(t, tmpDir)

	url := fmt.Sprintf("%s/bigfile", fileServer.URL)
	req := httptest.NewRequest(http.MethodGet,
		fmt.Sprintf("/api/server/models/download/stream?type=sd&url=%s&filename=big.safetensors", url), nil)
	rec := httptest.NewRecorder()

	mm.handleDownloadStream(rec, req)

	body := rec.Body.String()
	assert.Contains(t, body, "data: [DONE]")

	progressCount := 0
	lines := strings.Split(body, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		payload := strings.TrimPrefix(line, "data: ")
		if payload == "[DONE]" || strings.HasPrefix(payload, "[ERROR]") {
			continue
		}
		var progress map[string]interface{}
		if err := json.Unmarshal([]byte(payload), &progress); err == nil {
			progressCount++
		}
	}
	assert.GreaterOrEqual(t, progressCount, 2, "expected multiple progress events for large file")
}
