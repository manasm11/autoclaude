package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/manasm11/autoclaude/internal/runner"
	"github.com/manasm11/autoclaude/internal/session"
	"github.com/manasm11/autoclaude/internal/types"
)

// AppState represents the current screen of the TUI.
type AppState int

const (
	StateInput   AppState = iota // Adding commands
	StateQueue                   // Viewing the queue
	StateRunning                 // Execution in progress
	StateResume                  // Previous session detected
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

	queueRowEven = lipgloss.NewStyle().
			Background(lipgloss.Color("#2A2A2A")).
			Padding(0, 1)

	queueRowOdd = lipgloss.NewStyle().
			Background(lipgloss.Color("#333333")).
			Padding(0, 1)

	queueRowSelected = lipgloss.NewStyle().
				Background(lipgloss.Color("#7D56F4")).
				Foreground(lipgloss.Color("#FFFFFF")).
				Padding(0, 1)

	statusPending = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#808080"))

	statusPlanning = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#FFFFFF"))

	statusRunning = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#5B9BF5"))

	statusVerifying = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#00CED1"))

	statusDocumenting = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#87CEEB"))

	statusCommitting = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#DA70D6"))

	statusSuccess = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#00FF00"))

	statusFailed = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#FF4444"))

	statusFixing = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#FFD700"))

	indexStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#626262"))

	verifyStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#626262"))

	outputStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#A0A0A0"))

	viewNameStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#A0A0A0")).
			MarginLeft(1)
)

// Model is the top-level BubbleTea model for autoclaude.
type Model struct {
	state         AppState
	commands      []*types.Command
	runner        *runner.Runner
	textInput     textarea.Model
	verifyInput   textinput.Model
	inputMode     string // "prompt" or "verify"
	width         int
	height        int
	err           error
	scrollOffset  int
	selectedIndex int
	spinner       spinner.Model
	currentCmd    int
	done          bool
	outputLines   []string
	statusMsg     string
	statusDetail  string // e.g. "Attempt 2/3", shown next to spinner
	autoRun       bool
	autoLoadCount  int    // number of commands auto-loaded from detected config
	autoLoadFile   string // filename of auto-detected config (e.g. "autoclaude.toml")
	resumeSession  *session.SessionState // detected previous session (nil if none)
	resumeIndex    int                   // index where execution would resume from
	autoResume     bool                  // auto-resume without TUI prompt (--auto-run)
	resetAttempts  bool                  // --reset-attempts: reset retry budget on resume
	failureReport    string // Full failure report text from runner
	failedCmdIndex   int    // Index of the failed command (-1 if none)
	showExpandedLog  bool   // 'l' toggle: show all attempts' full stdout/stderr
	failureScrollOff int    // Scroll offset for failure panel viewport
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

	s := spinner.New()
	s.Spinner = spinner.MiniDot
	s.Style = statusRunning

	return Model{
		state:          StateInput,
		commands:       make([]*types.Command, 0),
		runner:         r,
		textInput:      ta,
		verifyInput:    ti,
		inputMode:      "prompt",
		spinner:        s,
		currentCmd:     -1,
		failedCmdIndex: -1,
	}
}

// SetCommands pre-loads commands into the TUI model (e.g. from CLI flags).
func (m *Model) SetCommands(cmds []*types.Command) {
	m.commands = cmds
}

// SetStatusMsg sets a status message to display on startup (e.g. command load summary).
func (m *Model) SetStatusMsg(msg string) {
	m.statusMsg = msg
}

// SetAutoLoadInfo records that commands were auto-loaded from a detected config file.
func (m *Model) SetAutoLoadInfo(count int, filename string) {
	m.autoLoadCount = count
	m.autoLoadFile = filename
}

// SetAutoRun configures the model to skip queue review and start execution on init.
func (m *Model) SetAutoRun() {
	m.autoRun = true
}

// SetResumeSession configures the model to show the resume screen on startup.
func (m *Model) SetResumeSession(sess *session.SessionState) {
	m.resumeSession = sess
	// Find the first non-Success command index
	m.resumeIndex = len(sess.Commands) // default: past end (all succeeded)
	for i, sc := range sess.Commands {
		if types.ParseCommandStatus(sc.Status) != types.StatusSuccess {
			m.resumeIndex = i
			break
		}
	}
}

// SetAutoResume configures the model to automatically resume a session without showing the TUI prompt.
func (m *Model) SetAutoResume(sess *session.SessionState, resumeIndex int) {
	m.resumeSession = sess
	m.resumeIndex = resumeIndex
	m.autoResume = true
}

// SetResetAttempts configures the model to reset attempt counters on resume, giving a full fresh retry budget.
func (m *Model) SetResetAttempts() {
	m.resetAttempts = true
}

