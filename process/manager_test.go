package process

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"sd-studio-server/config"
)

func TestToolPathDirs_emptyDataDir(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()

	pm := &ProcessManager{
		processes: make(map[string]*ManagedProcess),
		dataDir:   tmpDir,
	}

	dirs := pm.toolPathDirs()

	if len(dirs) != 0 {
		t.Errorf("toolPathDirs() on empty dir returned %d entries, want 0: %v", len(dirs), dirs)
	}
}

func TestToolPathDirs_withBinDir(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	binDir := filepath.Join(tmpDir, "bin")
	if err := os.MkdirAll(binDir, 0755); err != nil {
		t.Fatalf("failed to create bin dir: %v", err)
	}

	pm := &ProcessManager{
		processes: make(map[string]*ManagedProcess),
		dataDir:   tmpDir,
	}

	dirs := pm.toolPathDirs()

	found := false
	for _, d := range dirs {
		if d == binDir {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("toolPathDirs() = %v, want to contain %q", dirs, binDir)
	}
}

func TestToolPathDirs_withPythonDir(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()

	pythonBinDir := filepath.Join(tmpDir, "python", "bin")
	if runtime.GOOS == "windows" {
		pythonBinDir = filepath.Join(tmpDir, "python", "Scripts")
	}
	if err := os.MkdirAll(pythonBinDir, 0755); err != nil {
		t.Fatalf("failed to create python bin dir: %v", err)
	}

	pm := &ProcessManager{
		processes: make(map[string]*ManagedProcess),
		dataDir:   tmpDir,
	}

	dirs := pm.toolPathDirs()

	found := false
	for _, d := range dirs {
		if d == pythonBinDir {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("toolPathDirs() = %v, want to contain %q", dirs, pythonBinDir)
	}
}

func TestToolPathDirs_withGitCmdDir(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()

	gitCmdDir := filepath.Join(tmpDir, "git", "cmd")
	if err := os.MkdirAll(gitCmdDir, 0755); err != nil {
		t.Fatalf("failed to create git cmd dir: %v", err)
	}

	pm := &ProcessManager{
		processes: make(map[string]*ManagedProcess),
		dataDir:   tmpDir,
	}

	dirs := pm.toolPathDirs()

	if runtime.GOOS == "windows" {
		found := false
		for _, d := range dirs {
			if d == gitCmdDir {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("toolPathDirs() on windows = %v, want to contain %q", dirs, gitCmdDir)
		}
	} else {
		for _, d := range dirs {
			if d == gitCmdDir {
				t.Errorf("toolPathDirs() on non-windows should not contain git cmd dir, got %v", dirs)
				break
			}
		}
	}
}

func TestToolPathDirs_allDirs(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()

	pythonBinDir := filepath.Join(tmpDir, "python", "bin")
	if runtime.GOOS == "windows" {
		pythonBinDir = filepath.Join(tmpDir, "python", "Scripts")
	}
	binDir := filepath.Join(tmpDir, "bin")
	gitCmdDir := filepath.Join(tmpDir, "git", "cmd")

	for _, d := range []string{pythonBinDir, binDir, gitCmdDir} {
		if err := os.MkdirAll(d, 0755); err != nil {
			t.Fatalf("failed to create dir %s: %v", d, err)
		}
	}

	pm := &ProcessManager{
		processes: make(map[string]*ManagedProcess),
		dataDir:   tmpDir,
	}

	dirs := pm.toolPathDirs()

	expectedMin := 2
	if runtime.GOOS == "windows" {
		expectedMin = 3
	}
	if len(dirs) < expectedMin {
		t.Errorf("toolPathDirs() returned %d entries, expected at least %d: %v", len(dirs), expectedMin, dirs)
	}
}

func TestToolPathDirs_nonexistentDirsSkipped(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()

	pm := &ProcessManager{
		processes: make(map[string]*ManagedProcess),
		dataDir:   filepath.Join(tmpDir, "nonexistent"),
	}

	dirs := pm.toolPathDirs()

	if len(dirs) != 0 {
		t.Errorf("toolPathDirs() with nonexistent dataDir returned %d entries, want 0: %v", len(dirs), dirs)
	}
}

func TestNewProcessManager(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		Processes: map[string]config.ProcessConfig{
			"ollama": {
				Name:      "ollama",
				Binary:    "ollama",
				AutoStart: true,
			},
		},
	}

	pm := NewProcessManager(cfg, nil, "/tmp/data")

	if len(pm.processes) != 1 {
		t.Fatalf("NewProcessManager: expected 1 process, got %d", len(pm.processes))
	}

	mp, ok := pm.processes["ollama"]
	if !ok {
		t.Fatal("NewProcessManager: 'ollama' process not found")
	}
	if mp.Status != "stopped" {
		t.Errorf("new process status = %q, want 'stopped'", mp.Status)
	}
	if mp.Config.Name != "ollama" {
		t.Errorf("process name = %q, want 'ollama'", mp.Config.Name)
	}
	if mp.LogBuf == nil {
		t.Error("process LogBuf should not be nil")
	}
}

func TestFindBinary_notFound(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()

	result := FindBinary(tmpDir, "nonexistent_binary_xyz_12345")
	if result != "" {
		t.Errorf("FindBinary for nonexistent = %q, want empty string", result)
	}
}

func TestFindBinary_inBinDir(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()

	var binDir string
	var binaryName string
	if runtime.GOOS == "windows" {
		binDir = filepath.Join(tmpDir, "bin")
		binaryName = "mytool.exe"
	} else {
		binDir = filepath.Join(tmpDir, "bin")
		binaryName = "mytool"
	}

	if err := os.MkdirAll(binDir, 0755); err != nil {
		t.Fatalf("failed to create bin dir: %v", err)
	}

	binaryPath := filepath.Join(binDir, binaryName)
	f, err := os.OpenFile(binaryPath, os.O_CREATE|os.O_WRONLY, 0755)
	if err != nil {
		t.Fatalf("failed to create mock binary: %v", err)
	}
	f.Close()

	result := FindBinary(tmpDir, "mytool")
	if result == "" {
		t.Error("FindBinary returned empty string, expected a path")
	}
	if result != binaryPath {
		t.Errorf("FindBinary = %q, want %q", result, binaryPath)
	}
}

func TestBackoffDuration(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		restarts int
		wantSecs int
	}{
		{"zero_restarts", 0, 2},
		{"one_restart", 1, 4},
		{"two_restarts", 2, 8},
		{"three_restarts", 3, 16},
		{"four_restarts", 4, 32},
		{"five_restarts_capped", 5, 32},
		{"ten_restarts_capped", 10, 32},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := backoffDuration(tt.restarts)
			want := time.Duration(tt.wantSecs) * time.Second
			if got != want {
				t.Errorf("backoffDuration(%d) = %v, want %v", tt.restarts, got, want)
			}
		})
	}
}
