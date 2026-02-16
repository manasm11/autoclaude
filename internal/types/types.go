package types

import (
	"fmt"
	"strings"
	"time"
)

// CommandStatus represents the current state of a command execution.
type CommandStatus int

const (
	StatusPending    CommandStatus = iota // 0
	StatusPlanning                        // 1
	StatusRunning                         // 2
	StatusVerifying                       // 3
	StatusDocumenting                     // 4
	StatusCommitting                      // 5
	StatusSuccess                         // 6
	StatusFailed                          // 7
	StatusRetrying                        // 8
	StatusFixing                          // 9
)

// String returns the human-readable label for a CommandStatus value.
func (s CommandStatus) String() string {
	labels := []string{
		"Pending",
		"Planning",
		"Running",
		"Verifying",
		"Documenting",
		"Committing",
		"Success",
		"Failed",
		"Retrying",
		"Fixing",
	}
	if int(s) < 0 || int(s) >= len(labels) {
		return "Unknown"
	}
	return labels[s]
}

// AttemptLog records details of a single execution attempt for debugging.
type AttemptLog struct {
	AttemptNumber int
	StartedAt     time.Time
	EndedAt       time.Time
	Duration      time.Duration
	FailedStep    string // e.g. "Planning", "Running", "Verifying"
	Command       string // the command or prompt that was executed
	ExitCode      int
	Stdout        string
	Stderr        string
	WorkDir       string
	GitBranch     string
	GitStatus     string
}

// Command represents a single claude code prompt to execute.
type Command struct {
	Prompt     string        // the claude code prompt
	Verify     string        // optional verification command (empty = no verification)
	MaxRetries int           // default 3
	Status     CommandStatus // current execution status
	Output     string        // captured stdout/stderr
	PlanOutput string        // output from the planning phase
	Attempts       int           // number of attempts made
	AttemptLogs    []AttemptLog  // detailed log of each attempt for debugging
	LastFailedStep string        // which step failed: "planning", "execution", or "verification"
	LastExitCode   int           // exit code of the failed process
	LastStderr     string        // stderr from the failed step
	LastStdout     string        // stdout from the failed step
	FixAttempts    int           // number of auto-fix attempts made so far
}

// NewCommand creates a new Command with sensible defaults.
func NewCommand(prompt string) *Command {
	return &Command{
		Prompt: prompt,
		Status: StatusPending,
	}
}

// FormatFailureReport formats all attempt logs into a readable debug report.
func (c *Command) FormatFailureReport() string {
	if len(c.AttemptLogs) == 0 {
		return "No attempt logs recorded."
	}

	var b strings.Builder
	prompt := c.Prompt
	if len(prompt) > 200 {
		prompt = prompt[:200] + "..."
	}
	fmt.Fprintf(&b, "=== Failure Report for: %s ===\n", prompt)
	fmt.Fprintf(&b, "Total attempts: %d\n\n", len(c.AttemptLogs))

	for _, a := range c.AttemptLogs {
		fmt.Fprintf(&b, "--- Attempt #%d ---\n", a.AttemptNumber)
		fmt.Fprintf(&b, "  Failed Step:  %s\n", a.FailedStep)
		fmt.Fprintf(&b, "  Exit Code:    %d\n", a.ExitCode)
		fmt.Fprintf(&b, "  Started:      %s\n", a.StartedAt.Format(time.RFC3339))
		fmt.Fprintf(&b, "  Ended:        %s\n", a.EndedAt.Format(time.RFC3339))
		fmt.Fprintf(&b, "  Duration:     %s\n", a.Duration.Round(time.Millisecond))
		if a.Command != "" {
			fmt.Fprintf(&b, "  Command:      %s\n", a.Command)
		}
		if a.WorkDir != "" {
			fmt.Fprintf(&b, "  WorkDir:      %s\n", a.WorkDir)
		}
		if a.GitBranch != "" {
			fmt.Fprintf(&b, "  Git Branch:   %s\n", a.GitBranch)
		}
		if a.GitStatus != "" {
			fmt.Fprintf(&b, "  Git Status:   %s\n", a.GitStatus)
		}
		if a.Stdout != "" {
			fmt.Fprintf(&b, "  Stdout:\n    %s\n", strings.ReplaceAll(strings.TrimRight(a.Stdout, "\n"), "\n", "\n    "))
		}
		if a.Stderr != "" {
			fmt.Fprintf(&b, "  Stderr:\n    %s\n", strings.ReplaceAll(strings.TrimRight(a.Stderr, "\n"), "\n", "\n    "))
		}
		b.WriteString("\n")
	}

	return b.String()
}

// BuildFixPrompt generates a comprehensive prompt for Claude to fix a failed step.
func (c *Command) BuildFixPrompt() string {
	var b strings.Builder

	fmt.Fprintf(&b, "Original task:\n%s\n\n", c.Prompt)
	fmt.Fprintf(&b, "Failed step: %s\n", c.LastFailedStep)
	fmt.Fprintf(&b, "Exit code: %d\n\n", c.LastExitCode)

	if c.PlanOutput != "" {
		fmt.Fprintf(&b, "Plan that was used:\n%s\n\n", c.PlanOutput)
	}

	if c.LastStdout != "" {
		fmt.Fprintf(&b, "Stdout:\n%s\n\n", c.LastStdout)
	}

	if c.LastStderr != "" {
		fmt.Fprintf(&b, "Stderr:\n%s\n\n", c.LastStderr)
	}

	b.WriteString("The above command failed. Analyze the error output carefully and fix the issue. Do not start from scratch — identify what went wrong and make targeted fixes to resolve the error.")

	return b.String()
}

