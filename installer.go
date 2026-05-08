package main

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

type InstallMethod string

const (
	InstallZip     InstallMethod = "zip"
	InstallBinary  InstallMethod = "binary"
	InstallPip     InstallMethod = "pip"
	InstallArchive InstallMethod = "archive"
	InstallTgz     InstallMethod = "tgz"
)

type InstallStatus struct {
	Key        string `json:"key"`
	Installed  bool   `json:"installed"`
	Installing bool   `json:"installing"`
	Progress   string `json:"progress"`
	Error      string `json:"error,omitempty"`
}

type Installer struct {
	config   *Config
	mu       sync.RWMutex
	statuses map[string]*InstallStatus
	logs     map[string]*RingBuffer
}

func NewInstaller(cfg *Config) *Installer {
	inst := &Installer{
		config:   cfg,
		statuses: make(map[string]*InstallStatus),
		logs:     make(map[string]*RingBuffer),
	}

	for key, bc := range cfg.Backends {
		if bc.Install.Method != "" {
			s := &InstallStatus{Key: key}
			s.Installed = inst.checkInstalled(bc.Install)
			inst.statuses[key] = s
			inst.logs[key] = NewRingBuffer(ringBufferSize)
		}
	}

	for key, pc := range cfg.Processes {
		if pc.Install.Method != "" {
			if _, exists := inst.statuses[key]; exists {
				continue
			}
			s := &InstallStatus{Key: key}
			s.Installed = inst.checkInstalled(pc.Install)
			inst.statuses[key] = s
			inst.logs[key] = NewRingBuffer(ringBufferSize)
		}
	}

	return inst
}

func (inst *Installer) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/server/install/status", inst.handleAllStatus)
	mux.HandleFunc("/api/server/install/status/", inst.handleOneStatus)
	mux.HandleFunc("/api/server/install/", inst.handleInstall)
	mux.HandleFunc("/api/server/install/logs/", inst.handleLogs)
}

func (inst *Installer) IsInstalled(key string) bool {
	inst.mu.RLock()
	s, ok := inst.statuses[key]
	inst.mu.RUnlock()
	if !ok {
		return false
	}
	return s.Installed
}

func (inst *Installer) EnsureInstalled(key, binary string) error {
	if binary != "" {
		if strings.ContainsRune(binary, '/') {
			if _, err := os.Stat(binary); err == nil {
				return nil
			}
		} else {
			if _, err := exec.LookPath(binary); err == nil {
				return nil
			}
		}
	}
	ic := inst.getConfig(key)
	if ic == nil {
		return nil
	}
	return inst.Install(key)
}

func (inst *Installer) ensureStatus(key string) *RingBuffer {
	inst.mu.Lock()
	defer inst.mu.Unlock()

	s, ok := inst.statuses[key]
	if !ok {
		s = &InstallStatus{Key: key}
		ic := inst.getConfig(key)
		if ic != nil {
			s.Installed = inst.checkInstalled(*ic)
		}
		inst.statuses[key] = s
	}

	lb, ok := inst.logs[key]
	if !ok {
		lb = NewRingBuffer(ringBufferSize)
		inst.logs[key] = lb
	}
	return lb
}

func (inst *Installer) getConfig(key string) *InstallConfig {
	if bc, ok := inst.config.Backends[key]; ok && bc.Install.Method != "" {
		return &bc.Install
	}
	if pc, ok := inst.config.Processes[key]; ok && pc.Install.Method != "" {
		return &pc.Install
	}
	return nil
}

