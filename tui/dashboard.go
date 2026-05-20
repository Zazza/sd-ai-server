package tui

import (
	"fmt"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type DashboardModel struct {
	deps          ServerDeps
	ip            string
	width         int
	height        int
	cursor        int
	sysStats      SysStats
	gpuInfo       GPUInfo
	services      map[string]ServiceInfo
	showLogs      bool
	logKey        string
	logLines      []string
	logOffset     int
	logFollowing  bool
	showTerminal  bool
	terminalLines []string
	termOffset    int
	termFollowing bool
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

type logsTickMsg time.Time

func logsTick() tea.Cmd {
	return tea.Tick(2*time.Second, func(t time.Time) tea.Msg {
		return logsTickMsg(t)
	})
}

func (m DashboardModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height

	case logsTickMsg:
		if m.showLogs {
			m.refreshLogs(m.logKey)
			if m.logFollowing {
				m.logOffset = m.maxLogOffset()
			}
			return m, logsTick()
		}
		if m.showTerminal {
			if m.deps.ServerLogs != nil {
				m.terminalLines = m.deps.ServerLogs()
			}
			if m.termFollowing {
				m.termOffset = m.maxTermOffset()
			}
			return m, logsTick()
		}
		return m, nil

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
				m.logOffset = 0
				m.logFollowing = false
				return m, nil
			case "up", "k":
				m.logFollowing = false
				if m.logOffset > 0 {
					m.logOffset--
				}
			case "down", "j":
				if m.logOffset < m.maxLogOffset() {
					m.logOffset++
				}
				if m.logOffset >= m.maxLogOffset() {
					m.logFollowing = true
				}
			case "pgup":
				m.logFollowing = false
				m.logOffset -= m.visibleLogLines()
				if m.logOffset < 0 {
					m.logOffset = 0
				}
			case "pgdown":
				m.logOffset += m.visibleLogLines()
				if m.logOffset > m.maxLogOffset() {
					m.logOffset = m.maxLogOffset()
				}
				if m.logOffset >= m.maxLogOffset() {
					m.logFollowing = true
				}
			case "g":
				m.logFollowing = false
				m.logOffset = 0
			case "G":
				m.logFollowing = true
				m.logOffset = m.maxLogOffset()
			}
			return m, nil
		}

		if m.showTerminal {
			switch msg.String() {
			case "t", "q", "esc":
				m.showTerminal = false
				m.termOffset = 0
				m.termFollowing = false
				return m, nil
			case "up", "k":
				m.termFollowing = false
				if m.termOffset > 0 {
					m.termOffset--
				}
			case "down", "j":
				if m.termOffset < m.maxTermOffset() {
					m.termOffset++
				}
				if m.termOffset >= m.maxTermOffset() {
					m.termFollowing = true
				}
			case "pgup":
				m.termFollowing = false
				m.termOffset -= m.visibleTermLines()
				if m.termOffset < 0 {
					m.termOffset = 0
				}
			case "pgdown":
				m.termOffset += m.visibleTermLines()
				if m.termOffset > m.maxTermOffset() {
					m.termOffset = m.maxTermOffset()
				}
				if m.termOffset >= m.maxTermOffset() {
					m.termFollowing = true
				}
			case "g":
				m.termFollowing = false
				m.termOffset = 0
			case "G":
				m.termFollowing = true
				m.termOffset = m.maxTermOffset()
			}
			return m, nil
		}

		keys := m.serviceKeys()
		switch msg.String() {
		case "q":
			return m, tea.Quit
		case "t":
			m.showTerminal = true
			m.termOffset = 0
			m.termFollowing = true
			if m.deps.ServerLogs != nil {
				m.terminalLines = m.deps.ServerLogs()
			}
			m.termOffset = m.maxTermOffset()
			return m, logsTick()
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
				m.logOffset = 0
				m.logFollowing = true
				m.refreshLogs(name)
				m.logOffset = m.maxLogOffset()
				return m, logsTick()
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
	m.logLines = m.deps.ProcLogs(name, 200)
}

func (m DashboardModel) serviceKeys() []string {
	keys := make([]string, 0, len(m.services))
	for k := range m.services {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func (m DashboardModel) visibleLogLines() int {
	v := m.height - 6
	if v < 5 {
		v = 5
	}
	return v
}

func (m DashboardModel) maxLogOffset() int {
	total := len(m.logLines)
	visible := m.visibleLogLines()
	if total <= visible {
		return 0
	}
	return total - visible
}

func (m DashboardModel) visibleTermLines() int {
	v := m.height - 6
	if v < 5 {
		v = 5
	}
	return v
}

func (m DashboardModel) maxTermOffset() int {
	total := len(m.terminalLines)
	visible := m.visibleTermLines()
	if total <= visible {
		return 0
	}
	return total - visible
}

func (m DashboardModel) View() string {
	if m.showTerminal {
		return m.viewTerminal()
	}
	if m.showLogs {
		return m.viewLogs()
	}
	return m.viewDashboard()
}

func (m DashboardModel) viewLogs() string {
	var b strings.Builder

	header := titleStyle.Render(fmt.Sprintf(" Logs: %s ", m.logKey))
	b.WriteString(header)
	b.WriteString("\n")

	b.WriteString(separator(m.width))
	b.WriteString("\n")

	maxWidth := m.width - 4
	if maxWidth < 40 {
		maxWidth = 40
	}

	visible := m.visibleLogLines()

	if len(m.logLines) == 0 {
		b.WriteString(helpStyle.Render("No logs available"))
	} else {
		offset := m.logOffset
		if offset > m.maxLogOffset() {
			offset = m.maxLogOffset()
		}

		end := offset + visible
		if end > len(m.logLines) {
			end = len(m.logLines)
		}

		for i := offset; i < end; i++ {
			line := m.logLines[i]
			if maxWidth > 0 && len(line) > maxWidth {
				line = line[:maxWidth]
			}
			b.WriteString(line)
			b.WriteString("\n")
		}
	}

	total := len(m.logLines)
	top := m.logOffset + 1
	bot := m.logOffset + visible
	if bot > total {
		bot = total
	}
	scrollInfo := fmt.Sprintf(" line %d-%d/%d ", top, bot, total)

	followTag := ""
	if m.logFollowing {
		followTag = progressGreen.Render("FOLLOW")
	}

	b.WriteString(separator(m.width))
	b.WriteString("\n")
	b.WriteString(helpStyle.Render("[j/k/pgup/pgdn/g/G] scroll  [q/esc] back"))
	b.WriteString("  ")
	b.WriteString(labelStyle.Render(scrollInfo))
	if followTag != "" {
		b.WriteString("  ")
		b.WriteString(followTag)
	}

	return borderStyle.Render(b.String())
}

func (m DashboardModel) viewTerminal() string {
	var b strings.Builder

	header := titleStyle.Render(" Terminal ")
	b.WriteString(header)
	b.WriteString("\n")

	b.WriteString(separator(m.width))
	b.WriteString("\n")

	maxWidth := m.width - 4
	if maxWidth < 40 {
		maxWidth = 40
	}

	visible := m.visibleTermLines()

	if len(m.terminalLines) == 0 {
		b.WriteString(helpStyle.Render("No output yet"))
	} else {
		offset := m.termOffset
		if offset > m.maxTermOffset() {
			offset = m.maxTermOffset()
		}

		end := offset + visible
		if end > len(m.terminalLines) {
			end = len(m.terminalLines)
		}

		for i := offset; i < end; i++ {
			line := m.terminalLines[i]
			if maxWidth > 0 && len(line) > maxWidth {
				line = line[:maxWidth]
			}
			b.WriteString(line)
			b.WriteString("\n")
		}
	}

	total := len(m.terminalLines)
	top := m.termOffset + 1
	bot := m.termOffset + visible
	if bot > total {
		bot = total
	}
	scrollInfo := fmt.Sprintf(" line %d-%d/%d ", top, bot, total)

	followTag := ""
	if m.termFollowing {
		followTag = progressGreen.Render("FOLLOW")
	}

	b.WriteString(separator(m.width))
	b.WriteString("\n")
	b.WriteString(helpStyle.Render("[j/k/pgup/pgdn/g/G] scroll  [t/q/esc] back"))
	b.WriteString("  ")
	b.WriteString(labelStyle.Render(scrollInfo))
	if followTag != "" {
		b.WriteString("  ")
		b.WriteString(followTag)
	}

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
	b.WriteString(helpStyle.Render("[r] restart  [s] start/stop  [l] logs  [t] terminal  [q] quit"))

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
