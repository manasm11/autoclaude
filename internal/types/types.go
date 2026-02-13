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
		Prompt:     prompt,
		MaxRetries: 3,
		Status:     StatusPending,
	}
}
