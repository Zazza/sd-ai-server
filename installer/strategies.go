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

	"sd-studio-server/config"
	"sd-studio-server/process"
)

func (inst *Installer) installZip(key string, ic config.InstallConfig, lb *process.RingBuffer) error {
	target := inst.resolveTarget(ic.Target)
	inst.setProgress(key, "downloading")
	lb.Write(fmt.Sprintf("Downloading %s -> %s", ic.URL, target))
	log.Printf("[%s] downloading %s -> %s", key, ic.URL, target)

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
	lb.Write(fmt.Sprintf("Downloaded %s", FormatBytes(size)))
	log.Printf("[%s] downloaded %s", key, FormatBytes(size))

	inst.setProgress(key, "extracting")
	lb.Write(fmt.Sprintf("Extracting to %s", target))
	log.Printf("[%s] extracting to %s", key, target)

	if err := extractZip(tmpPath, target); err != nil {
		return fmt.Errorf("extract: %w", err)
	}

	if key == "forge" {
		ensureForgeVenv(inst.config.DataDir, target, lb)
		preInstallForgeDeps(inst.config.DataDir, target, lb)
	}

	lb.Write("Installation complete")
	log.Printf("[%s] installed to %s", key, target)
	return nil
}

func (inst *Installer) installBinary(key string, ic config.InstallConfig, lb *process.RingBuffer) error {
	url := ic.URL
	if strings.Contains(url, "{os}") || strings.Contains(url, "{arch}") {
		url = strings.ReplaceAll(url, "{os}", runtime.GOOS)
		url = strings.ReplaceAll(url, "{arch}", runtime.GOARCH)
	}

	target := inst.resolveTarget(ic.Target)
	inst.setProgress(key, "downloading")
	lb.Write(fmt.Sprintf("Downloading %s -> %s", url, target))
	log.Printf("[%s] downloading %s -> %s", key, url, target)

	resp, err := http.Get(url)
	if err != nil {
		return fmt.Errorf("download: %w", err)
	}
	defer resp.Body.Close()

	log.Printf("[%s] HTTP %d, Content-Length: %s", key, resp.StatusCode, FormatBytes(resp.ContentLength))
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download failed: HTTP %d", resp.StatusCode)
	}

	targetDir := filepath.Dir(target)
	if err := os.MkdirAll(targetDir, 0755); err != nil {
		return fmt.Errorf("create target dir: %w", err)
	}

	f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0755)
	if err != nil {
		return fmt.Errorf("create binary: %w", err)
	}

	totalSize := resp.ContentLength
	pw := &progressWriter{
		w:        f,
		total:    totalSize,
		key:      key,
		target:   target,
		lastLog:  0,
		lastTime: 0,
		inst:     inst,
		lb:       lb,
	}
	size, err := io.Copy(pw, resp.Body)
	f.Close()
	if err != nil {
		os.Remove(target)
		return fmt.Errorf("save binary: %w", err)
	}

	if err := os.Chmod(target, 0755); err != nil {
		return fmt.Errorf("chmod: %w", err)
	}

	lb.Write(fmt.Sprintf("Installed binary %s (%s)", target, FormatBytes(size)))
	log.Printf("[%s] installed binary %s (%s)", key, target, FormatBytes(size))
	return nil
}

