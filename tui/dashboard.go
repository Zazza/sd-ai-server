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

		svcKeys := m.serviceOnlyKeys()
		switch msg.String() {
		case "q":
			return m, func() tea.Msg { return quitMsg{} }
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
			if m.cursor < len(svcKeys)-1 {
				m.cursor++
			}
		case "r":
			if m.cursor < len(svcKeys) {
				go m.deps.RestartProc(svcKeys[m.cursor])
			}
		case "s":
			if m.cursor < len(svcKeys) {
				name := svcKeys[m.cursor]
				info := m.services[name]
				if info.Status == "running" {
					go m.deps.StopProc(name)
				} else {
					go m.deps.StartProc(name)
				}
			}
		case "l":
			if m.cursor < len(svcKeys) {
				name := svcKeys[m.cursor]
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
			Category: ps.Category,
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

func (m DashboardModel) serviceOnlyKeys() []string {
	keys := m.serviceKeys()
	filtered := make([]string, 0, len(keys))
	for _, k := range keys {
		if m.services[k].Category != "utility" {
			filtered = append(filtered, k)
		}
	}
	return filtered
}

func (m DashboardModel) visibleLogLines() int {
	return m.height - 4
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
	return m.height - 4
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
	if m.width == 0 || m.height == 0 {
		return "Loading..."
	}
	if m.showTerminal {
		return m.viewTerminal()
	}
	if m.showLogs {
		return m.viewLogs()
	}
	return m.viewDashboard()
}

var headerBg = lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("#FAFAFA")).
		Background(lipgloss.Color("#7D56F4"))

func (m DashboardModel) viewLogs() string {
	w, h := m.width, m.height

	var lines []string
	lines = append(lines, headerBg.Render(fmt.Sprintf(" Logs: %s ", m.logKey)))
	lines = append(lines, sep(w))

	visible := h - 4
	contentArea := m.renderScrollArea(m.logLines, m.logOffset, visible, w)
	lines = append(lines, contentArea...)

	lines = append(lines, sep(w))

	total := len(m.logLines)
	top := m.logOffset + 1
	bot := m.logOffset + visible
	if bot > total {
		bot = total
	}
	scrollInfo := fmt.Sprintf("line %d-%d/%d", top, bot, total)
	footer := helpStyle.Render("[j/k/pgup/pgdn/g/G] scroll  [q/esc] back")
	footer += "  " + labelStyle.Render(scrollInfo)
	if m.logFollowing {
		footer += "  " + progressGreen.Render("FOLLOW")
	}
	lines = append(lines, footer)

	return fillScreen(w, h, lines)
}

func (m DashboardModel) viewTerminal() string {
	w, h := m.width, m.height

	var lines []string
	lines = append(lines, headerBg.Render(" Terminal "))
	lines = append(lines, sep(w))

	visible := h - 4
	contentArea := m.renderScrollArea(m.terminalLines, m.termOffset, visible, w)
	lines = append(lines, contentArea...)

	lines = append(lines, sep(w))

	total := len(m.terminalLines)
	top := m.termOffset + 1
	bot := m.termOffset + visible
	if bot > total {
		bot = total
	}
	scrollInfo := fmt.Sprintf("line %d-%d/%d", top, bot, total)
	footer := helpStyle.Render("[j/k/pgup/pgdn/g/G] scroll  [t/q/esc] back")
	footer += "  " + labelStyle.Render(scrollInfo)
	if m.termFollowing {
		footer += "  " + progressGreen.Render("FOLLOW")
	}
	lines = append(lines, footer)

	return fillScreen(w, h, lines)
}

func (m DashboardModel) renderScrollArea(logLines []string, offset, visible, width int) []string {
	result := make([]string, 0, visible)

	if len(logLines) == 0 {
		result = append(result, helpStyle.Render("No logs available"))
		for len(result) < visible {
			result = append(result, "")
		}
		return result
	}

	if offset > len(logLines)-visible {
		offset = len(logLines) - visible
	}
	if offset < 0 {
		offset = 0
	}

	end := offset + visible
	if end > len(logLines) {
		end = len(logLines)
	}

	for i := offset; i < end; i++ {
		line := logLines[i]
		runes := []rune(line)
		if len(runes) > width {
			line = string(runes[:width])
		}
		result = append(result, line)
	}

	for len(result) < visible {
		result = append(result, "")
	}

	return result
}

func (m DashboardModel) viewDashboard() string {
	w, h := m.width, m.height

	var lines []string

	port := m.deps.Port
	header := fmt.Sprintf(" SD Studio Server   %s  %s:%d ", runningStyle.Render("[RUNNING]"), m.ip, port)
	lines = append(lines, headerBg.Render(header))
	lines = append(lines, sep(w))
	lines = append(lines, m.renderMetrics()...)
	lines = append(lines, sep(w))
	lines = append(lines, subtitleStyle.Render("SERVICES"))

	keys := m.serviceKeys()
	cursorIdx := 0
	for _, key := range keys {
		si := m.services[key]
		if si.Category == "utility" {
			continue
		}
		cursor := " "
		if cursorIdx == m.cursor {
			cursor = selectedStyle.Render(">")
		}
		cursorIdx++

		var statusIcon, statusText string
		switch si.Status {
		case "running":
			statusIcon = healthyStyle.Render("*")
			healthText := ""
			if si.Healthy {
				healthText = fmt.Sprintf(" ok %dms", si.Latency)
			}
			statusText = fmt.Sprintf("%s PID %d %s%s", runningStyle.Render("running"), si.PID, si.Uptime, healthText)
		case "crashed":
			statusIcon = crashedStyle.Render("!")
			statusText = crashedStyle.Render("crashed")
		default:
			statusIcon = stoppedStyle.Render("o")
			statusText = stoppedStyle.Render(si.Status)
		}

		line := fmt.Sprintf("%s %s %-28s %s", cursor, statusIcon, si.Name, statusText)
		lines = append(lines, line)
	}

	// Utilities
	lines = append(lines, sep(w))
	lines = append(lines, subtitleStyle.Render("UTILITIES"))
	for _, key := range keys {
		si := m.services[key]
		if si.Category != "utility" || key == "python" {
			continue
		}
		installStatus := stoppedStyle.Render("not installed")
		installs := m.deps.InstallStatus()
		if is, ok := installs[key]; ok && is.Installed {
			installStatus = runningStyle.Render("installed")
			if is.Version != "" {
				installStatus += fmt.Sprintf(" v%s", is.Version)
			}
		}
		line := fmt.Sprintf("  %s %-28s %s", stoppedStyle.Render("-"), si.Name, installStatus)
		lines = append(lines, line)
	}

	for len(lines) < h-1 {
		lines = append(lines, "")
	}

	lines = append(lines, helpStyle.Render("[r] restart  [s] start/stop  [l] logs  [t] terminal  [q] quit"))

	return fillScreen(w, h, lines)
}

func (m DashboardModel) renderMetrics() []string {
	var lines []string

	cpuBar := renderBar(m.sysStats.CPUUsage, barWidth)
	lines = append(lines, fmt.Sprintf("  CPU  %s", cpuBar))

	ramUsed := formatMemMB(m.sysStats.RAMUsed)
	ramTotal := formatMemMB(m.sysStats.RAMTotal)
	ramBar := renderBar(m.sysStats.RAMUsage, barWidth)
	lines = append(lines, fmt.Sprintf("  RAM  %s  %s/%s", ramBar, ramUsed, ramTotal))

	if m.gpuInfo.Available {
		var vramPct float64
		if m.gpuInfo.MemoryTotal > 0 {
			vramPct = float64(m.gpuInfo.MemoryUsed) / float64(m.gpuInfo.MemoryTotal) * 100
		}
		vramBar := renderBar(vramPct, barWidth)
		vramUsed := fmt.Sprintf("%dMB", m.gpuInfo.MemoryUsed)
		vramTotal := fmt.Sprintf("%dMB", m.gpuInfo.MemoryTotal)
		lines = append(lines, fmt.Sprintf("  VRAM %s  %s/%s", vramBar, vramUsed, vramTotal))

		gpuBar := renderBar(float64(m.gpuInfo.Utilization), barWidth)
		lines = append(lines, fmt.Sprintf("  GPU  %s", gpuBar))
	}

	return lines
}

func formatMemMB(bytes uint64) string {
	const MB = 1024 * 1024
	const GB = 1024 * 1024 * 1024
	if bytes >= GB {
		return fmt.Sprintf("%.1fGB", float64(bytes)/float64(GB))
	}
	return fmt.Sprintf("%.0fMB", float64(bytes)/float64(MB))
}

func sep(w int) string {
	return strings.Repeat("-", w)
}

func fillScreen(w, h int, lines []string) string {
	for len(lines) < h {
		lines = append(lines, "")
	}
	if len(lines) > h {
		lines = lines[:h]
	}
	var b strings.Builder
	for i, l := range lines {
		pad := w - lipgloss.Width(l)
		if pad > 0 {
			b.WriteString(l)
			b.WriteString(strings.Repeat(" ", pad))
		} else {
			runes := []rune(l)
			if len(runes) > w {
				b.WriteString(string(runes[:w]))
			} else {
				b.WriteString(l)
			}
		}
		if i < h-1 {
			b.WriteByte('\n')
		}
	}
	return b.String()
}