// Init returns the initial command for the BubbleTea program.
func (m Model) Init() tea.Cmd {
	if m.resumeSession != nil {
		if m.autoResume {
			return func() tea.Msg {
				return resumeRunMsg{}
			}
		}
		return func() tea.Msg {
			return showResumeMsg{}
		}
	}
	if m.autoRun && len(m.commands) > 0 {
		return func() tea.Msg {
			return autoRunMsg{}
		}
	}
	if len(m.commands) > 0 {
		return func() tea.Msg {
			return showQueueMsg{}
		}
	}
	return textarea.Blink
}

// autoRunMsg triggers immediate execution, skipping the TUI queue review.
type autoRunMsg struct{}

// showQueueMsg switches to queue view for reviewing pre-loaded commands.
type showQueueMsg struct{}

// showResumeMsg switches to the resume screen when a previous session is detected.
type showResumeMsg struct{}

// resumeRunMsg triggers execution from the resume point.
type resumeRunMsg struct{}

// newSessionMsg discards the old session and proceeds with normal startup.
type newSessionMsg struct{}

// Update handles all incoming messages and returns the updated model.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case autoRunMsg:
		m.state = StateRunning
		m.done = false
		m.currentCmd = -1
		m.scrollOffset = 0
		m.outputLines = nil
		m.runner.Run()
		return m, m.spinner.Tick

	case showQueueMsg:
		m.state = StateQueue
		m.textInput.Blur()
		return m, nil

	case showResumeMsg:
		m.state = StateResume
		m.textInput.Blur()
		return m, nil

	case resumeRunMsg:
		// Load commands from session, prepare runner, and start execution
		sess := m.resumeSession
		cmds := session.ToCommands(sess)

		// Reset the resume-from command to pending, preserving retry budget
		if m.resumeIndex < len(cmds) {
			cmds[m.resumeIndex].Status = types.StatusPending
			if m.resetAttempts {
				cmds[m.resumeIndex].Attempts = 0
				cmds[m.resumeIndex].AttemptLogs = nil
				cmds[m.resumeIndex].FixAttempts = 0
				cmds[m.resumeIndex].LastFailedStep = ""
				cmds[m.resumeIndex].LastExitCode = 0
				cmds[m.resumeIndex].LastStderr = ""
				cmds[m.resumeIndex].LastStdout = ""
			} else {
				cmds[m.resumeIndex].Attempts = len(cmds[m.resumeIndex].AttemptLogs)
			}
		}

		m.commands = cmds
		m.runner.Commands = cmds
		m.runner.MaxRetries = sess.MaxRetries
		m.runner.CurrentIndex = m.resumeIndex

		m.resumeSession = nil
		m.state = StateRunning
		m.done = false
		m.currentCmd = -1
		m.scrollOffset = 0
		m.outputLines = nil
		m.runner.RunFrom(m.resumeIndex)
		return m, m.spinner.Tick

	case newSessionMsg:
		// Discard old session, proceed with normal startup
		session.Clear(m.runner.WorkDir)
		m.resumeSession = nil
		if len(m.commands) > 0 {
			m.state = StateQueue
		} else {
			m.state = StateInput
			m.textInput.Focus()
			return m, textarea.Blink
		}
		return m, nil

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
		taHeight := m.height * 3 / 10
		if taHeight < 3 {
			taHeight = 3
		}
		if taHeight > 12 {
			taHeight = 12
		}
		m.textInput.SetHeight(taHeight)
		return m, nil

	case spinner.TickMsg:
		if m.state == StateRunning && !m.done {
			var cmd tea.Cmd
			m.spinner, cmd = m.spinner.Update(msg)
			return m, cmd
		}
		return m, nil

	case runner.OutputLineMsg:
		if msg.CmdIndex == m.currentCmd {
			m.outputLines = append(m.outputLines, msg.Line)
			maxOff := m.maxScrollOffset()
			if m.scrollOffset >= maxOff-5 || m.scrollOffset == 0 {
				m.scrollOffset = m.maxScrollOffset()
			}
		}
		return m, nil

	case runner.StatusUpdateMsg:
		if msg.CmdIndex >= 0 && msg.CmdIndex < len(m.commands) {
			m.commands[msg.CmdIndex].Status = msg.Status
			m.commands[msg.CmdIndex].Output = msg.Output
			m.statusDetail = msg.StatusDetail

			if (msg.Status == types.StatusPlanning || msg.Status == types.StatusRunning || msg.Status == types.StatusVerifying ||
				msg.Status == types.StatusDocumenting || msg.Status == types.StatusCommitting || msg.Status == types.StatusFixing) &&
				msg.CmdIndex != m.currentCmd {
				m.currentCmd = msg.CmdIndex
				m.scrollOffset = 0
				m.outputLines = nil
			}

			// Update spinner color to match current status
			m.spinner.Style = statusStyleFor(msg.Status)

			// On terminal statuses, reconcile with full output
			if msg.Status == types.StatusSuccess || msg.Status == types.StatusFailed {
				if msg.CmdIndex == m.currentCmd && msg.Output != "" {
					m.outputLines = strings.Split(msg.Output, "\n")
					maxOff := m.maxScrollOffset()
					if m.scrollOffset >= maxOff-3 || m.scrollOffset == 0 {
						m.scrollOffset = maxOff
					}
				}
			}
		}
		return m, nil

	case runner.AllDoneMsg:
		m.done = true
		return m, nil

	case runner.ExecutionErrorMsg:
		m.err = msg.Err
		m.failureReport = msg.FailureReport
		m.failedCmdIndex = msg.CmdIndex
		m.failureScrollOff = 0
		m.showExpandedLog = false
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
		if m.state == StateRunning && !m.done {
			m.runner.Cancel()
		}
		return m, tea.Quit
	}

	switch m.state {
	case StateResume:
		return m.handleResumeKey(msg)

	case StateInput:
		if m.inputMode == "prompt" {
			return m.handlePromptKey(msg)
		}
		return m.handleVerifyKey(msg)

	case StateQueue:
		return m.handleQueueKey(msg)

	case StateRunning:
		return m.handleRunningKey(msg)
	}

	return m, nil
}