func (inst *Installer) installArchive(key string, ic config.InstallConfig, lb *process.RingBuffer) error {
	url := ic.URL
	if strings.Contains(url, "{os}") || strings.Contains(url, "{arch}") {
		url = strings.ReplaceAll(url, "{os}", runtime.GOOS)
		url = strings.ReplaceAll(url, "{arch}", runtime.GOARCH)
	}

	target := inst.resolveTarget(ic.Target)
	inst.setProgress(key, "downloading")
	lb.Write(fmt.Sprintf("Downloading %s -> %s", url, target))
	log.Printf("[%s] downloading %s -> %s", key, url, target)

	resp, err := http.Get(url)
	if err != nil {
		return fmt.Errorf("download: %w", err)
	}
	defer resp.Body.Close()

	log.Printf("[%s] HTTP %d, Content-Length: %s", key, resp.StatusCode, FormatBytes(resp.ContentLength))
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
	lb.Write(fmt.Sprintf("Downloaded %s", FormatBytes(size)))
	log.Printf("[%s] downloaded %s", key, FormatBytes(size))

	targetDir := filepath.Dir(target)
	if err := os.MkdirAll(targetDir, 0755); err != nil {
		return fmt.Errorf("create target dir: %w", err)
	}

	binaryName := filepath.Base(target)
	targetPath := target

	if runtime.GOOS == "windows" && !strings.HasSuffix(binaryName, ".exe") {
		binaryName += ".exe"
		targetPath += ".exe"
	}

	inst.setProgress(key, "extracting")
	lb.Write(fmt.Sprintf("Extracting %s from archive", binaryName))
	log.Printf("[%s] extracting %s from archive", key, binaryName)

	switch {
	case strings.HasSuffix(url, ".tgz") || strings.HasSuffix(url, ".tar.gz"):
		err = extractBinaryFromTgz(tmpPath, binaryName, targetPath, lb)
	case strings.HasSuffix(url, ".tar.zst"):
		err = extractBinaryFromTarZst(tmpPath, binaryName, targetPath, lb)
	case strings.HasSuffix(url, ".zip"):
		err = extractBinaryFromZip(tmpPath, binaryName, targetPath)
	default:
		return fmt.Errorf("unsupported archive format: %s", url)
	}
	if err != nil {
		return fmt.Errorf("extract: %w", err)
	}

	if err := os.Chmod(targetPath, 0755); err != nil {
		return fmt.Errorf("chmod: %w", err)
	}

	lb.Write(fmt.Sprintf("Installed %s (%s)", targetPath, FormatBytes(size)))
	log.Printf("[%s] installed %s (%s)", key, targetPath, FormatBytes(size))
	return nil
}

func (inst *Installer) installTgz(key string, ic config.InstallConfig, lb *process.RingBuffer) error {
	target := inst.resolveTarget(ic.Target)
	inst.setProgress(key, "downloading")
	lb.Write(fmt.Sprintf("Downloading %s -> %s", ic.URL, target))
	log.Printf("[%s] downloading %s -> %s", key, ic.URL, target)

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
	lb.Write(fmt.Sprintf("Downloaded %s", FormatBytes(size)))
	log.Printf("[%s] downloaded %s", key, FormatBytes(size))

	inst.setProgress(key, "extracting")
	lb.Write(fmt.Sprintf("Extracting to %s", target))
	log.Printf("[%s] extracting to %s", key, target)

	if err := extractTgz(tmpPath, target); err != nil {
		return fmt.Errorf("extract: %w", err)
	}

	if key == "python" {
		createPythonSymlinks(target, lb)
		ensurePipAndSetuptools(target, lb)
	}

	if key == "forge" {
		ensureForgeVenv(inst.config.DataDir, target, lb)
		preInstallForgeDeps(inst.config.DataDir, target, lb)
	}

	lb.Write("Installation complete")
	log.Printf("[%s] installed to %s", key, target)
	return nil
}

func (inst *Installer) installPip(key string, ic config.InstallConfig, lb *process.RingBuffer) error {
	inst.setProgress(key, "checking pip")
	pipPath, pipCmd := findPipForDataDir(inst.config.DataDir, lb)
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

	pythonPath := pythonBinPath(inst.config.DataDir)
	var cmd *exec.Cmd
	if _, err := os.Stat(pythonPath); err == nil {
		cmd = exec.Command(pythonPath, "-m", "pip", "install", pkg)
	} else {
		cmd = exec.Command(pipPath, "install", pkg)
	}
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
		targetPath := process.FindBinary(inst.config.DataDir, ic.Target)
		if targetPath == "" {
			return fmt.Errorf("installed but %s not found", ic.Target)
		}
		lb.Write(fmt.Sprintf("Installed: %s", targetPath))
		log.Printf("[%s] installed: %s", key, targetPath)
	}

	return nil
}