func (inst *Installer) Install(key string) error {
	lb := inst.ensureStatus(key)

	inst.mu.Lock()
	s := inst.statuses[key]
	if s.Installing {
		inst.mu.Unlock()
		return fmt.Errorf("already installing: %s", key)
	}
	if s.Installed {
		inst.mu.Unlock()
		return nil
	}

	ic := inst.getConfig(key)
	if ic == nil {
		inst.mu.Unlock()
		return fmt.Errorf("no install config for: %s", key)
	}

	s.Installing = true
	s.Progress = "starting"
	s.Error = ""
	inst.statuses[key] = s
	inst.mu.Unlock()

	err := inst.doInstall(key, *ic, lb)

	inst.mu.Lock()
	s = inst.statuses[key]
	s.Installing = false
	if err != nil {
		s.Error = err.Error()
		s.Progress = "failed"
	} else {
		s.Installed = true
		s.Progress = "done"
	}
	inst.statuses[key] = s
	inst.mu.Unlock()

	return err
}

func (inst *Installer) Status() map[string]InstallStatus {
	inst.mu.RLock()
	defer inst.mu.RUnlock()

	result := make(map[string]InstallStatus, len(inst.statuses))
	for k, s := range inst.statuses {
		result[k] = *s
	}
	return result
}

func (inst *Installer) doInstall(key string, ic InstallConfig, lb *RingBuffer) error {
	switch ic.Method {
	case InstallZip:
		return inst.installZip(key, ic, lb)
	case InstallBinary:
		return inst.installBinary(key, ic, lb)
	case InstallPip:
		return inst.installPip(key, ic, lb)
	case InstallArchive:
		return inst.installArchive(key, ic, lb)
	case InstallTgz:
		return inst.installTgz(key, ic, lb)
	}
	return fmt.Errorf("unknown install method: %s", ic.Method)
}

func (inst *Installer) installZip(key string, ic InstallConfig, lb *RingBuffer) error {
	inst.setProgress(key, "downloading")
	lb.Write(fmt.Sprintf("Downloading %s -> %s", ic.URL, ic.Target))
	log.Printf("[%s] downloading %s -> %s", key, ic.URL, ic.Target)

	resp, err := http.Get(ic.URL)
	if err != nil {
		return fmt.Errorf("download: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download failed: HTTP %d", resp.StatusCode)
	}

	tmpFile, err := os.CreateTemp("", "sd-install-*.zip")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpPath := tmpFile.Name()
	defer os.Remove(tmpPath)

	pw := &progressWriter{
		w:        tmpFile,
		total:    resp.ContentLength,
		key:      key,
		target:   ic.Target,
		lastLog:  0,
		lastTime: 0,
		inst:     inst,
		lb:       lb,
	}
	size, err := io.Copy(pw, resp.Body)
	tmpFile.Close()
	if err != nil {
		return fmt.Errorf("save download: %w", err)
	}
	lb.Write(fmt.Sprintf("Downloaded %s", formatBytes(size)))
	log.Printf("[%s] downloaded %s", key, formatBytes(size))

	inst.setProgress(key, "extracting")
	lb.Write(fmt.Sprintf("Extracting to %s", ic.Target))
	log.Printf("[%s] extracting to %s", key, ic.Target)

	if err := extractZip(tmpPath, ic.Target); err != nil {
		return fmt.Errorf("extract: %w", err)
	}

	lb.Write("Installation complete")
	log.Printf("[%s] installed to %s", key, ic.Target)
	return nil
}

