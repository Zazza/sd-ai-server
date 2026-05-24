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
	"strconv"
	"strings"

	"sd-studio-server/process"
)

func pythonBinPath(dataDir string) string {
	if runtime.GOOS == "windows" {
		return filepath.Join(dataDir, "python", "python.exe")
	}
	return filepath.Join(dataDir, "python", "bin", "python3")
}

func pythonScriptsDir(dataDir string) string {
	if runtime.GOOS == "windows" {
		return filepath.Join(dataDir, "python", "Scripts")
	}
	return filepath.Join(dataDir, "python", "bin")
}

func pipBinPath(dataDir, name string) string {
	if runtime.GOOS == "windows" {
		return filepath.Join(dataDir, "python", "Scripts", name+".exe")
	}
	return filepath.Join(dataDir, "python", "bin", name)
}

func venvScriptsDir(venvDir string) string {
	if runtime.GOOS == "windows" {
		return filepath.Join(venvDir, "Scripts")
	}
	return filepath.Join(venvDir, "bin")
}

func findPipForDataDir(dataDir string, lb *process.RingBuffer) (string, string) {
	candidates := []struct {
		bin  string
		name string
	}{
		{pipBinPath(dataDir, "pip3"), "bundled pip3"},
		{pipBinPath(dataDir, "pip"), "bundled pip"},
		{"pip3", "pip3"},
		{"pip", "pip"},
		{"uv", "uv pip"},
	}

	for _, c := range candidates {
		if strings.ContainsRune(c.bin, os.PathSeparator) {
			if _, err := os.Stat(c.bin); err == nil {
				lb.Write(fmt.Sprintf("Found %s at %s", c.name, c.bin))
				return c.bin, c.name
			}
		} else {
			path, err := exec.LookPath(c.bin)
			if err == nil {
				lb.Write(fmt.Sprintf("Found %s at %s", c.name, path))
				if c.bin == "uv" {
					return path, "uv pip"
				}
				return path, c.name
			}
		}
	}

	lb.Write("pip not found, trying get-pip.py fallback")
	pipPath, err := installPipFallbackForDataDir(dataDir, lb)
	if err != nil {
		lb.Write(fmt.Sprintf("Fallback failed: %v", err))
		return "", ""
	}
	return pipPath, "pip3"
}