func (m Model) handleResumeKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()

	switch key {
	case "r":
		return m, func() tea.Msg { return resumeRunMsg{} }
	case "n":
		return m, func() tea.Msg { return newSessionMsg{} }
	case "q":
		return m, tea.Quit
	}

	return m, nil
}

func (m Model) handlePromptKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	m.statusMsg = ""

	switch key {
	case "ctrl+s":
		prompt := strings.TrimSpace(m.textInput.Value())
		if prompt != "" {
			m.textInput.Blur()
			m.verifyInput.Focus()
			m.inputMode = "verify"
			return m, textinput.Blink
		}
		m.statusMsg = "Prompt cannot be empty"
		return m, nil

	case "ctrl+r":
		if len(m.commands) > 0 {
			m.state = StateRunning
			m.textInput.Blur()
			m.done = false
			m.currentCmd = -1
			m.scrollOffset = 0
			m.outputLines = nil
			m.runner.Run()
			return m, m.spinner.Tick
		}
		return m, nil

	case "tab":
		m.textInput.Blur()
		m.state = StateQueue
		if m.selectedIndex >= len(m.commands) {
			m.selectedIndex = len(m.commands) - 1
		}
		if m.selectedIndex < 0 {
			m.selectedIndex = 0
		}
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
			m.done = false
			m.currentCmd = -1
			m.scrollOffset = 0
			m.outputLines = nil
			m.runner.Run()
			return m, m.spinner.Tick
		}
		return m, nil

	case "j", "down":
		if m.selectedIndex < len(m.commands)-1 {
			m.selectedIndex++
		}
		return m, nil

	case "k", "up":
		if m.selectedIndex > 0 {
			m.selectedIndex--
		}
		return m, nil

	case "d":
		if len(m.commands) > 0 && m.selectedIndex < len(m.commands) {
			m.commands = append(m.commands[:m.selectedIndex], m.commands[m.selectedIndex+1:]...)
			m.runner.RemoveCommand(m.selectedIndex)
			if m.selectedIndex >= len(m.commands) && m.selectedIndex > 0 {
				m.selectedIndex = len(m.commands) - 1
			}
		}
		return m, nil
	}

	return m, nil
}

func (m Model) handleRunningKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()

	switch key {
	case "q":
		if m.done {
			return m, tea.Quit
		}
		return m, nil
	case "ctrl+c":
		m.runner.Cancel()
		return m, tea.Quit
	case "l":
		if m.done && m.failedCmdIndex >= 0 {
			m.showExpandedLog = !m.showExpandedLog
			m.failureScrollOff = 0
		}
		return m, nil
	case "up", "k":
		if m.done && m.failedCmdIndex >= 0 {
			if m.failureScrollOff > 0 {
				m.failureScrollOff--
			}
		} else {
			if m.scrollOffset > 0 {
				m.scrollOffset--
			}
		}
		return m, nil
	case "down", "j":
		if m.done && m.failedCmdIndex >= 0 {
			m.failureScrollOff++
		} else {
			max := m.maxScrollOffset()
			if m.scrollOffset < max {
				m.scrollOffset++
			}
		}
		return m, nil
	}

	return m, nil
}

