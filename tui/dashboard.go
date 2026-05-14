package tui

import (
	"fmt"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type DashboardModel struct {
	deps     ServerDeps
	ip       string
	width    int
	height   int
	cursor   int
	sysStats SysStats
	gpuInfo  GPUInfo
	services map[string]ServiceInfo
	showLogs bool
	logKey   string
	logLines []string
}

func NewDashboardModel(deps ServerDeps, ip string) DashboardModel {
	return DashboardModel{
		deps: deps,
		ip:   ip,
	}
}

func (m DashboardModel) Init() tea.Cmd {
	return nil
}

func (m DashboardModel) collectStats() tea.Cmd {
	return func() tea.Msg {
		stats := PollSysStats()
		return sysStatsMsg{
			CPUUsage: stats.CPUUsage,
			RAMUsage: stats.RAMUsage,
			RAMUsed:  stats.RAMUsed,
			RAMTotal: stats.RAMTotal,
		}
	}
}

func (m DashboardModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height

	case sysStatsMsg:
		m.sysStats = SysStats{
			CPUUsage: msg.CPUUsage,
			RAMUsage: msg.RAMUsage,
			RAMUsed:  msg.RAMUsed,
			RAMTotal: msg.RAMTotal,
		}
		m.refreshServices()
		m.refreshGPU()
		return m, nil

	case tea.KeyMsg:
		if m.showLogs {
			switch msg.String() {
			case "q", "esc":
				m.showLogs = false
				return m, nil
			}
			return m, nil
		}

		keys := m.serviceKeys()
		switch msg.String() {
		case "q":
			return m, tea.Quit
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
		case "down", "j":
			if m.cursor < len(keys)-1 {
				m.cursor++
			}
		case "r":
			if m.cursor < len(keys) {
				go m.deps.RestartProc(keys[m.cursor])
			}
		case "s":
			if m.cursor < len(keys) {
				name := keys[m.cursor]
				info := m.services[name]
				if info.Status == "running" {
					go m.deps.StopProc(name)
				} else {
					go m.deps.StartProc(name)
				}
			}
		case "l":
			if m.cursor < len(keys) {
				name := keys[m.cursor]
				m.showLogs = true
				m.logKey = name
				m.refreshLogs(name)
			}
		}
	}
	return m, nil
}

func (m *DashboardModel) refreshServices() {
	statuses := m.deps.ProcStatus()
	healthResults := m.deps.HealthResults()

	m.services = make(map[string]ServiceInfo, len(statuses))
	for k, ps := range statuses {
		si := ServiceInfo{
			Name:   ps.Name,
			Status: ps.Status,
			PID:    ps.PID,
			Uptime: ps.Uptime,
		}
		if hr, ok := healthResults[k]; ok {
			si.Healthy = hr.Healthy
			si.Latency = hr.LatencyMs
		}
		m.services[k] = si
	}
}

func (m *DashboardModel) refreshGPU() {
	info := m.deps.GPUInfo()
	m.gpuInfo = info
}

func (m *DashboardModel) refreshLogs(name string) {
	m.logLines = m.deps.ProcLogs(name, 30)
}

