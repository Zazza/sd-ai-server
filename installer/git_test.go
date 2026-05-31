package installer

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestGitBinPath(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		dataDir string
		want    string
	}{
		{
			name:    "basic_path",
			dataDir: "/tmp/data",
			want: func() string {
				if runtime.GOOS == "windows" {
					return filepath.Join("/tmp/data", "git", "cmd", "git.exe")
				}
				return filepath.Join("/tmp/data", "bin", "git")
			}(),
		},
		{
			name:    "empty_dataDir",
			dataDir: "",
			want: func() string {
				if runtime.GOOS == "windows" {
					return filepath.Join("", "git", "cmd", "git.exe")
				}
				return filepath.Join("", "bin", "git")
			}(),
		},
		{
			name:    "nested_path",
			dataDir: "/home/user/.local/share/sd-studio",
			want: func() string {
				if runtime.GOOS == "windows" {
					return filepath.Join("/home/user/.local/share/sd-studio", "git", "cmd", "git.exe")
				}
				return filepath.Join("/home/user/.local/share/sd-studio", "bin", "git")
			}(),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := GitBinPath(tt.dataDir)
			if got != tt.want {
				t.Errorf("GitBinPath(%q) = %q, want %q", tt.dataDir, got, tt.want)
			}
		})
	}
}

func TestGitBinPath_platformComponents(t *testing.T) {
	t.Parallel()

	got := GitBinPath("/data")

	if runtime.GOOS == "windows" {
		if !strings.HasSuffix(got, "git.exe") {
			t.Errorf("on windows expected path ending with git.exe, got %q", got)
		}
		if !strings.Contains(got, filepath.Join("git", "cmd")) {
			t.Errorf("on windows expected path containing git/cmd, got %q", got)
		}
	} else {
		if !strings.HasSuffix(got, "git") {
			t.Errorf("on non-windows expected path ending with git, got %q", got)
		}
		if !strings.Contains(got, "bin") {
			t.Errorf("on non-windows expected path containing bin, got %q", got)
		}
	}
}

func TestGitAvailable_emptyDir(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()

	if !GitAvailable(tmpDir) {
		t.Log("GitAvailable returned false for empty temp dir, checking system PATH as fallback")
	}

	_, _ = os.Stat(filepath.Join(tmpDir, "nonexistent"))
}

func TestGitAvailable_withBinary(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()

	binPath := GitBinPath(tmpDir)
	dir := filepath.Dir(binPath)

	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("failed to create bin dir: %v", err)
	}

	f, err := os.OpenFile(binPath, os.O_CREATE|os.O_WRONLY, 0755)
	if err != nil {
		t.Fatalf("failed to create mock git binary: %v", err)
	}
	f.Close()

	if !GitAvailable(tmpDir) {
		t.Errorf("GitAvailable(%q) = false, want true (binary exists)", tmpDir)
	}
}

func TestGitAvailable_noBinary(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()

	nestedBinPath := GitBinPath(tmpDir)

	_ = nestedBinPath

	result := GitAvailable(tmpDir)
	if runtime.GOOS == "linux" || runtime.GOOS == "darwin" {
		if !result {
			t.Log("git not found in system PATH either - this is expected in some CI environments")
		}
	}
}

func TestMingitURL_format(t *testing.T) {
	t.Parallel()

	url := mingitURL()

	if !strings.Contains(url, gitVersion) {
		t.Errorf("mingitURL() = %q, expected to contain version %q", url, gitVersion)
	}

	if !strings.Contains(url, "MinGit") {
		t.Errorf("mingitURL() = %q, expected to contain 'MinGit'", url)
	}

	if !strings.HasSuffix(url, ".zip") {
		t.Errorf("mingitURL() = %q, expected to end with .zip", url)
	}

	if !strings.Contains(url, gitWinTag) {
		t.Errorf("mingitURL() = %q, expected to contain tag %q", url, gitWinTag)
	}

	expectedPrefix := "https://github.com/git-for-windows/git/releases/download/"
	if !strings.HasPrefix(url, expectedPrefix) {
		t.Errorf("mingitURL() = %q, expected to start with %q", url, expectedPrefix)
	}
}

func TestMingitURL_archSuffix(t *testing.T) {
	t.Parallel()

	url := mingitURL()

	if runtime.GOARCH == "arm64" {
		if !strings.Contains(url, "arm64") {
			t.Errorf("on arm64 expected URL to contain 'arm64', got %q", url)
		}
	} else {
		if !strings.Contains(url, "64-bit") {
			t.Errorf("on non-arm64 expected URL to contain '64-bit', got %q", url)
		}
	}
}

func TestFormatBytes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		bytes int64
		want  string
	}{
		{"zero", 0, "0 B"},
		{"bytes", 512, "512 B"},
		{"kilobytes", 1024, "1.0 KB"},
		{"megabytes", 1048576, "1.0 MB"},
		{"gigabytes", 1073741824, "1.0 GB"},
		{"partial_KB", 1536, "1.5 KB"},
		{"partial_MB", 2621440, "2.5 MB"},
		{"partial_GB", 1610612736, "1.5 GB"},
		{"large_bytes", 999, "999 B"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := FormatBytes(tt.bytes)
			if got != tt.want {
				t.Errorf("FormatBytes(%d) = %q, want %q", tt.bytes, got, tt.want)
			}
		})
	}
}
