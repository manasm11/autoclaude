package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/manasm11/autoclaude/internal/runner"
	"github.com/manasm11/autoclaude/internal/tui"

	tea "github.com/charmbracelet/bubbletea"
)

func main() {
	maxRetries := flag.Int("max-retries", 3, "maximum number of retries per command")
	workDir := flag.String("work-dir", "", "working directory for command execution (defaults to current directory)")
	flag.Parse()

	var wd string
	var err error
	if *workDir != "" {
		wd, err = filepath.Abs(*workDir)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error resolving working directory: %v\n", err)
			os.Exit(1)
		}
	} else {
		wd, err = os.Getwd()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error getting working directory: %v\n", err)
			os.Exit(1)
		}
	}

	if _, err := os.Stat(wd); err != nil {
		fmt.Fprintf(os.Stderr, "Error: working directory is not accessible: %v\n", err)
		os.Exit(1)
	}

	if _, err := exec.LookPath("claude"); err != nil {
		fmt.Fprintln(os.Stderr, "Error: 'claude' CLI not found in PATH.")
		fmt.Fprintln(os.Stderr, "Install Claude Code: https://docs.anthropic.com/en/docs/claude-code")
		os.Exit(1)
	}

	r := runner.NewRunner(wd)
	r.MaxRetries = *maxRetries
	model := tui.NewModel(r)

	p := tea.NewProgram(model, tea.WithAltScreen())
	r.SetProgram(p)

	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
