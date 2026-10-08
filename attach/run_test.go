package attach

import (
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseTarget(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name         string
		input        string
		wantHost     string
		wantPort     int
		wantHostPort string
		wantBase     string
		wantErr      bool
		errContains  string
	}{
		{
			name:         "empty defaults to loopback and 8080",
			input:        "",
			wantHost:     "127.0.0.1",
			wantPort:     8080,
			wantHostPort: "127.0.0.1:8080",
			wantBase:     "http://127.0.0.1:8080",
		},
		{
			name:         "host only gets default port",
			input:        "192.168.1.184",
			wantHost:     "192.168.1.184",
			wantPort:     8080,
			wantHostPort: "192.168.1.184:8080",
			wantBase:     "http://192.168.1.184:8080",
		},
		{
			name:         "host and port",
			input:        "192.168.1.184:9000",
			wantHost:     "192.168.1.184",
			wantPort:     9000,
			wantHostPort: "192.168.1.184:9000",
			wantBase:     "http://192.168.1.184:9000",
		},
		{
			name:         "http scheme is stripped",
			input:        "http://192.168.1.184:9000",
			wantHost:     "192.168.1.184",
			wantPort:     9000,
			wantHostPort: "192.168.1.184:9000",
			wantBase:     "http://192.168.1.184:9000",
		},
		{
			name:         "trailing slash is stripped",
			input:        "http://127.0.0.1:8080/",
			wantHost:     "127.0.0.1",
			wantPort:     8080,
			wantHostPort: "127.0.0.1:8080",
			wantBase:     "http://127.0.0.1:8080",
		},
		{
			name:         "bracketed ipv6 with port",
			input:        "[::1]:8080",
			wantHost:     "::1",
			wantPort:     8080,
			wantHostPort: "[::1]:8080",
			wantBase:     "http://[::1]:8080",
		},
		{
			name:         "bare ipv6 gets brackets in hostport",
			input:        "::1",
			wantHost:     "::1",
			wantPort:     8080,
			wantHostPort: "[::1]:8080",
			wantBase:     "http://[::1]:8080",
		},
		{
			name:         "bracketed ipv6 without port",
			input:        "[::1]",
			wantHost:     "::1",
			wantPort:     8080,
			wantHostPort: "[::1]:8080",
			wantBase:     "http://[::1]:8080",
		},
		{
			name:         "localhost without port",
			input:        "localhost",
			wantHost:     "localhost",
			wantPort:     8080,
			wantHostPort: "localhost:8080",
			wantBase:     "http://localhost:8080",
		},
		{
			name:     "port lower boundary is valid",
			input:    "h:1",
			wantHost: "h",
			wantPort: 1,
		},
		{
			name:     "port upper boundary is valid",
			input:    "h:65535",
			wantHost: "h",
			wantPort: 65535,
		},
		{
			name:        "port zero is rejected",
			input:       "h:0",
			wantErr:     true,
			errContains: "out of range",
		},
		{
			name:        "port above 65535 is rejected",
			input:       "h:65536",
			wantErr:     true,
			errContains: "out of range",
		},
		{
			name:        "negative port is rejected",
			input:       "h:-1",
			wantErr:     true,
			errContains: "out of range",
		},
		{
			name:        "non numeric port is rejected",
			input:       "h:abc",
			wantErr:     true,
			errContains: "invalid port",
		},
		{
			name:        "https is unsupported",
			input:       "https://x",
			wantErr:     true,
			errContains: "unsupported target",
		},
		{
			name:        "missing closing bracket",
			input:       "[::1:8080",
			wantErr:     true,
			errContains: "closing bracket",
		},
		{
			name:        "empty host is rejected",
			input:       ":8080",
			wantErr:     true,
			errContains: "empty host",
		},
		{
			name:        "invalid ipv6 is rejected",
			input:       "zz::x",
			wantErr:     true,
			errContains: "invalid IPv6",
		},
		{
			name:        "short help flag is rejected",
			input:       "-h",
			wantErr:     true,
			errContains: "usage",
		},
		{
			name:        "long help flag is rejected",
			input:       "--help",
			wantErr:     true,
			errContains: "usage",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := ParseTarget(tt.input)
			if tt.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errContains)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantHost, got.Host)
			assert.Equal(t, tt.wantPort, got.Port)
			if tt.wantHostPort != "" {
				assert.Equal(t, tt.wantHostPort, got.HostPort())
			}
			if tt.wantBase != "" {
				assert.Equal(t, tt.wantBase, got.Base)
			}
		})
	}
}

func swapStdin(t *testing.T, f *os.File) {
	t.Helper()
	old := os.Stdin
	os.Stdin = f
	t.Cleanup(func() {
		os.Stdin = old
	})
}

func withTerminalStdin(t *testing.T) {
	t.Helper()
	f, err := os.OpenFile("/dev/ptmx", os.O_RDWR, 0)
	if err != nil {
		t.Skipf("cannot allocate pty: %v", err)
	}
	swapStdin(t, f)
	t.Cleanup(func() { f.Close() })
}

func withNullStdin(t *testing.T) {
	t.Helper()
	f, err := os.Open(os.DevNull)
	require.NoError(t, err)
	swapStdin(t, f)
	t.Cleanup(func() { f.Close() })
}

func TestMain_StdinNotATerminal(t *testing.T) {
	withNullStdin(t)
	err := Main([]string{"192.168.1.184:8080"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not a terminal")
}

func TestMain_TooManyArgs(t *testing.T) {
	withTerminalStdin(t)
	err := Main([]string{"a", "b"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "usage")
}

func TestMain_Unreachable(t *testing.T) {
	withTerminalStdin(t)
	start := time.Now()
	err := Main([]string{"127.0.0.1:1"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unreachable")
	assert.Contains(t, err.Error(), "http://127.0.0.1:1")
	assert.Less(t, time.Since(start), preflightTimeout, "preflight must fail fast")
}

func TestMain_BadTarget(t *testing.T) {
	withTerminalStdin(t)
	err := Main([]string{"https://x"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported target")
}

func TestMain_HelpArg(t *testing.T) {
	withTerminalStdin(t)
	for _, arg := range []string{"-h", "--help"} {
		err := Main([]string{arg})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "usage")
	}
}