func (m Model) outputViewportHeight() int {
	reserved := 10 + len(m.commands) - 1
	if reserved < 10 {
		reserved = 10
	}
	h := m.height - reserved
	if h < 3 {
		h = 3
	}
	return h
}

func (m Model) maxScrollOffset() int {
	vpHeight := m.outputViewportHeight()
	max := len(m.outputLines) - vpHeight
	if max < 0 {
		return 0
	}
	return max
}

func (m Model) failureViewportHeight() int {
	// title(2) + summary header(4) + command list + failure panel chrome(8) + help bar(2)
	reserved := 2 + 4 + len(m.commands) + 8 + 2
	h := m.height - reserved
	if h < 3 {
		h = 3
	}
	return h
}

func (m Model) viewFailurePanel() string {
	var b strings.Builder

	if m.failedCmdIndex < 0 || m.failedCmdIndex >= len(m.commands) {
		return ""
	}

	cmd := m.commands[m.failedCmdIndex]

	// Section header — highlight auto-fix attempts
	if cmd.FixAttempts > 0 {
		b.WriteString(statusFailed.Render(fmt.Sprintf("═══ Command failed after %d auto-fix attempt(s) ═══", cmd.FixAttempts)))
	} else {
		b.WriteString(statusFailed.Render("═══ FAILURE DETAILS ═══"))
	}
	b.WriteString("\n\n")

	// Info fields
	prompt := truncate(cmd.Prompt, 80)
	b.WriteString(fmt.Sprintf("  Command #%d:  %q\n", m.failedCmdIndex+1, prompt))
	b.WriteString(fmt.Sprintf("  Attempts:    %d / %d\n", cmd.Attempts, cmd.MaxRetries))

	if len(cmd.AttemptLogs) > 0 {
		lastAttempt := cmd.AttemptLogs[len(cmd.AttemptLogs)-1]
		failedStep := lastAttempt.FailedStep
		if failedStep == "" {
			failedStep = "(unknown)"
		}
		b.WriteString(fmt.Sprintf("  Last failed: %s\n", failedStep))
		b.WriteString(fmt.Sprintf("  Exit code:   %d\n", lastAttempt.ExitCode))
	} else {
		b.WriteString("  No attempt data available\n")
	}

	// Per-attempt summary
	if len(cmd.AttemptLogs) > 1 {
		b.WriteString("\n")
		b.WriteString(helpStyle.Render("  Attempt history:"))
		b.WriteString("\n")
		for _, a := range cmd.AttemptLogs {
			step := a.FailedStep
			if step == "" {
				step = "success"
			}
			dur := a.Duration.Round(time.Millisecond).String()
			b.WriteString(fmt.Sprintf("    #%d  %s  exit=%d  %s\n", a.AttemptNumber, step, a.ExitCode, helpStyle.Render(dur)))
		}
	}

	b.WriteString("\n")

	// Scrollable content area
	vpHeight := m.failureViewportHeight()
	var contentLines []string

	if m.showExpandedLog {
		// Expanded: full failure report
		b.WriteString(statusFailed.Render("─── Full failure report ───"))
		b.WriteString("\n")
		if m.failureReport != "" {
			contentLines = strings.Split(m.failureReport, "\n")
		} else {
			contentLines = []string{"(no report available)"}
		}
	} else {
		// Compact: last attempt stderr
		b.WriteString(statusFailed.Render("─── Last attempt stderr ───"))
		b.WriteString("\n")
		if len(cmd.AttemptLogs) > 0 {
			lastAttempt := cmd.AttemptLogs[len(cmd.AttemptLogs)-1]
			if lastAttempt.Stderr != "" {
				contentLines = strings.Split(lastAttempt.Stderr, "\n")
			} else {
				contentLines = []string{"(no stderr captured)"}
			}
		} else {
			contentLines = []string{"(no attempt data available)"}
		}
	}

	// Cap scroll offset
	maxOff := len(contentLines) - vpHeight
	if maxOff < 0 {
		maxOff = 0
	}
	scrollOff := m.failureScrollOff
	if scrollOff > maxOff {
		scrollOff = maxOff
	}

	// Top scroll indicator
	if scrollOff > 0 {
		b.WriteString(helpStyle.Render(fmt.Sprintf("  --- %d lines above ---", scrollOff)))
		b.WriteString("\n")
	}

	// Visible lines
	end := scrollOff + vpHeight
	if end > len(contentLines) {
		end = len(contentLines)
	}

	maxWidth := m.width - 4
	if maxWidth < 40 {
		maxWidth = 40
	}

	for i := scrollOff; i < end; i++ {
		line := contentLines[i]
		if len(line) > maxWidth {
			line = line[:maxWidth]
		}
		b.WriteString(outputStyle.Render("  " + line))
		b.WriteString("\n")
	}

	// Bottom scroll indicator
	remaining := len(contentLines) - end
	if remaining > 0 {
		b.WriteString(helpStyle.Render(fmt.Sprintf("  --- %d lines below ---", remaining)))
		b.WriteString("\n")
	}

	b.WriteString("\n")
	b.WriteString(helpStyle.Render("Full report written to autoclaude-error.log"))

	return b.String()
}

