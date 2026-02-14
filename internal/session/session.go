package session

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/manasm11/autoclaude/internal/types"
)

// ErrCorrupted indicates the session file exists but contains invalid JSON.
var ErrCorrupted = errors.New("session file corrupted")

// SessionFile is the filename used to persist session state.
const SessionFile = ".autoclaude-session.json"

// SessionState captures the full state of an autoclaude run for resumption.
type SessionState struct {
	Commands     []types.SessionCommand `json:"commands"`
	CurrentIndex int                    `json:"current_index"`
	WorkDir      string                 `json:"work_dir"`
	MaxRetries   int                    `json:"max_retries"`
	CreatedAt    time.Time              `json:"created_at"`
	UpdatedAt    time.Time              `json:"updated_at"`
}

// Save writes the session state atomically to dir/SessionFile.
// It writes to a temporary file first, then renames for crash safety.
func Save(state *SessionState, dir string) error {
	state.UpdatedAt = time.Now()

	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling session state: %w", err)
	}

	target := filepath.Join(dir, SessionFile)
	tmp := target + ".tmp"

	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return fmt.Errorf("writing session temp file: %w", err)
	}

	if err := os.Rename(tmp, target); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("renaming session file: %w", err)
	}

	return nil
}

// Load reads and unmarshals the session file from dir.
// Returns os.ErrNotExist if no session file exists.
func Load(dir string) (*SessionState, error) {
	path := filepath.Join(dir, SessionFile)

	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, os.ErrNotExist
		}
		return nil, fmt.Errorf("reading session file: %w", err)
	}

	var state SessionState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrCorrupted, err)
	}

	return &state, nil
}

// Clear deletes the session file from dir.
func Clear(dir string) error {
	path := filepath.Join(dir, SessionFile)
	err := os.Remove(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("removing session file: %w", err)
	}
	return nil
}

// Exists checks whether a session file exists in dir.
func Exists(dir string) bool {
	path := filepath.Join(dir, SessionFile)
	_, err := os.Stat(path)
	return err == nil
}

// AllSucceeded returns true if every command in the session has StatusSuccess.
func AllSucceeded(state *SessionState) bool {
	if len(state.Commands) == 0 {
		return false
	}
	for _, sc := range state.Commands {
		if types.ParseCommandStatus(sc.Status) != types.StatusSuccess {
			return false
		}
	}
	return true
}

// ToCommands converts the SessionState's command list back to types.Command slices.
func ToCommands(state *SessionState) []*types.Command {
	cmds := make([]*types.Command, len(state.Commands))
	for i, sc := range state.Commands {
		cmds[i] = types.FromSessionCommand(sc)
	}
	return cmds
}
