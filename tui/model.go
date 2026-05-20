package tui

import (
	"context"
	"fmt"
	"net"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type ServerDeps struct {
	Port    int
	DataDir string

	EnsureAllInstalled func()
	InstallStatus      func() map[string]ComponentInstallStatus

	StartAll      func()
	StartMonitors func()
	ProcStatus    func() map[string]ServiceInfo
	StartProc     func(string) error
	StopProc      func(string) error
	RestartProc   func(string) error
	ProcLogs      func(string, int) []string

	GPUInfo       func() GPUInfo
	HealthResults func() map[string]HealthResult
	ServerLogs    func() []string
}

type ComponentInstallStatus struct {
	Key        string
	Installed  bool
	Installing bool
	Progress   string
	Error      string
}

type ServiceInfo struct {
	Name    string
	Status  string
	PID     int
	Uptime  string
	Healthy bool
	Latency int64
}

type GPUInfo struct {
	Name        string
	MemoryTotal int
	MemoryUsed  int
	Utilization int
	Available   bool
}

type HealthResult struct {
	Healthy   bool
	LatencyMs int64
	Error     string
}

type AppModel struct {
	phase    Phase
	deps     ServerDeps
	ctx      context.Context
	cancel   context.CancelFunc
	width    int
	height   int
	ip       string

	install   InstallModel
	dashboard DashboardModel
	quitting  bool
}

func NewAppModel(deps ServerDeps) AppModel {
	ctx, cancel := context.WithCancel(context.Background())

	m := AppModel{
		deps:   deps,
		ctx:    ctx,
		cancel: cancel,
		ip:     getLocalIP(),
		phase:  PhaseInstall,
	}

	m.install = NewInstallModel(deps)
	m.dashboard = NewDashboardModel(deps, m.ip)

	return m
}

func (m AppModel) Init() tea.Cmd {
	switch m.phase {
	case PhaseInstall:
		return tea.Batch(m.install.Init(), SysStatsTick())
	case PhaseDashboard:
		return tea.Batch(m.dashboard.Init(), SysStatsTick())
	}
	return nil
}

func (m AppModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.install.width = msg.Width
		m.dashboard.width = msg.Width
		m.dashboard.height = msg.Height

	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c":
			m.quitting = true
			m.cancel()
			return m, tea.Quit
		}

	case quitMsg:
		m.quitting = true
		m.cancel()
		return m, tea.Quit

	case InstallDoneMsg:
		m.phase = PhaseDashboard
		if m.deps.StartAll != nil {
			m.deps.StartAll()
		}
		if m.deps.StartMonitors != nil {
			m.deps.StartMonitors()
		}
		m.dashboard = NewDashboardModel(m.deps, m.ip)
		m.dashboard.width = m.width
		m.dashboard.height = m.height
		return m, tea.Batch(m.dashboard.Init(), SysStatsTick())

	case tickMsg:
		if m.phase == PhaseDashboard {
			return m, tea.Batch(m.dashboard.collectStats(), SysStatsTick())
		}
		return m, SysStatsTick()
	}

	var cmd tea.Cmd
	switch m.phase {
	case PhaseInstall:
		i, c := m.install.Update(msg)
		m.install = i.(InstallModel)
		cmd = c
	case PhaseDashboard:
		d, c := m.dashboard.Update(msg)
		m.dashboard = d.(DashboardModel)
		cmd = c
	}
	return m, cmd
}

func (m AppModel) View() string {
	if m.quitting {
		return "\n  Shutting down...\n\n"
	}

	switch m.phase {
	case PhaseInstall:
		return m.install.View()
	case PhaseDashboard:
		return m.dashboard.View()
	}
	return ""
}

func (m *AppModel) Context() context.Context {
	return m.ctx
}

func (m *AppModel) Cancel() {
	m.cancel()
}

func getLocalIP() string {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return "127.0.0.1"
	}
	for _, addr := range addrs {
		if ipnet, ok := addr.(*net.IPNet); ok && !ipnet.IP.IsLoopback() && ipnet.IP.To4() != nil {
			return ipnet.IP.String()
		}
	}
	return "127.0.0.1"
}

func renderBar(pct float64, width int) string {
	filled := int(pct / 100 * float64(width))
	if filled > width {
		filled = width
	}
	if filled < 0 {
		filled = 0
	}
	bar := strings.Repeat("█", filled) + strings.Repeat("░", width-filled)
	return fmt.Sprintf("[%-*s] %3.0f%%", width, bar, pct)
}

func centerBlock(width int, s string) string {
	lines := strings.Split(s, "\n")
	maxW := 0
	for _, l := range lines {
		if lipgloss.Width(l) > maxW {
			maxW = lipgloss.Width(l)
		}
	}
	if maxW >= width {
		return s
	}
	pad := (width - maxW) / 2
	var b strings.Builder
	for _, l := range lines {
		b.WriteString(strings.Repeat(" ", pad))
		b.WriteString(l)
		b.WriteString("\n")
	}
	return b.String()
}