func (inst *Installer) installBinary(key string, ic InstallConfig, lb *RingBuffer) error {
	url := ic.URL
	if strings.Contains(url, "{os}") || strings.Contains(url, "{arch}") {
		url = strings.ReplaceAll(url, "{os}", runtime.GOOS)
		url = strings.ReplaceAll(url, "{arch}", runtime.GOARCH)
	}

	inst.setProgress(key, "downloading")
	lb.Write(fmt.Sprintf("Downloading %s -> %s", url, ic.Target))
	log.Printf("[%s] downloading %s -> %s", key, url, ic.Target)

	resp, err := http.Get(url)
	if err != nil {
		return fmt.Errorf("download: %w", err)
	}
	defer resp.Body.Close()

	log.Printf("[%s] HTTP %d, Content-Length: %s", key, resp.StatusCode, formatBytes(resp.ContentLength))
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download failed: HTTP %d", resp.StatusCode)
	}

	targetDir := filepath.Dir(ic.Target)
	if err := os.MkdirAll(targetDir, 0755); err != nil {
		return fmt.Errorf("create target dir: %w", err)
	}

	f, err := os.OpenFile(ic.Target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0755)
	if err != nil {
		return fmt.Errorf("create binary: %w", err)
	}

	totalSize := resp.ContentLength
	pw := &progressWriter{
		w:        f,
		total:    totalSize,
		key:      key,
		target:   ic.Target,
		lastLog:  0,
		lastTime: 0,
		inst:     inst,
		lb:       lb,
	}
	size, err := io.Copy(pw, resp.Body)
	f.Close()
	if err != nil {
		os.Remove(ic.Target)
		return fmt.Errorf("save binary: %w", err)
	}

	if err := os.Chmod(ic.Target, 0755); err != nil {
		return fmt.Errorf("chmod: %w", err)
	}

	lb.Write(fmt.Sprintf("Installed binary %s (%s)", ic.Target, formatBytes(size)))
	log.Printf("[%s] installed binary %s (%s)", key, ic.Target, formatBytes(size))
	return nil
}

func (inst *Installer) installArchive(key string, ic InstallConfig, lb *RingBuffer) error {
	url := ic.URL
	if strings.Contains(url, "{os}") || strings.Contains(url, "{arch}") {
		url = strings.ReplaceAll(url, "{os}", runtime.GOOS)
		url = strings.ReplaceAll(url, "{arch}", runtime.GOARCH)
	}

	inst.setProgress(key, "downloading")
	lb.Write(fmt.Sprintf("Downloading %s -> %s", url, ic.Target))
	log.Printf("[%s] downloading %s -> %s", key, url, ic.Target)

	resp, err := http.Get(url)
	if err != nil {
		return fmt.Errorf("download: %w", err)
	}
	defer resp.Body.Close()

	log.Printf("[%s] HTTP %d, Content-Length: %s", key, resp.StatusCode, formatBytes(resp.ContentLength))
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download failed: HTTP %d", resp.StatusCode)
	}

	ext := archiveExt(url)
	tmpFile, err := os.CreateTemp("", "sd-install-*"+ext)
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpPath := tmpFile.Name()
	defer os.Remove(tmpPath)

	pw := &progressWriter{
		w:        tmpFile,
		total:    resp.ContentLength,
		key:      key,
		target:   ic.Target,
		lastLog:  0,
		lastTime: 0,
		inst:     inst,
		lb:       lb,
	}
	size, err := io.Copy(pw, resp.Body)
	tmpFile.Close()
	if err != nil {
		return fmt.Errorf("save download: %w", err)
	}
	lb.Write(fmt.Sprintf("Downloaded %s", formatBytes(size)))
	log.Printf("[%s] downloaded %s", key, formatBytes(size))

	targetDir := filepath.Dir(ic.Target)
	if err := os.MkdirAll(targetDir, 0755); err != nil {
		return fmt.Errorf("create target dir: %w", err)
	}

	binaryName := filepath.Base(ic.Target)

	inst.setProgress(key, "extracting")
	lb.Write(fmt.Sprintf("Extracting %s from archive", binaryName))
	log.Printf("[%s] extracting %s from archive", key, binaryName)

	switch {
	case strings.HasSuffix(url, ".tgz") || strings.HasSuffix(url, ".tar.gz"):
		err = extractBinaryFromTgz(tmpPath, binaryName, ic.Target, lb)
	case strings.HasSuffix(url, ".tar.zst"):
		err = extractBinaryFromTarZst(tmpPath, binaryName, ic.Target, lb)
	case strings.HasSuffix(url, ".zip"):
		err = extractBinaryFromZip(tmpPath, binaryName, ic.Target)
	default:
		return fmt.Errorf("unsupported archive format: %s", url)
	}
	if err != nil {
		return fmt.Errorf("extract: %w", err)
	}

	if err := os.Chmod(ic.Target, 0755); err != nil {
		return fmt.Errorf("chmod: %w", err)
	}

	lb.Write(fmt.Sprintf("Installed %s (%s)", ic.Target, formatBytes(size)))
	log.Printf("[%s] installed %s (%s)", key, ic.Target, formatBytes(size))
	return nil
}

