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

func findOllamaBinary() string {
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
	if p := findOllamaBinary(); p != "" {
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

	p := findOllamaBinary()
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
		if findOllamaBinary() != "" {
			break
		}
		time.Sleep(2 * time.Second)
	}

	lb.Write("Ollama installed successfully")
	return nil
}

func (inst *Installer) installOllamaLinux(lb *process.RingBuffer) error {
	lb.Write("Downloading Ollama install script...")
	log.Printf("[ollama] downloading install script")

	resp, err := http.Get("https://ollama.com/install.sh")
	if err != nil {
		return fmt.Errorf("download script: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download failed: HTTP %d", resp.StatusCode)
	}

	script, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read script: %w", err)
	}

	tmpFile, err := os.CreateTemp("", "ollama-install-*.sh")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpPath := tmpFile.Name()
	defer os.Remove(tmpPath)

	tmpFile.Write(script)
	tmpFile.Close()
	os.Chmod(tmpPath, 0755)

	lb.Write("Running Ollama installer...")
	log.Printf("[ollama] running install script")

	cmd := exec.Command("sh", tmpPath)
	output, err := cmd.CombinedOutput()
	if len(output) > 0 {
		for _, line := range strings.Split(string(output), "\n") {
			if line != "" {
				lb.Write(line)
			}
		}
	}
	if err != nil {
		return fmt.Errorf("install script failed: %w", err)
	}

	lb.Write("Ollama installed successfully")
	return nil
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

	lb.Write("Homebrew not found, downloading install script...")
	return inst.installOllamaLinux(lb)
}
