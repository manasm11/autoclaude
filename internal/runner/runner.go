package runner

import (
	"bufio"
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

// Runner manages sequential execution of commands.
type Runner struct {
	Commands     []*types.Command
	CurrentIndex int
	WorkDir      string
	MaxRetries   int
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
	for i, cmd := range r.Commands {
		r.CurrentIndex = i

		cmd.Status = types.StatusRunning
		r.sendUpdate(i, types.StatusRunning, "")
		r.saveSession()

		success := false
		for cmd.Attempts < cmd.MaxRetries {
			cmd.Attempts++

			// Run the claude command
			output, err := r.runClaude(i, cmd.Prompt)
			cmd.Output = output

			if err != nil {
				if cmd.Attempts < cmd.MaxRetries {
					cmd.Status = types.StatusRetrying
					r.sendUpdate(i, types.StatusRetrying, output)
					r.saveSession()
					continue
				}
				// Retries exhausted
				cmd.Status = types.StatusFailed
				r.sendUpdate(i, types.StatusFailed, output)
				r.saveSession()
				r.sendAllDone()
				return
			}

			// Claude succeeded — run verification if configured
			if cmd.Verify != "" {
				cmd.Status = types.StatusVerifying
				r.sendUpdate(i, types.StatusVerifying, cmd.Output)
				r.saveSession()

				verifyOutput, verifyErr := r.runVerify(i, cmd.Verify)
				cmd.Output = cmd.Output + "\n" + verifyOutput

				if verifyErr != nil {
					if cmd.Attempts < cmd.MaxRetries {
						cmd.Status = types.StatusRetrying
						r.sendUpdate(i, types.StatusRetrying, cmd.Output)
						r.saveSession()
						continue
					}
					cmd.Status = types.StatusFailed
					r.sendUpdate(i, types.StatusFailed, cmd.Output)
					r.saveSession()
					r.sendAllDone()
					return
				}
			}

			// Both claude and verify passed
			success = true
			break
		}

		if !success {
			cmd.Status = types.StatusFailed
			r.sendUpdate(i, types.StatusFailed, cmd.Output)
			r.saveSession()
			r.sendAllDone()
			return
		}

		// Commit and push
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
	}

	// All commands completed successfully — clear session file since no resume is needed
	session.Clear(r.WorkDir)
	r.sendAllDone()
}

func (r *Runner) executeFrom(startIndex int) {
	for i := startIndex; i < len(r.Commands); i++ {
		cmd := r.Commands[i]
		r.CurrentIndex = i

		cmd.Status = types.StatusRunning
		r.sendUpdate(i, types.StatusRunning, "")
		r.saveSession()

		success := false
		for cmd.Attempts < cmd.MaxRetries {
			cmd.Attempts++

			output, err := r.runClaude(i, cmd.Prompt)
			cmd.Output = output

			if err != nil {
				if cmd.Attempts < cmd.MaxRetries {
					cmd.Status = types.StatusRetrying
					r.sendUpdate(i, types.StatusRetrying, output)
					r.saveSession()
					continue
				}
				cmd.Status = types.StatusFailed
				r.sendUpdate(i, types.StatusFailed, output)
				r.saveSession()
				r.sendAllDone()
				return
			}

			if cmd.Verify != "" {
				cmd.Status = types.StatusVerifying
				r.sendUpdate(i, types.StatusVerifying, cmd.Output)
				r.saveSession()

				verifyOutput, verifyErr := r.runVerify(i, cmd.Verify)
				cmd.Output = cmd.Output + "\n" + verifyOutput

				if verifyErr != nil {
					if cmd.Attempts < cmd.MaxRetries {
						cmd.Status = types.StatusRetrying
						r.sendUpdate(i, types.StatusRetrying, cmd.Output)
						r.saveSession()
						continue
					}
					cmd.Status = types.StatusFailed
					r.sendUpdate(i, types.StatusFailed, cmd.Output)
					r.saveSession()
					r.sendAllDone()
					return
				}
			}

			success = true
			break
		}

		if !success {
			cmd.Status = types.StatusFailed
			r.sendUpdate(i, types.StatusFailed, cmd.Output)
			r.saveSession()
			r.sendAllDone()
			return
		}

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
	}

	session.Clear(r.WorkDir)
	r.sendAllDone()
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

func (r *Runner) runCommandStreaming(cmdIndex int, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(r.ctx, name, args...)
	cmd.Dir = r.WorkDir

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return "", fmt.Errorf("stdout pipe: %w", err)
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return "", fmt.Errorf("stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("start: %w", err)
	}

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
	go readPipe(stdoutPipe)
	go readPipe(stderrPipe)
	wg.Wait()

	err = cmd.Wait()

	mu.Lock()
	output := strings.Join(lines, "\n")
	mu.Unlock()

	return output, err
}

func (r *Runner) runClaude(cmdIndex int, prompt string) (string, error) {
	return r.runCommandStreaming(cmdIndex, "claude", "--dangerously-skip-permissions", "-p", prompt)
}

func (r *Runner) runVerify(cmdIndex int, verifyCmd string) (string, error) {
	return r.runCommandStreaming(cmdIndex, "sh", "-c", verifyCmd)
}
