package installer

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"sd-studio-server/process"
)

func findOllamaBinary(dataDir string) string {
	if dataDir != "" {
		binName := "ollama"
		if runtime.GOOS == "windows" {
			binName = "ollama.exe"
		}
		p := filepath.Join(dataDir, "ollama", binName)
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	if runtime.GOOS == "windows" {
		localAppData := os.Getenv("LOCALAPPDATA")
		if localAppData != "" {
			path := filepath.Join(localAppData, "Programs", "Ollama", "ollama.exe")
			if _, err := os.Stat(path); err == nil {
				return path
			}
		}
		home, _ := os.UserHomeDir()
		if home != "" {
			path := filepath.Join(home, "AppData", "Local", "Programs", "Ollama", "ollama.exe")
			if _, err := os.Stat(path); err == nil {
				return path
			}
		}
	}
	if runtime.GOOS == "linux" {
		for _, p := range []string{"/usr/local/bin/ollama", "/usr/bin/ollama"} {
			if _, err := os.Stat(p); err == nil {
				return p
			}
		}
	}
	if runtime.GOOS == "darwin" {
		for _, p := range []string{"/usr/local/bin/ollama", "/opt/homebrew/bin/ollama"} {
			if _, err := os.Stat(p); err == nil {
				return p
			}
		}
	}
	if p, err := exec.LookPath("ollama"); err == nil {
		return p
	}
	return ""
}

func (inst *Installer) EnsureOllama() string {
	if p := findOllamaBinary(inst.config.DataDir); p != "" {
		log.Printf("[ollama] found at %s", p)
		return p
	}

	log.Printf("[ollama] not found, installing...")
	lb := process.NewRingBuffer(50)

	if err := inst.installOllamaSystem(lb); err != nil {
		for _, line := range lb.Lines(50) {
			if line != "" {
				log.Printf("[ollama-install] %s", line)
			}
		}
		log.Printf("[ollama] install failed: %v", err)
		return ""
	}

	for _, line := range lb.Lines(50) {
		if line != "" {
			log.Printf("[ollama-install] %s", line)
		}
	}

	p := findOllamaBinary(inst.config.DataDir)
	if p != "" {
		log.Printf("[ollama] installed at %s", p)
	}
	return p
}

func (inst *Installer) installOllamaSystem(lb *process.RingBuffer) error {
	switch runtime.GOOS {
	case "windows":
		return inst.installOllamaWindows(lb)
	case "linux":
		return inst.installOllamaLinux(lb)
	case "darwin":
		return inst.installOllamaMac(lb)
	}
	return fmt.Errorf("unsupported platform for Ollama auto-install")
}

func (inst *Installer) installOllamaWindows(lb *process.RingBuffer) error {
	setupURL := "https://ollama.com/download/OllamaSetup.exe"

	lb.Write("Downloading Ollama installer...")
	log.Printf("[ollama] downloading %s", setupURL)

	resp, err := http.Get(setupURL)
	if err != nil {
		return fmt.Errorf("download installer: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download failed: HTTP %d", resp.StatusCode)
	}

	tmpFile, err := os.CreateTemp("", "ollama-setup-*.exe")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpPath := tmpFile.Name()
	defer os.Remove(tmpPath)

	size, err := io.Copy(tmpFile, resp.Body)
	tmpFile.Close()
	if err != nil {
		return fmt.Errorf("save installer: %w", err)
	}

	lb.Write(fmt.Sprintf("Downloaded %s, installing silently...", FormatBytes(size)))
	log.Printf("[ollama] running silent install (%s downloaded)", FormatBytes(size))

	cmd := exec.Command(tmpPath, "/S")
	output, err := cmd.CombinedOutput()
	if len(output) > 0 {
		for _, line := range strings.Split(string(output), "\n") {
			if line != "" {
				lb.Write(line)
			}
		}
	}
	if err != nil {
		return fmt.Errorf("installer failed: %w", err)
	}

	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		if findOllamaBinary(inst.config.DataDir) != "" {
			break
		}
		time.Sleep(2 * time.Second)
	}

	lb.Write("Ollama installed successfully")
	return nil
}

func (inst *Installer) installOllamaLinux(lb *process.RingBuffer) error {
	return inst.downloadOllamaBinary(lb, "linux")
}

func (inst *Installer) installOllamaMac(lb *process.RingBuffer) error {
	if brew, err := exec.LookPath("brew"); err == nil {
		lb.Write("Installing Ollama via Homebrew...")
		log.Printf("[ollama] using Homebrew at %s", brew)

		cmd := exec.Command(brew, "install", "ollama")
		output, err := cmd.CombinedOutput()
		if len(output) > 0 {
			for _, line := range strings.Split(string(output), "\n") {
				if line != "" {
					lb.Write(line)
				}
			}
		}
		if err != nil {
			return fmt.Errorf("brew install ollama: %w", err)
		}

		lb.Write("Ollama installed via Homebrew")
		return nil
	}

	lb.Write("Homebrew not found, downloading binary...")
	return inst.downloadOllamaBinary(lb, "darwin")
}

func (inst *Installer) downloadOllamaBinary(lb *process.RingBuffer, goos string) error {
	arch := runtime.GOARCH
	if arch != "arm64" {
		arch = "amd64"
	}

	ext := ".tgz"
	if goos == "linux" {
		ext = ".tar.zst"
	}
	url := fmt.Sprintf("https://ollama.com/download/ollama-%s-%s%s", goos, arch, ext)
	target := filepath.Join(inst.config.DataDir, "ollama", "ollama")
	if runtime.GOOS == "windows" {
		target += ".exe"
	}

	inst.setProgress("ollama", "downloading")
	lb.Write(fmt.Sprintf("Downloading Ollama (%s/%s)...", goos, arch))
	log.Printf("[ollama] downloading %s -> %s", url, target)

	resp, err := http.Get(url)
	if err != nil {
		return fmt.Errorf("download: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download failed: HTTP %d", resp.StatusCode)
	}

	tmpFile, err := os.CreateTemp("", "ollama-download-*"+ext)
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpPath := tmpFile.Name()
	defer os.Remove(tmpPath)

	pw := &progressWriter{
		w:        tmpFile,
		total:    resp.ContentLength,
		key:      "ollama",
		target:   target,
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

	inst.setProgress("ollama", "extracting")
	lb.Write(fmt.Sprintf("Downloaded %s, extracting...", FormatBytes(size)))

	targetDir := filepath.Dir(target)
	if err := os.MkdirAll(targetDir, 0755); err != nil {
		return fmt.Errorf("create target dir: %w", err)
	}

	switch {
	case strings.HasSuffix(ext, ".tar.zst"):
		err = extractBinaryFromTarZst(tmpPath, "ollama", target, lb)
	case strings.HasSuffix(ext, ".tgz"):
		err = extractBinaryFromTgz(tmpPath, "ollama", target, lb)
	default:
		return fmt.Errorf("unsupported archive format: %s", ext)
	}
	if err != nil {
		return fmt.Errorf("extract: %w", err)
	}

	if err := os.Chmod(target, 0755); err != nil {
		return fmt.Errorf("chmod: %w", err)
	}

	inst.setProgress("ollama", "done")
	lb.Write("Ollama installed successfully")
	log.Printf("[ollama] installed to %s", target)
	return nil
}
