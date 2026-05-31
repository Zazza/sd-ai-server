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

	"sd-studio-server/process"
)

const (
	gitVersion = "2.47.1"
	gitWinTag  = "v2.47.1.windows.1"
)

func mingitURL() string {
	arch := "64-bit"
	if runtime.GOARCH == "arm64" {
		arch = "arm64"
	}
	return fmt.Sprintf(
		"https://github.com/git-for-windows/git/releases/download/%s/MinGit-%s-%s.zip",
		gitWinTag, gitVersion, arch,
	)
}

func GitBinPath(dataDir string) string {
	if runtime.GOOS == "windows" {
		return filepath.Join(dataDir, "git", "cmd", "git.exe")
	}
	return filepath.Join(dataDir, "bin", "git")
}

func GitAvailable(dataDir string) bool {
	if _, err := os.Stat(GitBinPath(dataDir)); err == nil {
		return true
	}
	if _, err := exec.LookPath("git"); err == nil {
		return true
	}
	return false
}

func (inst *Installer) EnsureGit() error {
	if GitAvailable(inst.config.DataDir) {
		return nil
	}

	lb := process.NewRingBuffer(50)
	defer func() {
		for _, line := range lb.Lines(50) {
			if line != "" {
				log.Printf("[git] %s", line)
			}
		}
	}()

	lb.Write("git not found, installing...")

	var err error
	switch runtime.GOOS {
	case "windows":
		err = inst.installGitWindows(lb)
	case "linux":
		err = inst.installGitLinux(lb)
	case "darwin":
		err = inst.installGitDarwin(lb)
	default:
		err = fmt.Errorf("unsupported platform: %s", runtime.GOOS)
	}

	if err != nil {
		lb.Write(fmt.Sprintf("git installation failed: %v", err))
	}
	return err
}

func (inst *Installer) installGitWindows(lb *process.RingBuffer) error {
	target := filepath.Join(inst.config.DataDir, "git")
	gitExe := filepath.Join(target, "cmd", "git.exe")

	if _, err := os.Stat(gitExe); err == nil {
		lb.Write("MinGit already installed")
		return nil
	}

	url := mingitURL()
	lb.Write(fmt.Sprintf("Downloading MinGit %s...", gitVersion))
	resp, err := http.Get(url)
	if err != nil {
		return fmt.Errorf("download mingit: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download mingit: HTTP %d", resp.StatusCode)
	}

	tmpFile, err := os.CreateTemp("", "mingit-*.zip")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpPath := tmpFile.Name()
	defer os.Remove(tmpPath)

	size, err := io.Copy(tmpFile, resp.Body)
	tmpFile.Close()
	if err != nil {
		return fmt.Errorf("save mingit: %w", err)
	}
	lb.Write(fmt.Sprintf("Downloaded %s", FormatBytes(size)))

	if err := os.MkdirAll(target, 0755); err != nil {
		return fmt.Errorf("create git dir: %w", err)
	}

	lb.Write("Extracting MinGit...")
	if err := extractZip(tmpPath, target); err != nil {
		os.RemoveAll(target)
		return fmt.Errorf("extract mingit: %w", err)
	}

	if _, err := os.Stat(gitExe); err != nil {
		os.RemoveAll(target)
		return fmt.Errorf("git.exe not found after extraction")
	}

	lb.Write("MinGit installed successfully")
	return nil
}

func (inst *Installer) installGitLinux(lb *process.RingBuffer) error {
	managers := []struct {
		cmd  string
		args []string
	}{
		{"apt-get", []string{"install", "-y", "git"}},
		{"dnf", []string{"install", "-y", "git"}},
		{"yum", []string{"install", "-y", "git"}},
		{"apk", []string{"add", "git"}},
		{"pacman", []string{"-S", "--noconfirm", "git"}},
	}

	for _, pm := range managers {
		if _, err := exec.LookPath(pm.cmd); err != nil {
			continue
		}
		lb.Write(fmt.Sprintf("Installing git via %s...", pm.cmd))
		output, err := exec.Command(pm.cmd, pm.args...).CombinedOutput()
		if err != nil {
			lb.Write(fmt.Sprintf("%s failed: %v: %s", pm.cmd, err, string(output)))
			continue
		}
		if GitAvailable(inst.config.DataDir) {
			lb.Write("git installed successfully")
			return nil
		}
	}

	return fmt.Errorf("install git: no package manager succeeded, install git manually")
}

func (inst *Installer) installGitDarwin(lb *process.RingBuffer) error {
	if err := exec.Command("xcode-select", "-p").Run(); err != nil {
		lb.Write("Installing Xcode command line tools...")
		_ = exec.Command("xcode-select", "--install").Run()
	}
	if GitAvailable(inst.config.DataDir) {
		lb.Write("git available via Xcode tools")
		return nil
	}
	return fmt.Errorf("git not available: install Xcode Command Line Tools manually")
}