func (inst *Installer) installTgz(key string, ic InstallConfig, lb *RingBuffer) error {
	inst.setProgress(key, "downloading")
	lb.Write(fmt.Sprintf("Downloading %s -> %s", ic.URL, ic.Target))
	log.Printf("[%s] downloading %s -> %s", key, ic.URL, ic.Target)

	resp, err := http.Get(ic.URL)
	if err != nil {
		return fmt.Errorf("download: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download failed: HTTP %d", resp.StatusCode)
	}

	tmpFile, err := os.CreateTemp("", "sd-install-*.tar.gz")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpPath := tmpFile.Name()
	defer os.Remove(tmpPath)

	pw := &progressWriter{
		w:        tmpFile,
		total:    resp.ContentLength,
		key:      key,
		target:   ic.Target,
		lastLog:  0,
		lastTime: 0,
		inst:     inst,
		lb:       lb,
	}
	size, err := io.Copy(pw, resp.Body)
	tmpFile.Close()
	if err != nil {
		return fmt.Errorf("save download: %w", err)
	}
	lb.Write(fmt.Sprintf("Downloaded %s", formatBytes(size)))
	log.Printf("[%s] downloaded %s", key, formatBytes(size))

	inst.setProgress(key, "extracting")
	lb.Write(fmt.Sprintf("Extracting to %s", ic.Target))
	log.Printf("[%s] extracting to %s", key, ic.Target)

	if err := extractTgz(tmpPath, ic.Target); err != nil {
		return fmt.Errorf("extract: %w", err)
	}

	lb.Write("Installation complete")
	log.Printf("[%s] installed to %s", key, ic.Target)
	return nil
}

func extractTgz(tgzPath, targetDir string) error {
	f, err := os.Open(tgzPath)
	if err != nil {
		return err
	}
	defer f.Close()

	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gz.Close()

	tmpDir, err := os.MkdirTemp("", "sd-extract-*")
	if err != nil {
		return err
	}

	success := false
	defer func() {
		if !success {
			os.RemoveAll(tmpDir)
		}
	}()

	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}

		parts := strings.SplitN(hdr.Name, "/", 2)
		if len(parts) < 2 || parts[1] == "" {
			continue
		}
		relPath := parts[1]

		destPath := filepath.Join(tmpDir, relPath)

		if hdr.Typeflag == tar.TypeDir {
			os.MkdirAll(destPath, os.FileMode(hdr.Mode))
			continue
		}

		if hdr.Typeflag != tar.TypeReg {
			continue
		}

		if err := os.MkdirAll(filepath.Dir(destPath), 0755); err != nil {
			return err
		}

		outFile, err := os.OpenFile(destPath, os.O_CREATE|os.O_WRONLY, os.FileMode(hdr.Mode))
		if err != nil {
			return err
		}

		_, err = io.Copy(outFile, tr)
		outFile.Close()
		if err != nil {
			return err
		}
	}

	if err := os.RemoveAll(targetDir); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove old target: %w", err)
	}

	if err := renameOrCopy(tmpDir, targetDir); err != nil {
		return fmt.Errorf("move to target: %w", err)
	}

	success = true
	return nil
}

func archiveExt(url string) string {
	switch {
	case strings.HasSuffix(url, ".tar.zst"):
		return ".tar.zst"
	case strings.HasSuffix(url, ".tar.gz"):
		return ".tar.gz"
	default:
		return filepath.Ext(url)
	}
}

