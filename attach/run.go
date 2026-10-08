package attach

import (
	"context"
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"
	"golang.org/x/term"

	"sd-studio-server/tui"
)

func Main(args []string) error {
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return fmt.Errorf("stdin is not a terminal")
	}
	if len(args) > 1 {
		return fmt.Errorf("usage: attach [host[:port]]")
	}

	arg := ""
	if len(args) == 1 {
		arg = args[0]
	}
	target, err := ParseTarget(arg)
	if err != nil {
		return err
	}

	client := NewClient(target)
	if err := client.Preflight(); err != nil {
		return fmt.Errorf("daemon at %s unreachable: %w", target.Base, err)
	}

	p := tea.NewProgram(tui.NewAttachModel(client.Deps(), target.HostPort()), tea.WithAltScreen())
	client.Notify(p.Send)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go client.Run(ctx)

	if _, err := p.Run(); err != nil {
		return fmt.Errorf("tui: %w", err)
	}
	cancel()
	return nil
}
