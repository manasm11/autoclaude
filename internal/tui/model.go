package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/manasm11/autoclaude/internal/runner"
	"github.com/manasm11/autoclaude/internal/types"
)

// AppState represents the current screen of the TUI.
type AppState int

const (
	StateInput   AppState = iota // Adding commands
	StateQueue                   // Viewing the queue
	StateRunning                 // Execution in progress
)

// Styles
var (
	titleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#FAFAFA")).
			Background(lipgloss.Color("#7D56F4")).
			Padding(0, 1)

	inputBoxStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("#7D56F4")).
			Padding(0, 1)

	helpStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#626262"))

	promptLabelStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.Color("#00CED1"))

	verifyLabelStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.Color("#FFA500"))
)

// Model is the top-level BubbleTea model for autoclaude.
type Model struct {
	state        AppState
	commands     []*types.Command
	runner       *runner.Runner
	textInput    textarea.Model
	verifyInput  textinput.Model
	inputMode    string // "prompt" or "verify"
	width        int
	height       int
	err          error
	scrollOffset int
}

// NewModel creates a new TUI model wired to the given runner.
func NewModel(r *runner.Runner) Model {
	ta := textarea.New()
	ta.Placeholder = "Enter your Claude Code prompt..."
	ta.ShowLineNumbers = false
	ta.SetWidth(80)
	ta.SetHeight(6)
	ta.Focus()

	ti := textinput.New()
	ti.Placeholder = "e.g. go test ./... (press Enter to skip)"
	ti.Width = 80
	ti.Blur()

	return Model{
		state:     StateInput,
		commands:  make([]*types.Command, 0),
		runner:    r,
		textInput: ta,
		verifyInput: ti,
		inputMode: "prompt",
	}
}

func (m Model) Init() tea.Cmd {
	return textarea.Blink
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		return m.handleKeyMsg(msg)

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		w := m.width - 8
		if w < 20 {
			w = 20
		}
		m.textInput.SetWidth(w)
		m.verifyInput.Width = w
		return m, nil

	case runner.StatusUpdateMsg:
		if msg.CmdIndex >= 0 && msg.CmdIndex < len(m.commands) {
			m.commands[msg.CmdIndex].Status = msg.Status
			m.commands[msg.CmdIndex].Output = msg.Output
		}
		return m, nil

	case runner.AllDoneMsg:
		return m, nil

	case runner.ExecutionErrorMsg:
		m.err = msg.Err
		return m, nil
	}

	// Pass to focused input component
	var cmd tea.Cmd
	if m.state == StateInput {
		if m.inputMode == "prompt" {
			m.textInput, cmd = m.textInput.Update(msg)
		} else {
			m.verifyInput, cmd = m.verifyInput.Update(msg)
		}
	}
	return m, cmd
}

func (m Model) handleKeyMsg(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()

	// Global quit
	if key == "ctrl+q" {
		return m, tea.Quit
	}

	switch m.state {
	case StateInput:
		if m.inputMode == "prompt" {
			return m.handlePromptKey(msg)
		}
		return m.handleVerifyKey(msg)

	case StateQueue:
		return m.handleQueueKey(msg)

	case StateRunning:
		// Only ctrl+q works, handled above
		return m, nil
	}

	return m, nil
}

func (m Model) handlePromptKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()

	switch key {
	case "ctrl+s":
		prompt := strings.TrimSpace(m.textInput.Value())
		if prompt != "" {
			m.textInput.Blur()
			m.verifyInput.Focus()
			m.inputMode = "verify"
			return m, textinput.Blink
		}
		return m, nil

	case "ctrl+r":
		if len(m.commands) > 0 {
			m.state = StateRunning
			m.textInput.Blur()
			m.runner.Run()
		}
		return m, nil

	case "tab":
		m.textInput.Blur()
		m.state = StateQueue
		return m, nil
	}

	// Pass to textarea
	var cmd tea.Cmd
	m.textInput, cmd = m.textInput.Update(msg)
	return m, cmd
}

func (m Model) handleVerifyKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()

	switch key {
	case "enter":
		prompt := strings.TrimSpace(m.textInput.Value())
		verify := strings.TrimSpace(m.verifyInput.Value())

		cmd := types.NewCommand(prompt)
		cmd.Verify = verify
		m.commands = append(m.commands, cmd)
		m.runner.AddCommand(cmd)

		// Reset inputs
		m.textInput.Reset()
		m.verifyInput.Reset()
		m.inputMode = "prompt"
		m.verifyInput.Blur()
		m.textInput.Focus()
		return m, textarea.Blink

	case "esc":
		m.verifyInput.Reset()
		m.inputMode = "prompt"
		m.verifyInput.Blur()
		m.textInput.Focus()
		return m, textarea.Blink
	}

	// Pass to textinput
	var cmd tea.Cmd
	m.verifyInput, cmd = m.verifyInput.Update(msg)
	return m, cmd
}