func extractBinaryFromTgz(tgzPath, binaryName, targetPath string, lb *RingBuffer) error {
	f, err := os.Open(tgzPath)
	if err != nil {
		return err
	}
	defer f.Close()

	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		if filepath.Base(hdr.Name) == binaryName && hdr.Typeflag == tar.TypeReg {
			out, err := os.OpenFile(targetPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0755)
			if err != nil {
				return err
			}
			_, err = io.Copy(out, tr)
			out.Close()
			if err != nil {
				return err
			}
			lb.Write(fmt.Sprintf("Extracted %s from %s", binaryName, hdr.Name))
			return nil
		}
	}
	return fmt.Errorf("%s not found in archive", binaryName)
}

func extractBinaryFromTarZst(tarZstPath, binaryName, targetPath string, lb *RingBuffer) error {
	tmpDir, err := os.MkdirTemp("", "sd-extract-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmpDir)

	cmd := exec.Command("tar", "-I", "zstd", "-xf", tarZstPath, "-C", tmpDir)
	if output, err := cmd.CombinedOutput(); err != nil {
		cmd = exec.Command("tar", "--zstd", "-xf", tarZstPath, "-C", tmpDir)
		if output2, err2 := cmd.CombinedOutput(); err2 != nil {
			return fmt.Errorf("tar extract (tried -I zstd and --zstd): %s; %s: %w", strings.TrimSpace(string(output)), strings.TrimSpace(string(output2)), err2)
		}
	}

	var found string
	filepath.Walk(tmpDir, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil || info.IsDir() {
			return nil
		}
		if filepath.Base(path) == binaryName {
			found = path
		}
		return nil
	})
	if found == "" {
		return fmt.Errorf("%s not found in extracted archive", binaryName)
	}

	if err := copyFile(found, targetPath); err != nil {
		return err
	}
	lb.Write(fmt.Sprintf("Extracted %s from archive", binaryName))
	return nil
}

func extractBinaryFromZip(zipPath, binaryName, targetPath string) error {
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return err
	}
	defer r.Close()

	for _, f := range r.File {
		if filepath.Base(f.Name) == binaryName && !f.FileInfo().IsDir() {
			rc, err := f.Open()
			if err != nil {
				return err
			}
			out, err := os.OpenFile(targetPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0755)
			if err != nil {
				rc.Close()
				return err
			}
			_, err = io.Copy(out, rc)
			out.Close()
			rc.Close()
			return err
		}
	}
	return fmt.Errorf("%s not found in archive", binaryName)
}

type progressWriter struct {
	w        io.Writer
	total    int64
	written  int64
	key      string
	target   string
	lastLog  int64
	lastTime int64
	inst     *Installer
	lb       *RingBuffer
}

func (pw *progressWriter) Write(p []byte) (int, error) {
	n, err := pw.w.Write(p)
	if err != nil {
		return n, err
	}
	pw.written += int64(n)
	now := time.Now().UnixMilli()
	shouldLog := pw.written-pw.lastLog >= 10*1024*1024 || now-pw.lastTime >= 5000
	if shouldLog {
		pw.lastLog = pw.written
		pw.lastTime = now
		pct := ""
		if pw.total > 0 {
			pct = fmt.Sprintf(" (%.0f%%)", float64(pw.written)/float64(pw.total)*100)
		}
		msg := fmt.Sprintf("Downloading %s%s", formatBytes(pw.written), pct)
		pw.inst.setProgress(pw.key, msg)
		pw.lb.Write(msg)
		log.Printf("[%s] %s", pw.key, msg)
	}
	return n, nil
}

