package config

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
	"github.com/manasm11/autoclaude/internal/types"
)

// ConfigCommand represents a single command entry in the TOML config file.
type ConfigCommand struct {
	Prompt     string `toml:"prompt"`
	Verify     string `toml:"verify"`
	MaxRetries int    `toml:"max_retries"`
}

// ConfigFile represents the top-level structure of an autoclaude TOML config file.
type ConfigFile struct {
	MaxRetries int             `toml:"max_retries"`
	WorkDir    string          `toml:"work_dir"`
	Commands   []ConfigCommand `toml:"command"`
}

// DetectConfigFile looks for a config file in dir, checking autoclaude.toml
// then .autoclaude.toml. Returns the path if found, or empty string if neither exists.
func DetectConfigFile(dir string) string {
	for _, name := range []string{"autoclaude.toml", ".autoclaude.toml"} {
		p := filepath.Join(dir, name)
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

// LoadConfig reads and parses a TOML config file from the given path.
// It validates that every command has a non-empty prompt.
func LoadConfig(path string) (*ConfigFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config file: %w", err)
	}

	var cfg ConfigFile
	if err := toml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing config file: %w", err)
	}

	// Default max_retries to 3 if not set.
	if cfg.MaxRetries == 0 {
		cfg.MaxRetries = 3
	}

	// Default work_dir to current working directory if not set.
	if cfg.WorkDir == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return nil, fmt.Errorf("getting current directory: %w", err)
		}
		cfg.WorkDir = cwd
	}

	for i, cmd := range cfg.Commands {
		if cmd.Prompt == "" {
			return nil, fmt.Errorf("command[%d]: prompt is required", i)
		}
	}

	return &cfg, nil
}

// ToCommands converts the parsed config into a slice of types.Command.
// Per-command max_retries overrides the global value when set.
func (cfg *ConfigFile) ToCommands() []*types.Command {
	cmds := make([]*types.Command, len(cfg.Commands))
	for i, cc := range cfg.Commands {
		retries := cfg.MaxRetries
		if cc.MaxRetries != 0 {
			retries = cc.MaxRetries
		}
		cmds[i] = &types.Command{
			Prompt:     cc.Prompt,
			Verify:     cc.Verify,
			MaxRetries: retries,
			Status:     types.StatusPending,
		}
	}
	return cmds
}