// ParseCommandStatus converts a string back to a CommandStatus.
// Returns StatusPending for unrecognized values.
func ParseCommandStatus(s string) CommandStatus {
	switch s {
	case "Pending":
		return StatusPending
	case "Planning":
		return StatusPlanning
	case "Running":
		return StatusRunning
	case "Verifying":
		return StatusVerifying
	case "Documenting":
		return StatusDocumenting
	case "Committing":
		return StatusCommitting
	case "Success":
		return StatusSuccess
	case "Failed":
		return StatusFailed
	case "Fixing":
		return StatusFixing
	default:
		return StatusPending
	}
}

// SessionAttemptLog is a JSON-friendly representation of AttemptLog for session persistence.
type SessionAttemptLog struct {
	AttemptNumber int    `json:"attempt_number"`
	StartedAt     string `json:"started_at"`
	EndedAt       string `json:"ended_at"`
	DurationMs    int64  `json:"duration_ms"`
	FailedStep    string `json:"failed_step"`
	Command       string `json:"command,omitempty"`
	ExitCode      int    `json:"exit_code"`
	Stdout        string `json:"stdout,omitempty"`
	Stderr        string `json:"stderr,omitempty"`
	WorkDir       string `json:"work_dir,omitempty"`
	GitBranch     string `json:"git_branch,omitempty"`
	GitStatus     string `json:"git_status,omitempty"`
}

// SessionCommand is a JSON-friendly representation of a Command for session persistence.
type SessionCommand struct {
	Prompt         string              `json:"prompt"`
	Verify         string              `json:"verify,omitempty"`
	MaxRetries     int                 `json:"max_retries"`
	Status         string              `json:"status"`
	Attempts       int                 `json:"attempts"`
	FixAttempts    int                 `json:"fix_attempts,omitempty"`
	Output         string              `json:"output,omitempty"`
	PlanOutput     string              `json:"plan_output,omitempty"`
	AttemptLogs    []SessionAttemptLog `json:"attempt_logs,omitempty"`
	LastFailedStep string              `json:"last_failed_step,omitempty"`
	LastExitCode   int                 `json:"last_exit_code,omitempty"`
	LastStderr     string              `json:"last_stderr,omitempty"`
	LastStdout     string              `json:"last_stdout,omitempty"`
}

// ToSessionCommand converts a Command to a SessionCommand for serialization.
func (c *Command) ToSessionCommand() SessionCommand {
	var sessionLogs []SessionAttemptLog
	for _, a := range c.AttemptLogs {
		sessionLogs = append(sessionLogs, SessionAttemptLog{
			AttemptNumber: a.AttemptNumber,
			StartedAt:     a.StartedAt.Format(time.RFC3339),
			EndedAt:       a.EndedAt.Format(time.RFC3339),
			DurationMs:    a.Duration.Milliseconds(),
			FailedStep:    a.FailedStep,
			Command:       a.Command,
			ExitCode:      a.ExitCode,
			Stdout:        a.Stdout,
			Stderr:        a.Stderr,
			WorkDir:       a.WorkDir,
			GitBranch:     a.GitBranch,
			GitStatus:     a.GitStatus,
		})
	}
	return SessionCommand{
		Prompt:         c.Prompt,
		Verify:         c.Verify,
		MaxRetries:     c.MaxRetries,
		Status:         c.Status.String(),
		Attempts:       c.Attempts,
		FixAttempts:    c.FixAttempts,
		Output:         c.Output,
		PlanOutput:     c.PlanOutput,
		AttemptLogs:    sessionLogs,
		LastFailedStep: c.LastFailedStep,
		LastExitCode:   c.LastExitCode,
		LastStderr:     c.LastStderr,
		LastStdout:     c.LastStdout,
	}
}

// FromSessionCommand creates a Command from a SessionCommand.
func FromSessionCommand(sc SessionCommand) *Command {
	var logs []AttemptLog
	for _, sa := range sc.AttemptLogs {
		startedAt, _ := time.Parse(time.RFC3339, sa.StartedAt)
		endedAt, _ := time.Parse(time.RFC3339, sa.EndedAt)
		logs = append(logs, AttemptLog{
			AttemptNumber: sa.AttemptNumber,
			StartedAt:     startedAt,
			EndedAt:       endedAt,
			Duration:      time.Duration(sa.DurationMs) * time.Millisecond,
			FailedStep:    sa.FailedStep,
			Command:       sa.Command,
			ExitCode:      sa.ExitCode,
			Stdout:        sa.Stdout,
			Stderr:        sa.Stderr,
			WorkDir:       sa.WorkDir,
			GitBranch:     sa.GitBranch,
			GitStatus:     sa.GitStatus,
		})
	}
	return &Command{
		Prompt:         sc.Prompt,
		Verify:         sc.Verify,
		MaxRetries:     sc.MaxRetries,
		Status:         ParseCommandStatus(sc.Status),
		Attempts:       sc.Attempts,
		FixAttempts:    sc.FixAttempts,
		Output:         sc.Output,
		PlanOutput:     sc.PlanOutput,
		AttemptLogs:    logs,
		LastFailedStep: sc.LastFailedStep,
		LastExitCode:   sc.LastExitCode,
		LastStderr:     sc.LastStderr,
		LastStdout:     sc.LastStdout,
	}
}
