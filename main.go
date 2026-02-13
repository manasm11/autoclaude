package main

import (
	"fmt"
	"os"

	"github.com/manasm11/autoclaude/internal/runner"
	"github.com/manasm11/autoclaude/internal/tui"

	tea "github.com/charmbracelet/bubbletea"
)

func main() {
	wd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error getting working directory: %v\n", err)
		os.Exit(1)
	}

	r := runner.NewRunner(wd)
	model := tui.NewModel(r)

	p := tea.NewProgram(model, tea.WithAltScreen())
	r.SetProgram(p)

	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