func formatBytes(b int64) string {
	const (
		KB = 1024
		MB = KB * 1024
		GB = MB * 1024
	)
	switch {
	case b >= GB:
		return fmt.Sprintf("%.1f GB", float64(b)/float64(GB))
	case b >= MB:
		return fmt.Sprintf("%.1f MB", float64(b)/float64(MB))
	case b >= KB:
		return fmt.Sprintf("%.1f KB", float64(b)/float64(KB))
	default:
		return fmt.Sprintf("%d B", b)
	}
}

func (inst *Installer) installPip(key string, ic InstallConfig, lb *RingBuffer) error {
	inst.setProgress(key, "checking pip")
	pipPath, pipCmd := findPip(lb)
	if pipPath == "" {
		return fmt.Errorf("pip not found — install Python 3 first")
	}

	inst.setProgress(key, "installing "+ic.URL)
	pkg := ic.URL
	if pkg == "" {
		pkg = ic.Target
	}
	lb.Write(fmt.Sprintf("Installing %s via %s", pkg, pipCmd))
	log.Printf("[%s] installing %s via %s", key, pkg, pipCmd)

	cmd := exec.Command(pipPath, "install", pkg)
	output, err := cmd.CombinedOutput()
	if len(output) > 0 {
		for _, line := range strings.Split(string(output), "\n") {
			if line != "" {
				lb.Write(line)
			}
		}
	}
	if err != nil {
		return fmt.Errorf("pip install: %w", err)
	}

	if ic.Target != "" {
		targetPath, err := exec.LookPath(ic.Target)
		if err != nil {
			return fmt.Errorf("installed but %s not found in PATH", ic.Target)
		}
		lb.Write(fmt.Sprintf("Installed: %s", targetPath))
		log.Printf("[%s] installed: %s", key, targetPath)
	}

	return nil
}

func (inst *Installer) checkInstalled(ic InstallConfig) bool {
	switch ic.Method {
	case InstallZip:
		if ic.Target == "" {
			return false
		}
		_, err := os.Stat(ic.Target)
		return err == nil
	case InstallBinary, InstallArchive, InstallTgz:
		if ic.Target == "" {
			return false
		}
		_, err := os.Stat(ic.Target)
		return err == nil
	case InstallPip:
		if ic.Target == "" {
			return false
		}
		_, err := exec.LookPath(ic.Target)
		return err == nil
	}
	return false
}

func (inst *Installer) setProgress(key string, progress string) {
	inst.mu.Lock()
	if s, ok := inst.statuses[key]; ok {
		s.Progress = progress
		inst.statuses[key] = s
	}
	inst.mu.Unlock()
}

func (inst *Installer) handleAllStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	writeJSON(w, inst.Status())
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
		writeError(w, "unknown key: "+key, http.StatusNotFound)
		return
	}
	writeJSON(w, s)
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
		writeError(w, "unknown install target: "+key, http.StatusNotFound)
		return
	}

	go func() {
		if err := inst.Install(key); err != nil {
			log.Printf("Install %s failed: %v", key, err)
		}
	}()

	writeJSON(w, map[string]string{"status": "installing", "key": key})
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
		writeError(w, "unknown key: "+key, http.StatusNotFound)
		return
	}

	lines := 50
	if v := r.URL.Query().Get("lines"); v != "" {
		if n, err := parsePositiveInt(v); err == nil {
			lines = n
		}
	}

	writeJSON(w, map[string]interface{}{
		"key":   key,
		"lines": lines,
		"logs":  lb.Lines(lines),
	})
}

func findPip(lb *RingBuffer) (string, string) {
	candidates := []struct {
		bin  string
		name string
	}{
		{"pip3", "pip3"},
		{"pip", "pip"},
		{"uv", "uv pip"},
	}

	for _, c := range candidates {
		path, err := exec.LookPath(c.bin)
		if err == nil {
			lb.Write(fmt.Sprintf("Found %s at %s", c.name, path))
			if c.bin == "uv" {
				return path, "uv pip"
			}
			return path, c.name
		}
	}

	lb.Write("pip not found, trying get-pip.py fallback")
	pipPath, err := installPipFallback(lb)
	if err != nil {
		lb.Write(fmt.Sprintf("Fallback failed: %v", err))
		return "", ""
	}
	return pipPath, "pip3"
}

