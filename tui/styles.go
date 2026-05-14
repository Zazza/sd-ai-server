package tui

import (
	"github.com/charmbracelet/lipgloss"
)

var (
	titleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#FAFAFA")).
			Background(lipgloss.Color("#7D56F4")).
			Padding(0, 2)

	subtitleStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#7D56F4")).
			Bold(true)

	labelStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#888888"))

	valueStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#FAFAFA"))

	runningStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#04B575")).Bold(true)

	stoppedStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#FF4672"))

	crashedStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#FF4672"))

	healthyStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#04B575"))

	unhealthyStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#FF4672"))

	progressGreen = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#04B575"))

	progressBlue = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#3B82F6"))

	progressYellow = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#F59E0B"))

	progressRed = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#FF4672"))

	progressGray = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#444444"))

	selectedStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#7D56F4")).Bold(true)

	helpStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#666666"))

	borderStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("#333333")).
			Padding(1, 2)

	inputPromptStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#7D56F4"))

	inputStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#FAFAFA"))

	checkboxStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#FAFAFA"))

	checkedStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#04B575"))

	barWidth = 30
)