func (m Model) viewName() string {
	switch m.state {
	case StateInput:
		return "Input"
	case StateQueue:
		return "Queue"
	case StateRunning:
		return "Running"
	case StateResume:
		return "Resume"
	default:
		return ""
	}
}

// View renders the current state of the TUI as a string.
func (m Model) View() string {
	var b strings.Builder

	b.WriteString(titleStyle.Render("autoclaude"))
	b.WriteString(viewNameStyle.Render(m.viewName()))
	b.WriteString("\n\n")

	switch m.state {
	case StateResume:
		b.WriteString(m.viewResume())
	case StateInput:
		b.WriteString(m.viewInput())
	case StateQueue:
		b.WriteString(m.viewQueue())
	case StateRunning:
		b.WriteString(m.viewRunning())
	}

	return b.String()
}

func (m Model) viewResume() string {
	var b strings.Builder
	sess := m.resumeSession
	if sess == nil {
		b.WriteString("No session data.\n")
		return b.String()
	}

	// Header with relative timestamp
	headerStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#FFA500"))
	b.WriteString(headerStyle.Render("Previous session found"))
	b.WriteString("  ")
	b.WriteString(helpStyle.Render(fmt.Sprintf("interrupted %s", relativeTime(sess.UpdatedAt))))
	b.WriteString("\n\n")

	// Summary counts
	total := len(sess.Commands)
	completed := 0
	failed := 0
	remaining := 0
	for _, sc := range sess.Commands {
		switch types.ParseCommandStatus(sc.Status) {
		case types.StatusSuccess:
			completed++
		case types.StatusFailed:
			failed++
		default:
			remaining++
		}
	}

	summaryStyle := lipgloss.NewStyle().Bold(true)
	b.WriteString(summaryStyle.Render(fmt.Sprintf("Total: %d", total)))
	b.WriteString("  ")
	b.WriteString(statusSuccess.Render(fmt.Sprintf("Completed: %d", completed)))
	b.WriteString("  ")
	b.WriteString(statusFailed.Render(fmt.Sprintf("Failed: %d", failed)))
	b.WriteString("  ")
	b.WriteString(statusPending.Render(fmt.Sprintf("Remaining: %d", remaining)))
	b.WriteString("\n\n")

	// Command list
	rowWidth := m.width - 2
	if rowWidth < 40 {
		rowWidth = 40
	}

	// Windowed rendering for long lists
	maxVisible := m.height - 12
	if maxVisible < 3 {
		maxVisible = 3
	}

	startIdx := 0
	endIdx := total
	if total > maxVisible {
		startIdx = m.resumeIndex - maxVisible/2
		if startIdx < 0 {
			startIdx = 0
		}
		endIdx = startIdx + maxVisible
		if endIdx > total {
			endIdx = total
			startIdx = endIdx - maxVisible
			if startIdx < 0 {
				startIdx = 0
			}
		}
	}

	if startIdx > 0 {
		b.WriteString(helpStyle.Render(fmt.Sprintf("  ... %d more above ...", startIdx)))
		b.WriteString("\n")
	}

	resumeMarker := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#FFA500"))

	for i := startIdx; i < endIdx; i++ {
		sc := sess.Commands[i]
		status := types.ParseCommandStatus(sc.Status)
		icon := styledStatusIcon(status)
		label := styledStatus(status)
		prompt := truncate(sc.Prompt, 70)
		idx := indexStyle.Render(fmt.Sprintf("%d.", i+1))

		content := fmt.Sprintf("%s %s  %s %s", idx, prompt, icon, label)

		// Show attempt history for failed/retried commands
		if len(sc.AttemptLogs) > 0 && status != types.StatusSuccess {
			content += helpStyle.Render(fmt.Sprintf("  (%d/%d attempts used)", len(sc.AttemptLogs), sc.MaxRetries))
		}

		if i == m.resumeIndex {
			content = resumeMarker.Render(">> ") + content
		} else {
			content = "   " + content
		}

		var rowStyle lipgloss.Style
		if i == m.resumeIndex {
			rowStyle = queueRowSelected.Width(rowWidth)
		} else if i%2 == 0 {
			rowStyle = queueRowEven.Width(rowWidth)
		} else {
			rowStyle = queueRowOdd.Width(rowWidth)
		}

		b.WriteString(rowStyle.Render(content))
		b.WriteString("\n")
	}

	if endIdx < total {
		b.WriteString(helpStyle.Render(fmt.Sprintf("  ... %d more below ...", total-endIdx)))
		b.WriteString("\n")
	}

	b.WriteString("\n")
	b.WriteString(helpStyle.Render("r: resume  |  n: new session  |  q: quit"))

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
		switch queueCount {
		case 0:
			b.WriteString("No commands queued\n\n")
		case 1:
			b.WriteString("1 command queued\n\n")
		default:
			b.WriteString(fmt.Sprintf("%d commands queued\n\n", queueCount))
		}

		if m.statusMsg != "" {
			b.WriteString(statusFailed.Render(m.statusMsg))
			b.WriteString("\n\n")
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

	if m.statusMsg != "" {
		b.WriteString(helpStyle.Render(m.statusMsg))
		b.WriteString("\n\n")
	}

	if m.autoLoadCount > 0 {
		info := fmt.Sprintf("Loaded %d commands from %s", m.autoLoadCount, m.autoLoadFile)
		b.WriteString(helpStyle.Render(info))
		b.WriteString("\n\n")
	}

	if len(m.commands) == 0 {
		b.WriteString("No commands in queue.\n\n")
	} else {
		// Clamp selectedIndex just in case
		sel := m.selectedIndex
		if sel >= len(m.commands) {
			sel = len(m.commands) - 1
		}
		if sel < 0 {
			sel = 0
		}

		rowWidth := m.width - 2
		if rowWidth < 40 {
			rowWidth = 40
		}

		// Windowed rendering for long queues
		maxVisible := m.height - 6
		if maxVisible < 3 {
			maxVisible = 3
		}

		startIdx := 0
		endIdx := len(m.commands)
		if len(m.commands) > maxVisible {
			// Center selected item in visible window
			startIdx = sel - maxVisible/2
			if startIdx < 0 {
				startIdx = 0
			}
			endIdx = startIdx + maxVisible
			if endIdx > len(m.commands) {
				endIdx = len(m.commands)
				startIdx = endIdx - maxVisible
				if startIdx < 0 {
					startIdx = 0
				}
			}
		}

		if startIdx > 0 {
			b.WriteString(helpStyle.Render(fmt.Sprintf("  ... %d more above ...", startIdx)))
			b.WriteString("\n")
		}

		for i := startIdx; i < endIdx; i++ {
			cmd := m.commands[i]
			icon := styledStatusIcon(cmd.Status)
			status := styledStatus(cmd.Status)
			prompt := truncate(cmd.Prompt, 80)
			idx := indexStyle.Render(fmt.Sprintf("%d.", i+1))

			var verify string
			if cmd.Verify != "" {
				verify = verifyStyle.Render(fmt.Sprintf("[verify: %s]", truncate(cmd.Verify, 30)))
			} else {
				verify = verifyStyle.Render("[no verification]")
			}

			content := fmt.Sprintf("%s %s  %s  %s %s", idx, prompt, verify, icon, status)

			var rowStyle lipgloss.Style
			if i == sel {
				rowStyle = queueRowSelected.Width(rowWidth)
			} else if i%2 == 0 {
				rowStyle = queueRowEven.Width(rowWidth)
			} else {
				rowStyle = queueRowOdd.Width(rowWidth)
			}

			b.WriteString(rowStyle.Render(content))
			b.WriteString("\n")
		}

		if endIdx < len(m.commands) {
			b.WriteString(helpStyle.Render(fmt.Sprintf("  ... %d more below ...", len(m.commands)-endIdx)))
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}

	b.WriteString(helpStyle.Render("tab: add more | ctrl+r: run all | d: delete | j/k: navigate | ctrl+q: quit"))

	return b.String()
}

func statusStyleFor(s types.CommandStatus) lipgloss.Style {
	switch s {
	case types.StatusPending:
		return statusPending
	case types.StatusPlanning:
		return statusPlanning
	case types.StatusRunning:
		return statusRunning
	case types.StatusVerifying:
		return statusVerifying
	case types.StatusDocumenting:
		return statusDocumenting
	case types.StatusCommitting:
		return statusCommitting
	case types.StatusSuccess:
		return statusSuccess
	case types.StatusFailed:
		return statusFailed
	case types.StatusFixing:
		return statusFixing
	default:
		return lipgloss.NewStyle()
	}
}

func styledStatusIcon(s types.CommandStatus) string {
	return statusStyleFor(s).Render(statusIcon(s))
}

func styledStatus(s types.CommandStatus) string {
	return statusStyleFor(s).Render(s.String())
}

func (m Model) viewRunning() string {
	if m.done {
		return m.viewRunningDone()
	}
	return m.viewRunningLive()
}

func (m Model) viewRunningLive() string {
	var b strings.Builder

	// Progress
	total := len(m.commands)
	current := m.currentCmd + 1
	progress := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#00BFFF")).
		Render(fmt.Sprintf("Running command %d/%d", current, total))
	b.WriteString(progress)
	b.WriteString("\n\n")

	// Current command prompt and status
	if m.currentCmd >= 0 && m.currentCmd < len(m.commands) {
		cmd := m.commands[m.currentCmd]
		b.WriteString(promptLabelStyle.Render("Prompt: "))
		b.WriteString(truncate(cmd.Prompt, 200))
		b.WriteString("\n")

		// Status flow breadcrumb
		b.WriteString(renderStatusFlow(cmd.Status, cmd.FixAttempts))
		b.WriteString("\n")

		b.WriteString(m.spinner.View())
		b.WriteString(" ")
		b.WriteString(styledStatus(cmd.Status))
		if m.statusDetail != "" {
			b.WriteString("  ")
			b.WriteString(helpStyle.Render(m.statusDetail))
		}
		b.WriteString("\n")

		// Enhanced fixing view: show what failed, condensed stderr, fix attempt count
		if cmd.Status == types.StatusFixing {
			b.WriteString("\n")
			if cmd.LastFailedStep != "" {
				failedLabel := capitalize(cmd.LastFailedStep)
				b.WriteString(statusFailed.Render(fmt.Sprintf("  %s failed (exit code %d)", failedLabel, cmd.LastExitCode)))
				b.WriteString("\n")
			}
			if cmd.LastStderr != "" {
				condensed := lastNLines(cmd.LastStderr, 10)
				for _, line := range strings.Split(condensed, "\n") {
					b.WriteString(outputStyle.Render("    " + line))
					b.WriteString("\n")
				}
			}
			maxFix := cmd.MaxRetries
			if maxFix < 1 {
				maxFix = 3
			}
			b.WriteString(statusFixing.Render(fmt.Sprintf("  Fix attempt %d/%d", cmd.FixAttempts, maxFix)))
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}

	// Output viewport
	b.WriteString(m.renderOutputViewport())
	b.WriteString("\n")

	// Previous commands summary
	for i, cmd := range m.commands {
		if i >= m.currentCmd {
			break
		}
		icon := styledStatusIcon(cmd.Status)
		prompt := truncate(cmd.Prompt, 60)
		b.WriteString(fmt.Sprintf("  %s %d. %s\n", icon, i+1, prompt))
	}
	if m.currentCmd > 0 {
		b.WriteString("\n")
	}

	// Help bar
	b.WriteString(helpStyle.Render("up/down: scroll output  |  ctrl+c: force quit"))

	return b.String()
}

func (m Model) viewRunningDone() string {
	var b strings.Builder

	// Count pass/fail
	passed := 0
	failed := 0
	for _, cmd := range m.commands {
		if cmd.Status == types.StatusSuccess {
			passed++
		} else if cmd.Status == types.StatusFailed {
			failed++
		}
	}

	// Header
	if failed == 0 {
		header := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#00FF00"))
		b.WriteString(header.Render("All commands completed successfully!"))
	} else {
		header := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#FF4444"))
		b.WriteString(header.Render("Execution finished with errors"))
	}
	b.WriteString("\n\n")

	// Counts
	b.WriteString(fmt.Sprintf("%s %d passed    %s %d failed    Total: %d\n\n",
		statusSuccess.Render("✓"), passed,
		statusFailed.Render("✗"), failed,
		len(m.commands)))

	// Per-command results
	for i, cmd := range m.commands {
		icon := styledStatusIcon(cmd.Status)
		prompt := truncate(cmd.Prompt, 60)
		status := styledStatus(cmd.Status)
		b.WriteString(fmt.Sprintf("  %s %d. %s  %s\n", icon, i+1, prompt, status))
	}

	// Failure panel or generic error
	if m.failedCmdIndex >= 0 {
		b.WriteString("\n")
		b.WriteString(m.viewFailurePanel())
	} else if m.err != nil {
		b.WriteString(fmt.Sprintf("\n%s\n", statusFailed.Render(fmt.Sprintf("Error: %v", m.err))))
	}

	b.WriteString("\n")
	if m.failedCmdIndex >= 0 {
		b.WriteString(helpStyle.Render("l: toggle full log  |  up/down: scroll  |  q: quit"))
	} else {
		b.WriteString(helpStyle.Render("q: quit"))
	}

	return b.String()
}

func (m Model) renderOutputViewport() string {
	var b strings.Builder

	if len(m.outputLines) == 0 || (len(m.outputLines) == 1 && m.outputLines[0] == "") {
		b.WriteString(outputStyle.Render("  (waiting for output...)"))
		b.WriteString("\n")
		return b.String()
	}

	vpHeight := m.outputViewportHeight()
	offset := m.scrollOffset
	if offset > m.maxScrollOffset() {
		offset = m.maxScrollOffset()
	}

	// Top indicator
	if offset > 0 {
		b.WriteString(helpStyle.Render(fmt.Sprintf("  --- %d lines above ---", offset)))
		b.WriteString("\n")
	}

	// Visible lines
	end := offset + vpHeight
	if end > len(m.outputLines) {
		end = len(m.outputLines)
	}

	maxWidth := m.width - 4
	if maxWidth < 40 {
		maxWidth = 40
	}

	for i := offset; i < end; i++ {
		line := m.outputLines[i]
		if len(line) > maxWidth {
			line = line[:maxWidth]
		}
		b.WriteString(outputStyle.Render("  " + line))
		b.WriteString("\n")
	}

	// Bottom indicator
	remaining := len(m.outputLines) - end
	if remaining > 0 {
		b.WriteString(helpStyle.Render(fmt.Sprintf("  --- %d lines below ---", remaining)))
		b.WriteString("\n")
	}

	return b.String()
}

func statusIcon(s types.CommandStatus) string {
	switch s {
	case types.StatusPending:
		return "\u25cb" // ○
	case types.StatusPlanning:
		return "\u270e" // ✎
	case types.StatusRunning:
		return "\u25cf" // ●
	case types.StatusVerifying:
		return "\u25ce" // ◎
	case types.StatusDocumenting:
		return "\u270d" // ✍
	case types.StatusCommitting:
		return "\u25c9" // ◉
	case types.StatusSuccess:
		return "\u2713" // ✓
	case types.StatusFailed:
		return "\u2717" // ✗
	case types.StatusFixing:
		return "\u26a1" // ⚡
	default:
		return "?"
	}
}

// relativeTime returns a human-readable string describing how long ago t was.
func relativeTime(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < 2*time.Minute:
		return "1 minute ago"
	case d < time.Hour:
		return fmt.Sprintf("%d minutes ago", int(d.Minutes()))
	case d < 2*time.Hour:
		return "1 hour ago"
	case d < 24*time.Hour:
		return fmt.Sprintf("%d hours ago", int(d.Hours()))
	case d < 48*time.Hour:
		return "yesterday"
	default:
		return fmt.Sprintf("%d days ago", int(d.Hours()/24))
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

func capitalize(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

func lastNLines(s string, n int) string {
	lines := strings.Split(s, "\n")
	if len(lines) <= n {
		return s
	}
	return strings.Join(lines[len(lines)-n:], "\n")
}

// isStatusBefore returns true if a is before b in the execution flow order.
func isStatusBefore(a, b types.CommandStatus) bool {
	order := map[types.CommandStatus]int{
		types.StatusPending:     0,
		types.StatusPlanning:    1,
		types.StatusRunning:     2,
		types.StatusVerifying:   3,
		types.StatusFixing:      4,
		types.StatusDocumenting: 5,
		types.StatusCommitting:  6,
		types.StatusSuccess:     7,
		types.StatusFailed:      7,
	}
	return order[a] < order[b]
}

// renderStatusFlow renders a breadcrumb like: Plan → Run → Verify → [Fix → Verify]* → Docs → Commit
func renderStatusFlow(current types.CommandStatus, fixAttempts int) string {
	type step struct {
		label  string
		status types.CommandStatus
	}

	greenStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#00FF00"))
	activeStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#FFFFFF"))
	futureStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#555555"))
	sepStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#555555"))

	steps := []step{
		{"Plan", types.StatusPlanning},
		{"Run", types.StatusRunning},
		{"Verify", types.StatusVerifying},
	}

	// Insert fix→verify cycles if we've had fix attempts
	if fixAttempts > 0 || current == types.StatusFixing {
		steps = append(steps, step{"Fix", types.StatusFixing})
		steps = append(steps, step{"Verify", types.StatusVerifying})
	}

	steps = append(steps,
		step{"Docs", types.StatusDocumenting},
		step{"Commit", types.StatusCommitting},
	)

	sep := sepStyle.Render(" → ")
	var parts []string

	for _, s := range steps {
		var rendered string
		if s.status == current {
			rendered = activeStyle.Render(s.label)
		} else if isStatusBefore(s.status, current) {
			rendered = greenStyle.Render(s.label)
		} else {
			rendered = futureStyle.Render(s.label)
		}
		parts = append(parts, rendered)
	}

	return strings.Join(parts, sep)
}
