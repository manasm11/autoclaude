package config

import (
	"fmt"
	"os"
	"path/filepath"
)

const sampleConfig = `# autoclaude configuration file
# Run with: autoclaude -f autoclaude.toml
# Or just run ` + "`autoclaude`" + ` in this directory (auto-detected)

# Global settings
max_retries = 3
# work_dir = "."  # defaults to current directory
# update_docs = true  # auto-update CLAUDE.md and README.md after each command (default: true)
# auto_fix = true  # auto-fix failures using Claude (default: true)

# Each [[command]] block defines a Claude Code task to run sequentially.
# After each successful command, changes are auto-committed and pushed.

[[command]]
prompt = """
Describe your first task here.
You can use multi-line strings for complex prompts.
Be specific about what files to create/modify and the expected behavior.
"""
verify = "go build ./..."  # optional: shell command to verify the change

[[command]]
prompt = "Describe your second task here."
# verify is optional — omit it to skip verification

[[command]]
prompt = "Describe a task with custom fix attempt limit."
verify = "go test ./..."
max_retries = 5  # overrides the global max_retries for this command
`

// GenerateSampleConfig writes a sample autoclaude.toml to the given directory.
// If the file already exists and force is false, it returns an error.
// Returns the full path of the created file.
func GenerateSampleConfig(dir string, force bool) (string, error) {
	path := filepath.Join(dir, "autoclaude.toml")

	if !force {
		if _, err := os.Stat(path); err == nil {
			return "", fmt.Errorf("autoclaude.toml already exists. Use --force to overwrite.")
		}
	}

	if err := os.WriteFile(path, []byte(sampleConfig), 0644); err != nil {
		return "", fmt.Errorf("failed to write config file: %w", err)
	}

	return path, nil
}