func installPipFallbackForDataDir(dataDir string, lb *process.RingBuffer) (string, error) {
	bundledPython := pythonBinPath(dataDir)
	var pythonPath string
	if _, err := os.Stat(bundledPython); err == nil {
		pythonPath = bundledPython
		lb.Write(fmt.Sprintf("Using bundled Python: %s", bundledPython))
	} else {
		path, err := exec.LookPath("python3")
		if err != nil {
			return "", fmt.Errorf("python3 not found")
		}
		pythonPath = path
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

func ensurePipAndSetuptools(pythonDir string, lb *process.RingBuffer) {
	pythonExe := filepath.Join(pythonDir, "bin", "python3")
	if runtime.GOOS == "windows" {
		pythonExe = filepath.Join(pythonDir, "python.exe")
	}
	if _, err := os.Stat(pythonExe); err != nil {
		lb.Write(fmt.Sprintf("python not found at %s, skipping pip/setuptools setup", pythonExe))
		return
	}

	cmd := exec.Command(pythonExe, "-c", "import pkg_resources")
	if err := cmd.Run(); err == nil {
		lb.Write("setuptools already installed")
		return
	}

	lb.Write("Installing pip and setuptools...")
	cmd = exec.Command(pythonExe, "-m", "ensurepip", "--upgrade")
	if output, err := cmd.CombinedOutput(); err != nil {
		lb.Write(fmt.Sprintf("ensurepip failed: %v: %s", err, string(output)))
	} else {
		lb.Write("pip installed via ensurepip")
	}

	cmd = exec.Command(pythonExe, "-m", "pip", "install", "--upgrade", "pip", "setuptools<78", "wheel")
	if output, err := cmd.CombinedOutput(); err != nil {
		lb.Write(fmt.Sprintf("pip/setuptools upgrade warning: %v: %s", err, string(output)))
	} else {
		lb.Write("pip, setuptools, wheel upgraded")
	}
}

func createPythonSymlinks(pythonDir string, lb *process.RingBuffer) {
	if runtime.GOOS == "windows" {
		return
	}
	binDir := filepath.Join(pythonDir, "bin")
	entries, err := os.ReadDir(binDir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasPrefix(name, "python3.") && !entry.IsDir() {
			linkName := strings.SplitN(name, ".", 2)[0]
			linkPath := filepath.Join(binDir, linkName)
			if _, err := os.Lstat(linkPath); err == nil {
				continue
			}
			if err := os.Symlink(name, linkPath); err != nil {
				lb.Write(fmt.Sprintf("symlink %s -> %s failed: %v", linkName, name, err))
			} else {
				lb.Write(fmt.Sprintf("symlink %s -> %s", linkName, name))
			}
		}
		if strings.HasPrefix(name, "pip3.") && !entry.IsDir() {
			linkName := strings.SplitN(name, ".", 2)[0]
			linkPath := filepath.Join(binDir, linkName)
			if _, err := os.Lstat(linkPath); err == nil {
				continue
			}
			if err := os.Symlink(name, linkPath); err != nil {
				lb.Write(fmt.Sprintf("symlink %s -> %s failed: %v", linkName, name, err))
			} else {
				lb.Write(fmt.Sprintf("symlink %s -> %s", linkName, name))
			}
		}
	}
}

func ensureForgeVenv(dataDir, forgeDir string, lb *process.RingBuffer) {
	venvDir := filepath.Join(forgeDir, "venv")
	activatePath := filepath.Join(venvScriptsDir(venvDir), "activate")
	if _, err := os.Stat(activatePath); err == nil {
		lb.Write("venv already exists, skipping")
		return
	}

	pythonBin := pythonBinPath(dataDir)
	if _, err := os.Stat(pythonBin); err != nil {
		lb.Write(fmt.Sprintf("python not found at %s, skipping venv creation", pythonBin))
		return
	}

	lb.Write("Creating venv for Forge...")
	cmd := exec.Command(pythonBin, "-m", "venv", venvDir)
	cmd.Dir = forgeDir
	if output, err := cmd.CombinedOutput(); err != nil {
		lb.Write(fmt.Sprintf("venv creation failed: %v: %s", err, string(output)))
		return
	}

	pipBin := filepath.Join(venvScriptsDir(venvDir), "pip")
	if runtime.GOOS == "windows" {
		pipBin = filepath.Join(venvScriptsDir(venvDir), "pip.exe")
	}
	lb.Write("Upgrading pip in venv...")
	cmd = exec.Command(pipBin, "install", "--upgrade", "pip", "setuptools<78")
	cmd.Dir = forgeDir
	if _, err := cmd.CombinedOutput(); err != nil {
		lb.Write(fmt.Sprintf("pip upgrade warning: %v", err))
	}

	lb.Write("venv created successfully")
}

func preInstallForgeDeps(dataDir, forgeDir string, lb *process.RingBuffer) {
	pythonExe := pythonBinPath(dataDir)
	if _, err := os.Stat(pythonExe); err != nil {
		lb.Write("python not found, skipping forge deps pre-install")
		return
	}

	cmd := exec.Command(pythonExe, "-c", "import torch; assert torch.cuda.is_available()")
	if err := cmd.Run(); err != nil {
		label, indexURL := detectTorchIndex()
		args := []string{"-m", "pip", "install", "torch", "torchvision", "torchaudio"}
		if indexURL != "" {
			args = append(args, "--index-url", indexURL)
		}
		lb.Write(fmt.Sprintf("Installing PyTorch (%s)...", label))
		cmd = exec.Command(pythonExe, args...)
		if output, err := cmd.CombinedOutput(); err != nil {
			lb.Write(fmt.Sprintf("PyTorch install failed: %v: %s", err, string(output)))
		} else {
			lb.Write("PyTorch installed successfully")
		}
	} else {
		lb.Write("PyTorch CUDA already available")
	}

	cmd = exec.Command(pythonExe, "-c", "import clip")
	if err := cmd.Run(); err == nil {
		lb.Write("CLIP already installed")
		return
	}

	lb.Write("Ensuring setuptools<78 for CLIP build...")
	cmd = exec.Command(pythonExe, "-m", "pip", "install", "setuptools<78", "--quiet")
	if output, err := cmd.CombinedOutput(); err != nil {
		lb.Write(fmt.Sprintf("setuptools pin warning: %v: %s", err, string(output)))
	}

	clipURL := "https://github.com/openai/CLIP/archive/d50d76daa670286dd6cacf3bcd80b5e4823fc8e1.zip"
	lb.Write("Pre-installing CLIP (no build isolation)...")
	cmd = exec.Command(pythonExe, "-m", "pip", "install", clipURL, "--prefer-binary", "--no-build-isolation")
	cmd.Dir = forgeDir
	if output, err := cmd.CombinedOutput(); err != nil {
		lb.Write(fmt.Sprintf("CLIP install failed: %v: %s", err, string(output)))
	} else {
		lb.Write("CLIP installed successfully")
	}
}

func detectTorchIndex() (label, indexURL string) {
	nvidiaSmi := "nvidia-smi"
	if runtime.GOOS == "windows" {
		if p := os.Getenv("ProgramFiles"); p != "" {
			candidate := p + `\NVIDIA Corporation\NVSMI\nvidia-smi.exe`
			if _, err := os.Stat(candidate); err == nil {
				nvidiaSmi = candidate
			}
		}
	}

	output, err := exec.Command(nvidiaSmi, "--query-gpu=compute_cap", "--format=csv,noheader,nounits").Output()
	if err != nil {
		return "CPU", ""
	}

	capStr := strings.TrimSpace(string(output))
	capStr = strings.SplitN(capStr, "\n", 2)[0]
	capStr = strings.TrimSpace(capStr)
	major, _ := strconv.Atoi(strings.SplitN(capStr, ".", 2)[0])

	switch {
	case major >= 8:
		return "CUDA 12.4", "https://download.pytorch.org/whl/cu124"
	case major >= 7:
		return "CUDA 12.1", "https://download.pytorch.org/whl/cu121"
	case major >= 6:
		return "CUDA 11.8", "https://download.pytorch.org/whl/cu118"
	default:
		return "CPU", ""
	}
}

func (inst *Installer) PreStartForge() {
	forgeDir := filepath.Join(inst.config.DataDir, "stable-diffusion-webui-forge")
	if _, err := os.Stat(forgeDir); err != nil {
		return
	}
	lb := process.NewRingBuffer(50)
	preInstallForgeDeps(inst.config.DataDir, forgeDir, lb)
	for _, line := range lb.Lines(50) {
		if line != "" {
			log.Printf("[forge-prestart] %s", line)
		}
	}
}

func (inst *Installer) EnsurePythonBasePackages() {
	pythonDir := filepath.Join(inst.config.DataDir, "python")
	pythonExe := pythonBinPath(inst.config.DataDir)
	if _, err := os.Stat(pythonExe); err != nil {
		return
	}
	lb := process.NewRingBuffer(50)
	ensurePipAndSetuptools(pythonDir, lb)
	for _, line := range lb.Lines(50) {
		if line != "" {
			log.Printf("[python] %s", line)
		}
	}

	forgeDir := filepath.Join(inst.config.DataDir, "stable-diffusion-webui-forge")
	if _, err := os.Stat(forgeDir); err == nil {
		lb = process.NewRingBuffer(50)
		preInstallForgeDeps(inst.config.DataDir, forgeDir, lb)
		for _, line := range lb.Lines(50) {
			if line != "" {
				log.Printf("[forge-deps] %s", line)
			}
		}
	}
}
