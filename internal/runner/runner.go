package runner

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/manasm11/autoclaude/internal/session"
	"github.com/manasm11/autoclaude/internal/types"
)

// StatusUpdateMsg is sent to the TUI when a command's status changes.
type StatusUpdateMsg struct {
	CmdIndex     int
	Status       types.CommandStatus
	Output       string
	StatusDetail string // e.g. "Attempt 2/3"
}

// AllDoneMsg is sent when all commands have finished (or one has permanently failed).
type AllDoneMsg struct{}

// ExecutionErrorMsg is sent when a command encounters an unrecoverable error.
type ExecutionErrorMsg struct {
	CmdIndex      int
	Err           error
	FailureReport string
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

// lastNLines returns the last n lines of s. If s has fewer than n lines, returns all of s.
func lastNLines(s string, n int) string {
	lines := strings.Split(s, "\n")
	if len(lines) <= n {
		return s
	}
	return strings.Join(lines[len(lines)-n:], "\n")
}

// executeSingle runs the full plan-execute-verify-commit cycle for a single command.
// Returns true if the command succeeded, false if it permanently failed.
func (r *Runner) executeSingle(i int, cmd *types.Command) bool {
	// Accumulators for the current attempt's stdout/stderr across all steps.
	var attemptStdout, attemptStderr strings.Builder

	// finalizeAttempt fills in the terminal fields of an AttemptLog and appends it
	// to cmd.AttemptLogs. It is called once per attempt (initial or fix).
	finalizeAttempt := func(log types.AttemptLog, failedStep string, exitCode int) {
		log.FailedStep = failedStep
		log.ExitCode = exitCode
		log.EndedAt = time.Now()
		log.Duration = log.EndedAt.Sub(log.StartedAt)
		log.Stdout = attemptStdout.String()
		log.Stderr = attemptStderr.String()
		cmd.AttemptLogs = append(cmd.AttemptLogs, log)
	}

	// updateLastAttempt updates the most-recently-appended AttemptLog in place.
	// Used for post-loop steps (doc, commit) that extend the final attempt.
	updateLastAttempt := func(failedStep string, exitCode int) {
		if len(cmd.AttemptLogs) == 0 {
			return
		}
		last := &cmd.AttemptLogs[len(cmd.AttemptLogs)-1]
		if failedStep != "" {
			last.FailedStep = failedStep
		}
		if exitCode != 0 {
			last.ExitCode = exitCode
		}
		last.EndedAt = time.Now()
		last.Duration = last.EndedAt.Sub(last.StartedAt)
		last.Stdout = attemptStdout.String()
		last.Stderr = attemptStderr.String()
	}

	// recordFailure stores the most recent failure details on cmd for BuildFixPrompt().
	recordFailure := func(failedStep string, exitCode int, stdout string, stderr string) {
		cmd.LastFailedStep = failedStep
		cmd.LastExitCode = exitCode
		cmd.LastStdout = stdout
		cmd.LastStderr = stderr
	}

	// sendFailed marks the command as failed and sends the failure report.
	sendFailed := func() {
		cmd.Status = types.StatusFailed
		r.sendUpdate(i, types.StatusFailed, cmd.Output, "")
		r.saveSession()
		report := r.writeFailureReport(cmd)
		if r.program != nil {
			r.program.Send(ExecutionErrorMsg{
				CmdIndex:      i,
				Err:           fmt.Errorf("command failed after %d fix attempt(s)", cmd.FixAttempts),
				FailureReport: report,
			})
		}
	}

	// --- Initial attempt context ---
	attemptStdout.Reset()
	attemptStderr.Reset()
	cmd.Attempts = 1

	gitBranch, gitStatus := r.captureGitContext()
	attemptLog := types.AttemptLog{
		AttemptNumber: 1,
		StartedAt:     time.Now(),
		WorkDir:       r.WorkDir,
		GitBranch:     gitBranch,
		GitStatus:     gitStatus,
	}

	// 1. PLANNING — skip if we already have a plan (e.g. resumed session)
	if cmd.PlanOutput == "" {
		cmd.Status = types.StatusPlanning
		r.sendUpdate(i, types.StatusPlanning, "", "")
		r.saveSession()

		planPrompt := "You are planning the implementation of a task. Create a detailed step-by-step implementation plan. List every file to create or modify, what exact changes to make in each file, function signatures, and edge cases to handle. Do NOT write any code, do NOT create or modify any files. Only output the plan in markdown.\n\nTask: " + cmd.Prompt
		attemptLog.Command = "claude -p <planning prompt>"
		planOutput, planResult, planErr := r.runClaude(i, planPrompt)
		attemptStdout.WriteString(planResult.Stdout)
		attemptStderr.WriteString(planResult.Stderr)
		if planErr != nil {
			finalizeAttempt(attemptLog, "Planning", planResult.ExitCode)
			cmd.Output += planOutput
			recordFailure("planning", planResult.ExitCode, planResult.Stdout, planResult.Stderr)
			if !cmd.AutoFix {
				sendFailed()
				return false
			}
			goto fixLoop
		}
		cmd.PlanOutput = planOutput
		r.saveSession()
	}

	// 2. EXECUTION
	{
		cmd.Status = types.StatusRunning
		cmd.Output += "═══ PLAN ═══\n" + cmd.PlanOutput + "\n═══ EXECUTION ═══\n"
		r.sendUpdate(i, types.StatusRunning, cmd.Output, "")
		r.saveSession()

		execPrompt := "Execute the following implementation plan exactly. Follow each step precisely.\n\nPlan:\n" + cmd.PlanOutput + "\n\nOriginal task: " + cmd.Prompt
		attemptLog.Command = "claude -p <execution prompt>"
		execOutput, execResult, execErr := r.runClaude(i, execPrompt)
		attemptStdout.WriteString(execResult.Stdout)
		attemptStderr.WriteString(execResult.Stderr)
		cmd.Output += execOutput

		if execErr != nil {
			finalizeAttempt(attemptLog, "Running", execResult.ExitCode)
			recordFailure("execution", execResult.ExitCode, execResult.Stdout, execResult.Stderr)
			if !cmd.AutoFix {
				sendFailed()
				return false
			}
			goto fixLoop
		}
	}

	// 3. VERIFICATION
	if cmd.Verify != "" {
		cmd.Status = types.StatusVerifying
		r.sendUpdate(i, types.StatusVerifying, cmd.Output, "")
		r.saveSession()

		attemptLog.Command = cmd.Verify
		verifyOutput, verifyResult, verifyErr := r.runVerify(i, cmd.Verify)
		attemptStdout.WriteString(verifyResult.Stdout)
		attemptStderr.WriteString(verifyResult.Stderr)
		cmd.Output += "\n" + verifyOutput

		if verifyErr != nil {
			finalizeAttempt(attemptLog, "Verifying", verifyResult.ExitCode)
			recordFailure("verification", verifyResult.ExitCode, verifyResult.Stdout, verifyResult.Stderr)
			if !cmd.AutoFix {
				sendFailed()
				return false
			}
			goto fixLoop
		}
	}

	// Initial attempt succeeded — log success.
	finalizeAttempt(attemptLog, "", 0)
	goto success

fixLoop:
	// Auto-fix loop: attempt to fix the failure and re-verify.
	for {
		cmd.FixAttempts++
		if cmd.FixAttempts >= cmd.MaxRetries {
			sendFailed()
			return false
		}

		fixDetail := fmt.Sprintf("Fix %d/%d", cmd.FixAttempts, cmd.MaxRetries)

		// Append fix separator to output.
		cmd.Output += fmt.Sprintf("\n═══ FIX ATTEMPT %d/%d ═══\n", cmd.FixAttempts, cmd.MaxRetries)
		cmd.Output += fmt.Sprintf("Failed step: %s | Exit code: %d\n", cmd.LastFailedStep, cmd.LastExitCode)
		cmd.Output += fmt.Sprintf("Stderr: %s\n", lastNLines(cmd.LastStderr, 50))

		cmd.Status = types.StatusFixing
		r.sendUpdate(i, types.StatusFixing, cmd.Output, fixDetail)
		r.saveSession()

		// Sleep 2 seconds before fix attempt.
		select {
		case <-time.After(2 * time.Second):
		case <-r.ctx.Done():
			return false
		}

		// Reset per-attempt accumulators for the fix attempt.
		attemptStdout.Reset()
		attemptStderr.Reset()

		gitBranch, gitStatus := r.captureGitContext()
		fixAttemptLog := types.AttemptLog{
			AttemptNumber: cmd.FixAttempts + 1, // +1 because attempt 1 is the initial try
			StartedAt:     time.Now(),
			WorkDir:       r.WorkDir,
			GitBranch:     gitBranch,
			GitStatus:     gitStatus,
			Command:       "claude -p <auto-fix prompt>",
		}

		// Run claude with the fix prompt.
		fixPrompt := cmd.BuildFixPrompt()
		fixOutput, fixResult, _ := r.runClaude(i, fixPrompt)
		attemptStdout.WriteString(fixResult.Stdout)
		attemptStderr.WriteString(fixResult.Stderr)
		cmd.Output += fmt.Sprintf("Fix output: %s\n", fixOutput)

		// After fix, re-verify only (don't re-plan or re-execute).
		if cmd.Verify != "" {
			cmd.Status = types.StatusVerifying
			r.sendUpdate(i, types.StatusVerifying, cmd.Output, fixDetail)
			r.saveSession()

			verifyOutput, verifyResult, verifyErr := r.runVerify(i, cmd.Verify)
			attemptStdout.WriteString(verifyResult.Stdout)
			attemptStderr.WriteString(verifyResult.Stderr)
			cmd.Output += "\n" + verifyOutput

			if verifyErr != nil {
				// Verification failed again — log this fix attempt and loop.
				finalizeAttempt(fixAttemptLog, "auto-fix", verifyResult.ExitCode)
				recordFailure("verification", verifyResult.ExitCode, verifyResult.Stdout, verifyResult.Stderr)
				continue
			}
		} else {
			// No verify command — assume fix succeeded if claude exited 0.
			if fixResult.ExitCode != 0 {
				finalizeAttempt(fixAttemptLog, "auto-fix", fixResult.ExitCode)
				recordFailure("execution", fixResult.ExitCode, fixResult.Stdout, fixResult.Stderr)
				continue
			}
		}

		// Fix succeeded — log success attempt.
		finalizeAttempt(fixAttemptLog, "", 0)
		// Clear PlanOutput — the fix was applied directly, don't re-plan.
		cmd.PlanOutput = ""
		break
	}

success:
	// 4. DOCUMENTATION (non-fatal)
	if !r.NoDocs {
		cmd.Status = types.StatusDocumenting
		r.sendUpdate(i, types.StatusDocumenting, cmd.Output, "")
		r.saveSession()

		docPrompt := "Review the changes just made in this project. Update the following documentation files to reflect these changes:\n\n1. CLAUDE.md — This is the project memory file for Claude Code. Update it with any new conventions, architecture decisions, file structure changes, dependencies added, or important patterns established by the recent changes. Create the file if it doesn't exist. Keep it concise and useful as a reference for future Claude Code sessions.\n\n2. README.md — Update the user-facing documentation to reflect any new features, usage changes, API changes, or configuration options introduced by the recent changes. Create the file if it doesn't exist. Do not remove existing content unless it's outdated due to the changes.\n\nOnly update sections relevant to the recent changes. Do not rewrite unrelated sections. If no documentation updates are needed, make no changes.\n\nRecent task that was executed: " + cmd.Prompt
		docOutput, docResult, docErr := r.runClaude(i, docPrompt)
		attemptStdout.WriteString(docResult.Stdout)
		attemptStderr.WriteString(docResult.Stderr)
		if docErr != nil {
			cmd.Output += "\n═══ DOCUMENTATION ═══\n" + fmt.Sprintf("[warn] documentation update failed: %v", docErr)
			updateLastAttempt("Documenting", docResult.ExitCode)
		} else {
			cmd.Output += "\n═══ DOCUMENTATION ═══\n" + docOutput
			updateLastAttempt("", 0)
		}
		r.sendUpdate(i, types.StatusDocumenting, cmd.Output, "")
	}

	// 5. COMMIT
	cmd.Status = types.StatusCommitting
	r.sendUpdate(i, types.StatusCommitting, cmd.Output, "")
	r.saveSession()

	commitOutput, commitResult, commitErr := r.runClaude(i, "Git add all changes, commit with a concise meaningful commit message describing what was done, and push to origin. Do not ask for confirmation.")
	attemptStdout.WriteString(commitResult.Stdout)
	attemptStderr.WriteString(commitResult.Stderr)
	if commitErr != nil {
		cmd.Output += "\n" + fmt.Sprintf("[warn] git commit/push failed: %v", commitErr)
		updateLastAttempt("Committing", commitResult.ExitCode)
	} else {
		cmd.Output += "\n" + commitOutput
		updateLastAttempt("", 0)
	}

	cmd.Status = types.StatusSuccess
	r.sendUpdate(i, types.StatusSuccess, cmd.Output, "")
	r.saveSession()
	return true
}

// writeFailureReport formats and appends a failure report to autoclaude-error.log.
// Returns the full report string for use in TUI error messages.
func (r *Runner) writeFailureReport(cmd *types.Command) string {
	prompt := cmd.Prompt
	if len(prompt) > 100 {
		prompt = prompt[:100] + "..."
	}

	var b strings.Builder
	b.WriteString("════════════════════════════════════════\n")
	fmt.Fprintf(&b, "FAILURE REPORT — %s\n", time.Now().Format(time.RFC3339))
	fmt.Fprintf(&b, "Command: %q\n", prompt)
	b.WriteString("════════════════════════════════════════\n")

	body := cmd.FormatFailureReport()
	fullReport := b.String() + "\n" + body + "\n\n"

	logPath := filepath.Join(r.WorkDir, "autoclaude-error.log")
	f, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		cmd.Output += fmt.Sprintf("\n[warn] failed to write error log: %v", err)
		return fullReport
	}
	defer f.Close()
	if _, err := f.WriteString(fullReport); err != nil {
		cmd.Output += fmt.Sprintf("\n[warn] failed to write error log: %v", err)
	}

	return fullReport
}

func (r *Runner) sendUpdate(index int, status types.CommandStatus, output string, detail string) {
	if r.program != nil {
		r.program.Send(StatusUpdateMsg{
			CmdIndex:     index,
			Status:       status,
			Output:       output,
			StatusDetail: detail,
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

// captureGitContext runs git commands to capture the current branch and working tree status.
// Errors are silently ignored; empty strings are returned if git is unavailable.
func (r *Runner) captureGitContext() (branch string, status string) {
	if out, err := exec.Command("git", "-C", r.WorkDir, "branch", "--show-current").Output(); err == nil {
		branch = strings.TrimSpace(string(out))
	}
	if out, err := exec.Command("git", "-C", r.WorkDir, "status", "--porcelain").Output(); err == nil {
		status = strings.TrimSpace(string(out))
	}
	return
}

func (r *Runner) runClaude(cmdIndex int, prompt string) (string, CommandResult, error) {
	return r.runCommandStreaming(cmdIndex, "claude", "--dangerously-skip-permissions", "-p", prompt)
}

func (r *Runner) runVerify(cmdIndex int, verifyCmd string) (string, CommandResult, error) {
	return r.runCommandStreaming(cmdIndex, "sh", "-c", verifyCmd)
}
