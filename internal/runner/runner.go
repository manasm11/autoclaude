package runner

import (
	"context"
	"fmt"
	"os/exec"

	tea "github.com/charmbracelet/bubbletea"
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

// Runner manages sequential execution of commands.
type Runner struct {
	Commands     []*types.Command
	CurrentIndex int
	WorkDir      string
	program      *tea.Program
	ctx          context.Context
	cancel       context.CancelFunc
}

// NewRunner creates a new Runner with the given working directory.
func NewRunner(workDir string) *Runner {
	ctx, cancel := context.WithCancel(context.Background())
	return &Runner{
		Commands: make([]*types.Command, 0),
		WorkDir:  workDir,
		ctx:      ctx,
		cancel:   cancel,
	}
}

// SetProgram stores the BubbleTea program reference for sending messages.
func (r *Runner) SetProgram(p *tea.Program) {
	r.program = p
}

// AddCommand appends a command to the execution queue.
func (r *Runner) AddCommand(cmd *types.Command) {
	r.Commands = append(r.Commands, cmd)
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

func (r *Runner) executeAll() {
	for i, cmd := range r.Commands {
		r.CurrentIndex = i

		cmd.Status = types.StatusRunning
		r.sendUpdate(i, types.StatusRunning, "")

		success := false
		for cmd.Attempts < cmd.MaxRetries {
			cmd.Attempts++

			// Run the claude command
			output, err := r.runClaude(cmd.Prompt)
			cmd.Output = output

			if err != nil {
				if cmd.Attempts < cmd.MaxRetries {
					cmd.Status = types.StatusRetrying
					r.sendUpdate(i, types.StatusRetrying, output)
					continue
				}
				// Retries exhausted
				cmd.Status = types.StatusFailed
				r.sendUpdate(i, types.StatusFailed, output)
				r.program.Send(AllDoneMsg{})
				return
			}

			// Claude succeeded — run verification if configured
			if cmd.Verify != "" {
				cmd.Status = types.StatusVerifying
				r.sendUpdate(i, types.StatusVerifying, cmd.Output)

				verifyOutput, verifyErr := r.runVerify(cmd.Verify)
				cmd.Output = cmd.Output + "\n" + verifyOutput

				if verifyErr != nil {
					if cmd.Attempts < cmd.MaxRetries {
						cmd.Status = types.StatusRetrying
						r.sendUpdate(i, types.StatusRetrying, cmd.Output)
						continue
					}
					cmd.Status = types.StatusFailed
					r.sendUpdate(i, types.StatusFailed, cmd.Output)
					r.program.Send(AllDoneMsg{})
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
			r.program.Send(AllDoneMsg{})
			return
		}

		// Commit and push
		cmd.Status = types.StatusCommitting
		r.sendUpdate(i, types.StatusCommitting, cmd.Output)

		commitOutput, commitErr := r.runClaude("Git add all changes, commit with a concise meaningful commit message describing what was done, and push to origin. Do not ask for confirmation.")
		if commitErr != nil {
			cmd.Output = cmd.Output + "\n" + fmt.Sprintf("[warn] git commit/push failed: %v", commitErr)
		} else {
			cmd.Output = cmd.Output + "\n" + commitOutput
		}

		cmd.Status = types.StatusSuccess
		r.sendUpdate(i, types.StatusSuccess, cmd.Output)
	}

	r.program.Send(AllDoneMsg{})
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

func (r *Runner) runCommand(name string, args ...string) (string, error) {
	cmd := exec.CommandContext(r.ctx, name, args...)
	cmd.Dir = r.WorkDir
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func (r *Runner) runClaude(prompt string) (string, error) {
	return r.runCommand("claude", "--dangerously-skip-permissions", "-p", prompt)
}

func (r *Runner) runVerify(verifyCmd string) (string, error) {
	return r.runCommand("sh", "-c", verifyCmd)
}
