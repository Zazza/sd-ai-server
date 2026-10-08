package tui

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func renderGPUQLine(t *testing.T, snap QueueSnapshot) string {
	t.Helper()
	m := NewDashboardModel(ServerDeps{QueueSnapshot: func() QueueSnapshot { return snap }}, "127.0.0.1")
	for _, line := range m.renderMetrics() {
		if strings.Contains(line, "GPUQ") {
			return line
		}
	}
	return ""
}

func TestRenderMetrics_GPUQueueLine(t *testing.T) {
	line := renderGPUQLine(t, QueueSnapshot{
		Budget:   15884,
		Running:  []QueueJob{{WeightMB: 11000}},
		Waiting:  []QueueJob{{}, {}},
		Warnings: []string{"w1", "w2", "w3"},
	})
	if line == "" {
		t.Fatal("GPUQ line missing")
	}
	for _, want := range []string{"11000/15884MB", "run 1", "queue 2", "warn 3"} {
		if !strings.Contains(line, want) {
			t.Fatalf("GPUQ line %q missing %q", line, want)
		}
	}
}

func TestRenderMetrics_GPUQueueNoWarnWhenZero(t *testing.T) {
	line := renderGPUQLine(t, QueueSnapshot{Budget: 15884})
	if line == "" {
		t.Fatal("GPUQ line missing")
	}
	if strings.Contains(line, "warn") {
		t.Fatalf("unexpected warn segment in %q", line)
	}
}

func TestRenderMetrics_GPUQueueHiddenWhenDisabled(t *testing.T) {
	if line := renderGPUQLine(t, QueueSnapshot{}); line != "" {
		t.Fatalf("GPUQ line must be hidden when disabled, got %q", line)
	}

	m := NewDashboardModel(ServerDeps{}, "127.0.0.1")
	for _, line := range m.renderMetrics() {
		if strings.Contains(line, "GPUQ") {
			t.Fatalf("GPUQ line must be hidden without QueueSnapshot dep, got %q", line)
		}
	}
}

func TestRenderMetrics_GPUQueueUsedSumsRunningWeights(t *testing.T) {
	line := renderGPUQLine(t, QueueSnapshot{
		Budget:  15884,
		Running: []QueueJob{{WeightMB: 11000}, {WeightMB: 3000}, {WeightMB: 884}},
		Waiting: []QueueJob{{WeightMB: 5000}},
	})
	require.NotEmpty(t, line, "GPUQ line missing")
	assert.Contains(t, line, "14884/15884MB")
	assert.Contains(t, line, "run 3")
	assert.Contains(t, line, "queue 1")
	assert.NotContains(t, line, "19884/15884MB")
}

func TestView_QueueHintWithAndWithoutDep(t *testing.T) {
	with := NewDashboardModel(ServerDeps{QueueSnapshot: func() QueueSnapshot { return QueueSnapshot{Budget: 1} }}, "127.0.0.1")
	with.width, with.height = 100, 30
	assert.Contains(t, with.View(), "[g] gpu queue")

	without := NewDashboardModel(ServerDeps{}, "127.0.0.1")
	without.width, without.height = 100, 30
	assert.NotContains(t, without.View(), "[g] gpu queue")
}

func TestCollectStats_UsesPollStats(t *testing.T) {
	want := SysStats{CPUUsage: 33.3, RAMUsage: 66.6, RAMUsed: 8, RAMTotal: 16}
	m := NewDashboardModel(ServerDeps{PollStats: func() SysStats { return want }}, "127.0.0.1")

	cmd := m.collectStats()
	require.NotNil(t, cmd)

	msg, ok := cmd().(sysStatsMsg)
	require.True(t, ok, "collectStats must emit sysStatsMsg")
	assert.InDelta(t, want.CPUUsage, msg.CPUUsage, 0.0001)
	assert.InDelta(t, want.RAMUsage, msg.RAMUsage, 0.0001)
	assert.Equal(t, want.RAMUsed, msg.RAMUsed)
	assert.Equal(t, want.RAMTotal, msg.RAMTotal)
}