func (m DashboardModel) serviceKeys() []string {
	keys := make([]string, 0, len(m.services))
	for k := range m.services {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func (m DashboardModel) View() string {
	if m.showLogs {
		return m.viewLogs()
	}
	return m.viewDashboard()
}

func (m DashboardModel) viewLogs() string {
	var b strings.Builder

	header := titleStyle.Render(fmt.Sprintf(" Logs: %s ", m.logKey))
	b.WriteString(header)
	b.WriteString("\n\n")

	if len(m.logLines) == 0 {
		b.WriteString(helpStyle.Render("No logs available"))
	} else {
		for _, line := range m.logLines {
			if len(line) > 120 {
				line = line[:120]
			}
			b.WriteString(line)
			b.WriteString("\n")
		}
	}

	b.WriteString("\n")
	b.WriteString(helpStyle.Render("q/esc — back"))

	return borderStyle.Render(b.String())
}

func (m DashboardModel) viewDashboard() string {
	var b strings.Builder

	port := m.deps.Port
	header := fmt.Sprintf(" SD Studio Server    %s  %s:%d ", runningStyle.Render("[RUNNING]"), m.ip, port)
	b.WriteString(titleStyle.Render(header))
	b.WriteString("\n")

	b.WriteString(separator(m.width))
	b.WriteString("\n")

	b.WriteString(m.renderMetrics())
	b.WriteString("\n")

	b.WriteString(separator(m.width))
	b.WriteString("\n")

	b.WriteString(subtitleStyle.Render("SERVICES"))
	b.WriteString("\n")

	keys := m.serviceKeys()
	for i, key := range keys {
		si := m.services[key]
		cursor := " "
		if i == m.cursor {
			cursor = lipgloss.NewStyle().Foreground(lipgloss.Color("#7D56F4")).Render(">")
		}

		var statusIcon, statusText string
		switch si.Status {
		case "running":
			statusIcon = healthyStyle.Render("●")
			healthText := ""
			if si.Healthy {
				healthText = fmt.Sprintf(" healthy %dms", si.Latency)
			}
			statusText = fmt.Sprintf("%s PID %d  %s%s", runningStyle.Render("running"), si.PID, si.Uptime, healthText)
		case "crashed":
			statusIcon = crashedStyle.Render("●")
			statusText = crashedStyle.Render("crashed")
		default:
			statusIcon = stoppedStyle.Render("o")
			statusText = stoppedStyle.Render(si.Status)
		}

		line := fmt.Sprintf("%s %s %-28s %s", cursor, statusIcon, si.Name, statusText)
		b.WriteString(line)
		b.WriteString("\n")
	}

	b.WriteString("\n")
	b.WriteString(helpStyle.Render("[r] restart  [s] start/stop  [l] logs  [q] quit"))

	return borderStyle.Render(b.String())
}

func (m DashboardModel) renderMetrics() string {
	var b strings.Builder

	cpuBar := renderBar(m.sysStats.CPUUsage, barWidth)
	b.WriteString(fmt.Sprintf("  CPU  %s\n", cpuBar))

	ramUsed := formatMemMB(m.sysStats.RAMUsed)
	ramTotal := formatMemMB(m.sysStats.RAMTotal)
	ramBar := renderBar(m.sysStats.RAMUsage, barWidth)
	b.WriteString(fmt.Sprintf("  RAM  %s  %s/%s\n", ramBar, ramUsed, ramTotal))

	if m.gpuInfo.Available {
		var vramPct float64
		if m.gpuInfo.MemoryTotal > 0 {
			vramPct = float64(m.gpuInfo.MemoryUsed) / float64(m.gpuInfo.MemoryTotal) * 100
		}
		vramBar := renderBar(vramPct, barWidth)
		vramUsed := fmt.Sprintf("%dMB", m.gpuInfo.MemoryUsed)
		vramTotal := fmt.Sprintf("%dMB", m.gpuInfo.MemoryTotal)
		b.WriteString(fmt.Sprintf("  VRAM %s  %s/%s\n", vramBar, vramUsed, vramTotal))

		gpuBar := renderBar(float64(m.gpuInfo.Utilization), barWidth)
		b.WriteString(fmt.Sprintf("  GPU  %s\n", gpuBar))
	}

	return b.String()
}

func formatMemMB(bytes uint64) string {
	const MB = 1024 * 1024
	const GB = 1024 * 1024 * 1024
	if bytes >= GB {
		return fmt.Sprintf("%.1fGB", float64(bytes)/float64(GB))
	}
	return fmt.Sprintf("%.0fMB", float64(bytes)/float64(MB))
}

func separator(width int) string {
	if width <= 0 {
		width = 60
	}
	return lipgloss.NewStyle().Foreground(lipgloss.Color("#333333")).Render(strings.Repeat("-", width-4))
}
