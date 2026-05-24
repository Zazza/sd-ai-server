package models

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"sd-studio-server/api"
	"sd-studio-server/installer"
)

func (m *ModelManager) handleDownloadStream(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	typ := r.URL.Query().Get("type")
	downloadURL := r.URL.Query().Get("url")
	filename := r.URL.Query().Get("filename")

	if typ == "" || downloadURL == "" || filename == "" {
		api.WriteError(w, "type, url and filename are required", http.StatusBadRequest)
		return
	}

	backend := m.config.GetActiveBackend()
	if backend == nil {
		api.WriteError(w, "no active backend", http.StatusBadRequest)
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
		api.WriteError(w, "invalid type: must be sd, lora or vae", http.StatusBadRequest)
		return
	}

	if dir == "" {
		api.WriteError(w, "directory not configured for type: "+typ, http.StatusBadRequest)
		return
	}

	filename = filepath.Base(filename)

	flusher, ok := w.(http.Flusher)
	if !ok {
		api.WriteError(w, "streaming not supported", http.StatusInternalServerError)
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
	log.Printf("[download] %s -> %s (%s)", filename, dir, installer.FormatBytes(resp.ContentLength))

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
	log.Printf("[download] complete: %s (%s)", filename, installer.FormatBytes(pw.written))
}

type sseProgressWriter struct {
	w           io.Writer
	total       int64
	written     int64
	lastWritten int64
	lastTime    int64
	lastLog     int64
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

		if now-pw.lastLog >= 10000 || pw.written == pw.total {
			pw.lastLog = now
			pct := ""
			if pw.total > 0 {
				pct = fmt.Sprintf(" (%.0f%%)", percent)
			}
			log.Printf("[download] %s / %s%s", installer.FormatBytes(pw.written), installer.FormatBytes(pw.total), pct)
		}
	}
	return n, nil
}
