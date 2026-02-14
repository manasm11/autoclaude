package types

// CommandStatus represents the current state of a command execution.
type CommandStatus int

const (
	StatusPending    CommandStatus = iota // 0
	StatusRunning                         // 1
	StatusVerifying                       // 2
	StatusCommitting                      // 3
	StatusSuccess                         // 4
	StatusFailed                          // 5
	StatusRetrying                        // 6
)

// String returns the human-readable label for a CommandStatus value.
func (s CommandStatus) String() string {
	labels := []string{
		"Pending",
		"Running",
		"Verifying",
		"Committing",
		"Success",
		"Failed",
		"Retrying",
	}
	if int(s) < 0 || int(s) >= len(labels) {
		return "Unknown"
	}
	return labels[s]
}

// Command represents a single claude code prompt to execute.
type Command struct {
	Prompt     string        // the claude code prompt
	Verify     string        // optional verification command (empty = no verification)
	MaxRetries int           // default 3
	Status     CommandStatus // current execution status
	Output     string        // captured stdout/stderr
	Attempts   int           // number of attempts made
}

// NewCommand creates a new Command with sensible defaults.
func NewCommand(prompt string) *Command {
	return &Command{
		Prompt: prompt,
		Status: StatusPending,
	}
}

// ParseCommandStatus converts a string back to a CommandStatus.
// Returns StatusPending for unrecognized values.
func ParseCommandStatus(s string) CommandStatus {
	switch s {
	case "Pending":
		return StatusPending
	case "Running":
		return StatusRunning
	case "Verifying":
		return StatusVerifying
	case "Committing":
		return StatusCommitting
	case "Success":
		return StatusSuccess
	case "Failed":
		return StatusFailed
	case "Retrying":
		return StatusRetrying
	default:
		return StatusPending
	}
}

// SessionCommand is a JSON-friendly representation of a Command for session persistence.
type SessionCommand struct {
	Prompt     string `json:"prompt"`
	Verify     string `json:"verify,omitempty"`
	MaxRetries int    `json:"max_retries"`
	Status     string `json:"status"`
	Attempts   int    `json:"attempts"`
	Output     string `json:"output,omitempty"`
}

// ToSessionCommand converts a Command to a SessionCommand for serialization.
func (c *Command) ToSessionCommand() SessionCommand {
	return SessionCommand{
		Prompt:     c.Prompt,
		Verify:     c.Verify,
		MaxRetries: c.MaxRetries,
		Status:     c.Status.String(),
		Attempts:   c.Attempts,
		Output:     c.Output,
	}
}

// FromSessionCommand creates a Command from a SessionCommand.
func FromSessionCommand(sc SessionCommand) *Command {
	return &Command{
		Prompt:     sc.Prompt,
		Verify:     sc.Verify,
		MaxRetries: sc.MaxRetries,
		Status:     ParseCommandStatus(sc.Status),
		Attempts:   sc.Attempts,
		Output:     sc.Output,
	}
}