func TestView_ConnStateBanner(t *testing.T) {
	m := NewDashboardModel(ServerDeps{
		ConnState: func() string { return "connecting to http://127.0.0.1:8080..." },
	}, "192.168.1.184")
	m.width, m.height = 100, 30

	assert.Contains(t, m.View(), "connecting to http://127.0.0.1:8080...")

	plain := NewDashboardModel(ServerDeps{}, "127.0.0.1")
	plain.width, plain.height = 100, 30
	assert.NotContains(t, plain.View(), "connecting")
}

func TestView_ConnStateEmptyBannerHidden(t *testing.T) {
	m := NewDashboardModel(ServerDeps{
		ConnState: func() string { return "" },
		ProcStatus: func() map[string]ServiceInfo {
			return map[string]ServiceInfo{"sd": {Name: "SD", Category: "service"}}
		},
		HealthResults: func() map[string]HealthResult { return nil },
	}, "127.0.0.1")
	m.width, m.height = 100, 30
	m.refreshServices()

	view := m.View()
	assert.Contains(t, view, "SD")
	header := strings.SplitN(view, "\n", 2)[0]
	assert.NotEqual(t, strings.TrimSpace(header), "", "header line must exist")
	assert.Zero(t, strings.Count(view, "connecting"), "no stray banner expected")
}

func TestUpdate_ServicesChangeMsg_Refreshes(t *testing.T) {
	deps := ServerDeps{
		ProcStatus: func() map[string]ServiceInfo {
			return map[string]ServiceInfo{
				"sd":     {Name: "SD Forge", Status: "running", PID: 5, Uptime: "1m", Category: "service"},
				"python": {Name: "Python", Status: "stopped", Category: "utility"},
			}
		},
		HealthResults: func() map[string]HealthResult {
			return map[string]HealthResult{"sd": {Healthy: true, LatencyMs: 12}}
		},
		GPUInfo: func() GPUInfo {
			return GPUInfo{Name: "RTX 4080", MemoryTotal: 16376, MemoryUsed: 9001, Utilization: 55, Available: true}
		},
	}
	m := NewDashboardModel(deps, "127.0.0.1")
	assert.Empty(t, m.services)
	assert.False(t, m.gpuInfo.Available)

	next, cmd := m.Update(ServicesChangeMsg{})
	require.Nil(t, cmd)

	d, ok := next.(DashboardModel)
	require.True(t, ok)
	assert.Equal(t, ServiceInfo{
		Name:     "SD Forge",
		Status:   "running",
		PID:      5,
		Uptime:   "1m",
		Category: "service",
		Healthy:  true,
		Latency:  12,
	}, d.services["sd"])
	assert.Equal(t, "Python", d.services["python"].Name)
	assert.Equal(t, "utility", d.services["python"].Category)
	assert.Equal(t, GPUInfo{Name: "RTX 4080", MemoryTotal: 16376, MemoryUsed: 9001, Utilization: 55, Available: true}, d.gpuInfo)
}

func TestAddress(t *testing.T) {
	tests := []struct {
		name string
		ip   string
		port int
		want string
	}{
		{name: "bare ip gets deps port", ip: "10.1.2.3", port: 9999, want: "10.1.2.3:9999"},
		{name: "hostport kept as is", ip: "192.168.1.184:8080", port: 0, want: "192.168.1.184:8080"},
		{name: "ipv6 hostport kept as is", ip: "[::1]:9000", port: 1234, want: "[::1]:9000"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := NewDashboardModel(ServerDeps{Port: tt.port}, tt.ip)
			assert.Equal(t, tt.want, m.address())
		})
	}
}

func TestNewAttachModel(t *testing.T) {
	deps := ServerDeps{ConnState: func() string { return "connecting" }}
	m := NewAttachModel(deps, "192.168.1.184:8080")

	assert.Equal(t, PhaseDashboard, m.phase)
	assert.Equal(t, "192.168.1.184:8080", m.ip)
	assert.Equal(t, "Loading...", m.View())
	assert.NotNil(t, m.Init())
}

func TestView_TerminalHintHiddenWithoutServerLogs(t *testing.T) {
	without := NewDashboardModel(ServerDeps{}, "127.0.0.1")
	without.width, without.height = 100, 30
	assert.NotContains(t, without.View(), "[t] terminal")

	with := NewDashboardModel(ServerDeps{ServerLogs: func() []string { return nil }}, "127.0.0.1")
	with.width, with.height = 100, 30
	assert.Contains(t, with.View(), "[t] terminal")
}
