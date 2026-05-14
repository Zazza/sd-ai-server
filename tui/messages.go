package tui

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

type Phase int

const (
	PhaseWizard Phase = iota
	PhaseInstall
	PhaseDashboard
)

type tickMsg time.Time

type installProgressMsg struct {
	Key      string
	Progress string
}

func InstallProgressMsg(key, progress string) installProgressMsg {
	return installProgressMsg{Key: key, Progress: progress}
}

type installDoneMsg struct {
	Key   string
	Error error
}

type allInstalledMsg struct{}

type sysStatsMsg struct {
	CPUUsage float64
	RAMUsage float64
	RAMUsed  uint64
	RAMTotal uint64
}

type ServicesChangeMsg struct{}

type GPUUpdateMsg struct {
	Info GPUInfo
}

func GPUUpdateMsgFunc(info GPUInfo) GPUUpdateMsg {
	return GPUUpdateMsg{Info: info}
}

func SysStatsTick() tea.Cmd {
	return tea.Tick(3*time.Second, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}
