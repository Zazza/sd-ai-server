package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
)

type InstallModel struct {
	deps     ServerDeps
	spinner  spinner.Model
	statuses map[string]ComponentInstallStatus
	active   string
	width    int
	done     bool
}

type InstallDoneMsg struct{}

func NewInstallModel(deps ServerDeps) InstallModel {
	s := spinner.New()
	s.Spinner = spinner.Dot
	s.Style = progressBlue

	statuses := deps.InstallStatus()

	return InstallModel{
		deps:     deps,
		spinner:  s,
		statuses: statuses,
	}
}

func (m InstallModel) Init() tea.Cmd {
	return tea.Batch(m.spinner.Tick, m.runInstall())
}

func (m InstallModel) runInstall() tea.Cmd {
	return func() tea.Msg {
		m.deps.EnsureAllInstalled()
		return allInstalledMsg{}
	}
}

func (m InstallModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width

	case installProgressMsg:
		if s, ok := m.statuses[msg.Key]; ok {
			s.Progress = msg.Progress
			if msg.Progress == "done" {
				s.Installed = true
				s.Installing = false
			} else if msg.Progress == "failed" {
				s.Installing = false
				s.Error = "failed"
			} else {
				s.Installing = true
			}
			m.statuses[msg.Key] = s
		}
		m.active = msg.Key
		return m, nil

	case allInstalledMsg:
		m.done = true
		return m, func() tea.Msg { return InstallDoneMsg{} }
	}

	var cmd tea.Cmd
	m.spinner, cmd = m.spinner.Update(msg)
	return m, cmd
}

func (m InstallModel) View() string {
	var b strings.Builder

	b.WriteString(titleStyle.Render(" Installing components... "))
	b.WriteString("\n\n")

	order := []string{"python", "forge", "ollama"}

	for _, key := range order {
		s, ok := m.statuses[key]
		if !ok {
			continue
		}

		label := fmt.Sprintf("%-16s", componentLabel(key))

		switch {
		case s.Installed:
			b.WriteString(label)
			b.WriteString(progressGreen.Render(" done"))
		case s.Installing:
			b.WriteString(label)
			b.WriteString(m.spinner.View())
			b.WriteString(" ")
			b.WriteString(progressBlue.Render(truncateProgress(s.Progress, 40)))
		case s.Error != "":
			b.WriteString(label)
			b.WriteString(progressRed.Render(" failed"))
		default:
			b.WriteString(label)
			b.WriteString(progressGray.Render(" waiting"))
		}
		b.WriteString("\n")
	}

	if m.done {
		b.WriteString("\n")
		b.WriteString(progressGreen.Render("All components installed! Starting services..."))
	}

	b.WriteString("\n\n")
	b.WriteString(helpStyle.Render("Ctrl+C to quit"))

	return borderStyle.Render(b.String())
}

func componentLabel(key string) string {
	switch key {
	case "python":
		return "Python 3.10"
	case "forge":
		return "Forge"
	case "ollama":
		return "Ollama"
	default:
		return key
	}
}

func truncateProgress(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}