func (m Model) handleQueueKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()

	switch key {
	case "tab", "esc":
		m.state = StateInput
		m.textInput.Focus()
		return m, textarea.Blink

	case "ctrl+r":
		if len(m.commands) > 0 {
			m.state = StateRunning
			m.runner.Run()
		}
		return m, nil
	}

	return m, nil
}

func (m Model) View() string {
	var b strings.Builder

	b.WriteString(titleStyle.Render("autoclaude"))
	b.WriteString("\n\n")

	switch m.state {
	case StateInput:
		b.WriteString(m.viewInput())
	case StateQueue:
		b.WriteString(m.viewQueue())
	case StateRunning:
		b.WriteString(m.viewRunning())
	}

	return b.String()
}

func (m Model) viewInput() string {
	var b strings.Builder

	if m.inputMode == "prompt" {
		b.WriteString(promptLabelStyle.Render("Claude Prompt:"))
		b.WriteString("\n")
		b.WriteString(inputBoxStyle.Render(m.textInput.View()))
		b.WriteString("\n\n")

		queueCount := len(m.commands)
		if queueCount > 0 {
			b.WriteString(fmt.Sprintf("Queue: %d command(s) ready\n\n", queueCount))
		}

		b.WriteString(helpStyle.Render("ctrl+s: submit prompt  |  tab: view queue  |  ctrl+r: run all  |  ctrl+q: quit"))
	} else {
		// Verify mode — show truncated prompt and verify input
		prompt := strings.TrimSpace(m.textInput.Value())
		display := truncate(prompt, 60)
		b.WriteString(promptLabelStyle.Render("Prompt: "))
		b.WriteString(display)
		b.WriteString("\n\n")

		b.WriteString(verifyLabelStyle.Render("Verification Command (optional):"))
		b.WriteString("\n")
		b.WriteString(inputBoxStyle.Render(m.verifyInput.View()))
		b.WriteString("\n\n")

		b.WriteString(helpStyle.Render("enter: add to queue  |  esc: back to prompt  |  ctrl+q: quit"))
	}

	return b.String()
}

func (m Model) viewQueue() string {
	var b strings.Builder

	if len(m.commands) == 0 {
		b.WriteString("No commands in queue.\n\n")
	} else {
		for i, cmd := range m.commands {
			icon := statusIcon(cmd.Status)
			prompt := truncate(cmd.Prompt, 50)
			b.WriteString(fmt.Sprintf("  %s %d. %s", icon, i+1, prompt))
			if cmd.Verify != "" {
				b.WriteString(fmt.Sprintf("  [verify: %s]", truncate(cmd.Verify, 30)))
			}
			b.WriteString(fmt.Sprintf("  (%s)\n", cmd.Status))
		}
		b.WriteString("\n")
	}

	b.WriteString(helpStyle.Render("tab/esc: back to input  |  ctrl+r: run all  |  ctrl+q: quit"))

	return b.String()
}

func (m Model) viewRunning() string {
	var b strings.Builder

	for i, cmd := range m.commands {
		icon := statusIcon(cmd.Status)
		prompt := truncate(cmd.Prompt, 50)
		b.WriteString(fmt.Sprintf("  %s %d. %s  (%s)\n", icon, i+1, prompt, cmd.Status))
	}

	if m.err != nil {
		b.WriteString(fmt.Sprintf("\nError: %v\n", m.err))
	}

	b.WriteString("\n")
	b.WriteString(helpStyle.Render("ctrl+q: quit"))

	return b.String()
}

func statusIcon(s types.CommandStatus) string {
	switch s {
	case types.StatusPending:
		return "\u25cb" // ○
	case types.StatusRunning:
		return "\u25cf" // ●
	case types.StatusVerifying:
		return "\u25ce" // ◎
	case types.StatusCommitting:
		return "\u25c9" // ◉
	case types.StatusSuccess:
		return "\u2713" // ✓
	case types.StatusFailed:
		return "\u2717" // ✗
	case types.StatusRetrying:
		return "\u21bb" // ↻
	default:
		return "?"
	}
}

func truncate(s string, max int) string {
	// Replace newlines with spaces for display
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) <= max {
		return s
	}
	return s[:max-3] + "..."
}
