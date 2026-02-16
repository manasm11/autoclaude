package runner

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/manasm11/autoclaude/internal/session"
	"github.com/manasm11/autoclaude/internal/types"
)

// StatusUpdateMsg is sent to the TUI when a command's status changes.
type StatusUpdateMsg struct {
	CmdIndex int
	Status   types.CommandStatus
	Output   string
}

// AllDoneMsg is sent when all commands have finished (or one has permanently failed).
type AllDoneMsg struct{}

// ExecutionErrorMsg is sent when a command encounters an unrecoverable error.
type ExecutionErrorMsg struct {
	CmdIndex int
	Err      error
}

// OutputLineMsg is sent to the TUI for each line of output from a running command.
type OutputLineMsg struct {
	CmdIndex int
	Line     string
}

// CommandResult holds the structured output from a command execution.
type CommandResult struct {
	Stdout   string
	Stderr   string
	ExitCode int
}

// Runner manages sequential execution of commands.
type Runner struct {
	Commands     []*types.Command
	CurrentIndex int
	WorkDir      string
	MaxRetries   int
	NoDocs       bool
	program      *tea.Program
	ctx          context.Context
	cancel       context.CancelFunc
}

// NewRunner creates a new Runner with the given working directory.
func NewRunner(workDir string) *Runner {
	ctx, cancel := context.WithCancel(context.Background())
	return &Runner{
		Commands:   make([]*types.Command, 0),
		WorkDir:    workDir,
		MaxRetries: 3,
		ctx:        ctx,
		cancel:     cancel,
	}
}

// SetProgram stores the BubbleTea program reference for sending messages.
func (r *Runner) SetProgram(p *tea.Program) {
	r.program = p
}

// AddCommand appends a command to the execution queue.
func (r *Runner) AddCommand(cmd *types.Command) {
	if cmd.MaxRetries <= 0 {
		cmd.MaxRetries = r.MaxRetries
	}
	r.Commands = append(r.Commands, cmd)
}

// Cancel cancels the runner's context, stopping any in-progress command execution.
func (r *Runner) Cancel() {
	r.cancel()
}

// RemoveCommand removes a command by index from the execution queue.
func (r *Runner) RemoveCommand(index int) {
	if index < 0 || index >= len(r.Commands) {
		return
	}
	r.Commands = append(r.Commands[:index], r.Commands[index+1:]...)
}

// Run launches command execution in a background goroutine and returns immediately.
func (r *Runner) Run() {
	go r.executeAll()
}

// RunFrom launches command execution starting from the given index, skipping earlier commands.
func (r *Runner) RunFrom(startIndex int) {
	go r.executeFrom(startIndex)
}

func (r *Runner) executeAll() {
	for i := range r.Commands {
		r.CurrentIndex = i
		if !r.executeSingle(i, r.Commands[i]) {
			r.sendAllDone()
			return
		}
	}

	// All commands completed successfully — clear session file since no resume is needed
	session.Clear(r.WorkDir)
	r.sendAllDone()
}

func (r *Runner) executeFrom(startIndex int) {
	for i := startIndex; i < len(r.Commands); i++ {
		r.CurrentIndex = i
		if !r.executeSingle(i, r.Commands[i]) {
			r.sendAllDone()
			return
		}
	}

	session.Clear(r.WorkDir)
	r.sendAllDone()
}

