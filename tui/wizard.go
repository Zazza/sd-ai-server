package tui

import (
	"fmt"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
)

type WizardDoneMsg struct {
	DataDir string
}

type WizardResult struct {
	DataDir    string
	Components map[string]bool
}

type Component struct {
	Key    string
	Label  string
	Active bool
}

type WizardModel struct {
	dataInput  textinput.Model
	focusIndex int
	components []Component
	submitted  bool
}

func NewWizardModel(defaultDir string) WizardModel {
	ti := textinput.New()
	ti.Placeholder = defaultDir
	ti.Focus()
	ti.CharLimit = 256
	ti.Width = 40

	return WizardModel{
		dataInput: ti,
		components: []Component{
			{Key: "python", Label: "Python 3.10", Active: true},
			{Key: "forge", Label: "Stable Diffusion Forge", Active: true},
			{Key: "ollama", Label: "Ollama (LLM)", Active: true},
		},
		focusIndex: 0,
	}
}

func (m WizardModel) Init() tea.Cmd {
	return textinput.Blink
}

func (m WizardModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.Type {
		case tea.KeyUp:
			if m.focusIndex > 0 {
				m.focusIndex--
			}
			m.updateFocus()
			return m, nil
		case tea.KeyDown:
			if m.focusIndex < len(m.components) {
				m.focusIndex++
			}
			m.updateFocus()
			return m, nil
		case tea.KeyEnter:
			if m.focusIndex == 0 {
				m.focusIndex = 1
				m.updateFocus()
				return m, nil
			}
			if m.focusIndex == len(m.components) {
				m.submitted = true
				dir := m.dataInput.Value()
				if dir == "" {
					dir = m.dataInput.Placeholder
				}
				return m, func() tea.Msg {
					return WizardDoneMsg{DataDir: dir}
				}
			}
			return m, nil
		case tea.KeySpace:
			if m.focusIndex > 0 && m.focusIndex <= len(m.components) {
				idx := m.focusIndex - 1
				m.components[idx].Active = !m.components[idx].Active
			}
			return m, nil
		}
	}

	var cmd tea.Cmd
	m.dataInput, cmd = m.dataInput.Update(msg)
	return m, cmd
}

func (m *WizardModel) updateFocus() {
	if m.focusIndex == 0 {
		m.dataInput.Focus()
	} else {
		m.dataInput.Blur()
	}
}

func (m WizardModel) View() string {
	var b strings.Builder

	b.WriteString(titleStyle.Render(" SD Studio Server — Setup "))
	b.WriteString("\n\n")

	b.WriteString(labelStyle.Render("Data directory:"))
	b.WriteString("\n")

	dirStr := m.dataInput.View()
	if m.focusIndex == 0 {
		dirStr = selectedStyle.Render("> " + dirStr)
	} else {
		dirStr = "  " + dirStr
	}
	b.WriteString(dirStr)

	if m.focusIndex == 0 {
		dir := m.dataInput.Value()
		if dir == "" {
			dir = m.dataInput.Placeholder
		}
		if runtime.GOOS == "windows" {
			abs, _ := filepath.Abs(dir)
			if strings.HasPrefix(strings.ToUpper(abs), "C:") {
				b.WriteString("\n")
				b.WriteString(progressYellow.Render("  Warning: Installing on C: drive may use significant disk space"))
			}
		}
	}

	b.WriteString("\n\n")
	b.WriteString(labelStyle.Render("Select components to install:"))
	b.WriteString("\n")

	for i, comp := range m.components {
		cursor := " "
		if m.focusIndex == i+1 {
			cursor = ">"
		}

		checkbox := "[ ]"
		if comp.Active {
			checkbox = checkedStyle.Render("[x]")
		} else {
			checkbox = checkboxStyle.Render("[ ]")
		}

		line := fmt.Sprintf("%s %s %s", cursor, checkbox, comp.Label)
		if m.focusIndex == i+1 {
			line = selectedStyle.Render(line)
		}
		b.WriteString(line)
		b.WriteString("\n")
	}

	b.WriteString("\n")
	if m.focusIndex == len(m.components) {
		b.WriteString(selectedStyle.Render("> Confirm"))
	} else {
		b.WriteString(helpStyle.Render("  Enter to confirm"))
	}

	b.WriteString("\n\n")
	b.WriteString(helpStyle.Render("↑/↓ navigate  •  Space toggle  •  Enter confirm  •  Ctrl+C quit"))

	return borderStyle.Render(b.String())
}

func (m WizardModel) ActiveComponents() map[string]bool {
	result := make(map[string]bool, len(m.components))
	for _, c := range m.components {
		result[c.Key] = c.Active
	}
	return result
}

type wizardRunner struct {
	wizard WizardModel
	result WizardResult
	done   bool
}

func (m *wizardRunner) Init() tea.Cmd {
	return m.wizard.Init()
}

func (m *wizardRunner) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case WizardDoneMsg:
		m.result = WizardResult{
			DataDir:    msg.DataDir,
			Components: m.wizard.ActiveComponents(),
		}
		m.done = true
		return m, tea.Quit
	case tea.KeyMsg:
		if msg.String() == "ctrl+c" {
			m.done = true
			return m, tea.Quit
		}
	}
	w, cmd := m.wizard.Update(msg)
	m.wizard = w.(WizardModel)
	return m, cmd
}

func (m *wizardRunner) View() string {
	if m.done {
		return ""
	}
	return m.wizard.View()
}

func RunWizard(defaultDir string) WizardResult {
	runner := &wizardRunner{
		wizard: NewWizardModel(defaultDir),
	}
	p := tea.NewProgram(runner, tea.WithAltScreen())
	if final, err := p.Run(); err == nil {
		if r, ok := final.(*wizardRunner); ok {
			return r.result
		}
	}
	return WizardResult{DataDir: defaultDir}
}