func installPipFallback(lb *RingBuffer) (string, error) {
	pythonPath, err := exec.LookPath("python3")
	if err != nil {
		return "", fmt.Errorf("python3 not found")
	}

	resp, err := http.Get("https://bootstrap.pypa.io/get-pip.py")
	if err != nil {
		return "", fmt.Errorf("download get-pip.py: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download get-pip.py: HTTP %d", resp.StatusCode)
	}

	tmpFile, err := os.CreateTemp("", "get-pip-*.py")
	if err != nil {
		return "", fmt.Errorf("create temp file: %w", err)
	}
	tmpPath := tmpFile.Name()
	defer os.Remove(tmpPath)

	if _, err := io.Copy(tmpFile, resp.Body); err != nil {
		tmpFile.Close()
		return "", fmt.Errorf("save get-pip.py: %w", err)
	}
	tmpFile.Close()

	lb.Write("Installing pip via get-pip.py...")
	cmd := exec.Command(pythonPath, tmpPath)
	output, err := cmd.CombinedOutput()
	if len(output) > 0 {
		for _, line := range strings.Split(string(output), "\n") {
			if line != "" {
				lb.Write(line)
			}
		}
	}
	if err != nil {
		return "", fmt.Errorf("run get-pip.py: %w", err)
	}

	return exec.LookPath("pip3")
}

func extractZip(zipPath, targetDir string) error {
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return err
	}
	defer r.Close()

	tmpDir, err := os.MkdirTemp("", "sd-extract-*")
	if err != nil {
		return err
	}

	success := false
	defer func() {
		if !success {
			os.RemoveAll(tmpDir)
		}
	}()

	for _, f := range r.File {
		if f.Name == "" {
			continue
		}

		parts := strings.SplitN(f.Name, "/", 2)
		if len(parts) < 2 || parts[1] == "" {
			continue
		}
		relPath := parts[1]

		destPath := filepath.Join(tmpDir, relPath)

		if f.FileInfo().IsDir() {
			os.MkdirAll(destPath, 0755)
			continue
		}

		if err := os.MkdirAll(filepath.Dir(destPath), 0755); err != nil {
			return err
		}

		rc, err := f.Open()
		if err != nil {
			return err
		}

		outFile, err := os.OpenFile(destPath, os.O_CREATE|os.O_WRONLY, f.Mode())
		if err != nil {
			rc.Close()
			return err
		}

		_, err = io.Copy(outFile, rc)
		outFile.Close()
		rc.Close()
		if err != nil {
			return err
		}
	}

	if err := os.RemoveAll(targetDir); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove old target: %w", err)
	}

	if err := renameOrCopy(tmpDir, targetDir); err != nil {
		return fmt.Errorf("move to target: %w", err)
	}

	success = true
	return nil
}

func renameOrCopy(src, dst string) error {
	if err := os.Rename(src, dst); err == nil {
		return nil
	}

	if err := os.MkdirAll(dst, 0755); err != nil {
		return err
	}

	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}

	for _, entry := range entries {
		srcPath := filepath.Join(src, entry.Name())
		dstPath := filepath.Join(dst, entry.Name())

		if entry.IsDir() {
			if err := renameOrCopy(srcPath, dstPath); err != nil {
				return err
			}
		} else {
			if err := copyFile(srcPath, dstPath); err != nil {
				return err
			}
		}
	}

	return os.RemoveAll(src)
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	info, err := in.Stat()
	if err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		return err
	}

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, info.Mode())
	if err != nil {
		return err
	}

	_, err = io.Copy(out, in)
	out.Close()
	return err
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