// executeSingle runs the full plan-execute-verify-commit cycle for a single command.
// Returns true if the command succeeded, false if it permanently failed.
func (r *Runner) executeSingle(i int, cmd *types.Command) bool {
	success := false
	for cmd.Attempts < cmd.MaxRetries {
		cmd.Attempts++

		// 1. PLANNING — skip if we already have a plan (e.g. resumed session)
		if cmd.PlanOutput == "" {
			cmd.Status = types.StatusPlanning
			r.sendUpdate(i, types.StatusPlanning, "")
			r.saveSession()

			planPrompt := "You are planning the implementation of a task. Create a detailed step-by-step implementation plan. List every file to create or modify, what exact changes to make in each file, function signatures, and edge cases to handle. Do NOT write any code, do NOT create or modify any files. Only output the plan in markdown.\n\nTask: " + cmd.Prompt
			planOutput, planErr := r.runClaude(i, planPrompt)
			if planErr != nil {
				if cmd.Attempts < cmd.MaxRetries {
					cmd.Status = types.StatusRetrying
					r.sendUpdate(i, types.StatusRetrying, planOutput)
					r.saveSession()
					continue
				}
				cmd.Status = types.StatusFailed
				cmd.Output = planOutput
				r.sendUpdate(i, types.StatusFailed, planOutput)
				r.saveSession()
				return false
			}
			cmd.PlanOutput = planOutput
			r.saveSession()
		}

		// 2. EXECUTION
		cmd.Status = types.StatusRunning
		cmd.Output = "═══ PLAN ═══\n" + cmd.PlanOutput + "\n═══ EXECUTION ═══\n"
		r.sendUpdate(i, types.StatusRunning, cmd.Output)
		r.saveSession()

		execPrompt := "Execute the following implementation plan exactly. Follow each step precisely.\n\nPlan:\n" + cmd.PlanOutput + "\n\nOriginal task: " + cmd.Prompt
		execOutput, execErr := r.runClaude(i, execPrompt)
		cmd.Output = cmd.Output + execOutput

		if execErr != nil {
			cmd.PlanOutput = "" // fresh plan on retry
			if cmd.Attempts < cmd.MaxRetries {
				cmd.Status = types.StatusRetrying
				r.sendUpdate(i, types.StatusRetrying, cmd.Output)
				r.saveSession()
				continue
			}
			cmd.Status = types.StatusFailed
			r.sendUpdate(i, types.StatusFailed, cmd.Output)
			r.saveSession()
			return false
		}

		// 3. VERIFICATION
		if cmd.Verify != "" {
			cmd.Status = types.StatusVerifying
			r.sendUpdate(i, types.StatusVerifying, cmd.Output)
			r.saveSession()

			verifyOutput, verifyErr := r.runVerify(i, cmd.Verify)
			cmd.Output = cmd.Output + "\n" + verifyOutput

			if verifyErr != nil {
				cmd.PlanOutput = "" // fresh plan on retry
				if cmd.Attempts < cmd.MaxRetries {
					cmd.Status = types.StatusRetrying
					r.sendUpdate(i, types.StatusRetrying, cmd.Output)
					r.saveSession()
					continue
				}
				cmd.Status = types.StatusFailed
				r.sendUpdate(i, types.StatusFailed, cmd.Output)
				r.saveSession()
				return false
			}
		}

		// Plan + execution + verify all passed
		success = true
		break
	}

	if !success {
		cmd.Status = types.StatusFailed
		r.sendUpdate(i, types.StatusFailed, cmd.Output)
		r.saveSession()
		return false
	}

	// 4. DOCUMENTATION (non-fatal)
	if !r.NoDocs {
		cmd.Status = types.StatusDocumenting
		r.sendUpdate(i, types.StatusDocumenting, cmd.Output)
		r.saveSession()

		docPrompt := "Review the changes just made in this project. Update the following documentation files to reflect these changes:\n\n1. CLAUDE.md — This is the project memory file for Claude Code. Update it with any new conventions, architecture decisions, file structure changes, dependencies added, or important patterns established by the recent changes. Create the file if it doesn't exist. Keep it concise and useful as a reference for future Claude Code sessions.\n\n2. README.md — Update the user-facing documentation to reflect any new features, usage changes, API changes, or configuration options introduced by the recent changes. Create the file if it doesn't exist. Do not remove existing content unless it's outdated due to the changes.\n\nOnly update sections relevant to the recent changes. Do not rewrite unrelated sections. If no documentation updates are needed, make no changes.\n\nRecent task that was executed: " + cmd.Prompt
		docOutput, docErr := r.runClaude(i, docPrompt)
		if docErr != nil {
			cmd.Output = cmd.Output + "\n═══ DOCUMENTATION ═══\n" + fmt.Sprintf("[warn] documentation update failed: %v", docErr)
		} else {
			cmd.Output = cmd.Output + "\n═══ DOCUMENTATION ═══\n" + docOutput
		}
		r.sendUpdate(i, types.StatusDocumenting, cmd.Output)
	}

	// 5. COMMIT
	cmd.Status = types.StatusCommitting
	r.sendUpdate(i, types.StatusCommitting, cmd.Output)
	r.saveSession()

	commitOutput, commitErr := r.runClaude(i, "Git add all changes, commit with a concise meaningful commit message describing what was done, and push to origin. Do not ask for confirmation.")
	if commitErr != nil {
		cmd.Output = cmd.Output + "\n" + fmt.Sprintf("[warn] git commit/push failed: %v", commitErr)
	} else {
		cmd.Output = cmd.Output + "\n" + commitOutput
	}

	cmd.Status = types.StatusSuccess
	r.sendUpdate(i, types.StatusSuccess, cmd.Output)
	r.saveSession()
	return true
}

func (r *Runner) sendUpdate(index int, status types.CommandStatus, output string) {
	if r.program != nil {
		r.program.Send(StatusUpdateMsg{
			CmdIndex: index,
			Status:   status,
			Output:   output,
		})
	}
}

func (r *Runner) sendAllDone() {
	if r.program != nil {
		r.program.Send(AllDoneMsg{})
	}
}

// saveSession persists the current runner state to disk for resumption.
// Errors are logged as warnings on the current command's output but do not halt execution.
func (r *Runner) saveSession() {
	sessionCmds := make([]types.SessionCommand, len(r.Commands))
	for i, cmd := range r.Commands {
		sessionCmds[i] = cmd.ToSessionCommand()
	}

	state := &session.SessionState{
		Commands:     sessionCmds,
		CurrentIndex: r.CurrentIndex,
		WorkDir:      r.WorkDir,
		MaxRetries:   r.MaxRetries,
		CreatedAt:    time.Now(),
		UpdatedAt:    time.Now(),
	}

	if err := session.Save(state, r.WorkDir); err != nil {
		// Log warning but don't fail execution
		if r.CurrentIndex >= 0 && r.CurrentIndex < len(r.Commands) {
			r.Commands[r.CurrentIndex].Output += fmt.Sprintf("\n[warn] failed to save session state: %v", err)
		}
	}
}

func (r *Runner) runCommandStreaming(cmdIndex int, name string, args ...string) (string, CommandResult, error) {
	cmd := exec.CommandContext(r.ctx, name, args...)
	cmd.Dir = r.WorkDir

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return "", CommandResult{ExitCode: -1}, fmt.Errorf("stdout pipe: %w", err)
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return "", CommandResult{ExitCode: -1}, fmt.Errorf("stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return "", CommandResult{ExitCode: -1}, fmt.Errorf("start: %w", err)
	}

	var stdoutBuf, stderrBuf bytes.Buffer
	stdoutTee := io.TeeReader(stdoutPipe, &stdoutBuf)
	stderrTee := io.TeeReader(stderrPipe, &stderrBuf)

	var mu sync.Mutex
	var lines []string
	var wg sync.WaitGroup

	readPipe := func(pipe io.Reader) {
		defer wg.Done()
		scanner := bufio.NewScanner(pipe)
		scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for scanner.Scan() {
			line := scanner.Text()
			mu.Lock()
			lines = append(lines, line)
			mu.Unlock()
			if r.program != nil {
				r.program.Send(OutputLineMsg{
					CmdIndex: cmdIndex,
					Line:     line,
				})
			}
		}
	}

	wg.Add(2)
	go readPipe(stdoutTee)
	go readPipe(stderrTee)
	wg.Wait()

	waitErr := cmd.Wait()

	mu.Lock()
	output := strings.Join(lines, "\n")
	mu.Unlock()

	exitCode := 0
	if waitErr != nil {
		if exitErr, ok := waitErr.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			exitCode = -1
		}
	}

	result := CommandResult{
		Stdout:   stdoutBuf.String(),
		Stderr:   stderrBuf.String(),
		ExitCode: exitCode,
	}

	return output, result, waitErr
}

func (r *Runner) runClaude(cmdIndex int, prompt string) (string, error) {
	output, _, err := r.runCommandStreaming(cmdIndex, "claude", "--dangerously-skip-permissions", "-p", prompt)
	return output, err
}

func (r *Runner) runVerify(cmdIndex int, verifyCmd string) (string, error) {
	output, _, err := r.runCommandStreaming(cmdIndex, "sh", "-c", verifyCmd)
	return output, err
}
