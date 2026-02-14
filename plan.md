# Implementation Plan: `autoclaude init` Subcommand

## Overview

Add an `init` subcommand that generates a sample `autoclaude.toml` file in the current working directory. The subcommand supports a `--force` flag to overwrite an existing file.

---

## Files to Modify

### 1. `main.go`

**What to change:** Insert a subcommand check before flag parsing (between line 59 `func main() {` and line 61 where flag variables are declared).

**Exact changes:**

- Add `"path/filepath"` is already imported (line 9) — no import changes needed for path handling.
- After `func main() {` (line 59) and before the flag variable declarations (line 61), add a subcommand intercept block:

```
// Subcommand handling — check before flag.Parse() so "init" isn't
// consumed as an unknown flag.
if len(os.Args) >= 2 && os.Args[1] == "init" {
    force := false
    for _, arg := range os.Args[2:] {
        if arg == "--force" {
            force = true
        } else {
            fmt.Fprintf(os.Stderr, "Unknown flag for init: %s\n")
            os.Exit(1)
        }
    }
    wd, err := os.Getwd()
    if err != nil {
        fmt.Fprintf(os.Stderr, "Error getting working directory: %v\n", err)
        os.Exit(1)
    }
    if err := config.GenerateSampleConfig(wd, force); err != nil {
        fmt.Fprintf(os.Stderr, "Error: %v\n", err)
        os.Exit(1)
    }
    os.Exit(0)
}
```

- **Why before flag parsing:** `flag.Parse()` will error on `init` as a positional arg is fine, but `--force` would be an unknown flag. Intercepting early avoids conflicts with the existing flag set entirely.

**Update `usage()` function** (lines 33–57):

- Add `init` subcommand to the Usage section. Modify the `Usage:` block to show:
  ```
  Usage:
    autoclaude [flags]
    autoclaude init [--force]
  ```
- Add a `Subcommands:` section after the Usage block and before the Flags section:
  ```
  Subcommands:
    init            Generate a sample autoclaude.toml in the current directory
      --force       Overwrite existing autoclaude.toml
  ```

---

### 2. `internal/config/init.go` (NEW FILE)

**Create this file** in the `internal/config/` directory.

**Package:** `config`

**Imports:** `fmt`, `os`, `path/filepath`

**Function signature:**

```go
func GenerateSampleConfig(dir string, force bool) error
```

**Behavior:**

1. Construct the target path: `filepath.Join(dir, "autoclaude.toml")`
2. Check if the file already exists using `os.Stat()`:
   - If it exists AND `force` is `false`: return an error with message `"autoclaude.toml already exists in <dir>. Use --force to overwrite."`
   - If it exists AND `force` is `true`: proceed to overwrite
   - If it does not exist: proceed to create
3. Write the sample TOML content (defined as a raw string constant in the same file) using `os.WriteFile()` with permissions `0644`
4. Print to stdout:
   ```
   Created autoclaude.toml in <dir>
   Edit the file to add your commands, then run: autoclaude
   ```
5. Return `nil` on success

**Constant to define in the file:**

```go
const sampleConfig = `...`
```

The sample config content (raw string literal):

```toml
# autoclaude configuration file
# Run with: autoclaude -f autoclaude.toml
# Or just run `autoclaude` in this directory (auto-detected)

# Global settings
max_retries = 3
# work_dir = "."  # defaults to current directory

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
prompt = "Describe a task with custom retry limit."
verify = "go test ./..."
max_retries = 5  # overrides the global max_retries for this command
```

**Edge cases to handle:**

- `dir` does not exist or is not writable → `os.WriteFile` will return an error, which gets propagated
- `dir` is empty string → should not happen since caller resolves via `os.Getwd()`, but `filepath.Join` handles it gracefully
- File exists as a directory named `autoclaude.toml` → `os.WriteFile` will fail with a descriptive OS error
- Existing file is a symlink → `os.Stat` follows symlinks, so the existence check works correctly; `os.WriteFile` will overwrite the symlink target (acceptable behavior with `--force`)

---

### 3. `README.md`

**What to change:** Add documentation for the `init` subcommand in three places.

#### 3a. Add Quick Start section (NEW)

Insert a new `## Quick Start` section between the `## Installation` section (ends at line 24) and the `## Usage` section (starts at line 26).

Content:

```markdown
## Quick Start

```sh
autoclaude init       # generates a sample autoclaude.toml
# edit autoclaude.toml to add your commands
autoclaude            # runs the commands
```
```

#### 3b. Add init subcommand documentation in Usage section

Insert a new subsection after the `## Usage` intro paragraph (line 28) and before `### 1. Interactive TUI` (line 30). This becomes a "step zero" in the workflow.

Content:

```markdown
### Generating a config file

Use `init` to create a sample config file in the current directory:

```sh
autoclaude init
```

This creates `autoclaude.toml` with a well-commented template showing the available options. If the file already exists, autoclaude will refuse to overwrite it:

```sh
autoclaude init --force   # overwrite existing autoclaude.toml
```
```

#### 3c. Add to Flag reference table

Insert a row in the flag reference table (after line 127, the `--clear-session` row and before the `-h` row) — actually, since `init` is a subcommand not a flag, instead add a new section.

Insert a `## Subcommands` section between the `## Flag reference` section (ends at line 128) and the `## Command lifecycle` section (starts at line 129).

Content:

```markdown
## Subcommands

| Subcommand | Description |
|------------|-------------|
| `init` | Generate a sample `autoclaude.toml` in the current directory |
| `init --force` | Overwrite an existing `autoclaude.toml` |
```

---

## Summary of All Changes

| File | Action | Lines Affected |
|------|--------|---------------|
| `main.go` | Modify | Insert subcommand check after line 59 (before flag vars), update `usage()` at lines 33–57 |
| `internal/config/init.go` | Create | New file (~45 lines) with `GenerateSampleConfig()` function and `sampleConfig` constant |
| `README.md` | Modify | Insert Quick Start after line 24, init docs after line 28, Subcommands section after line 128 |

## What NOT to Change

- `internal/config/config.go` — no changes needed; the init logic is independent
- `example.autoclaude.toml` — keep as-is; it serves a different purpose (detailed reference example vs. the minimal getting-started template from `init`)
- No new dependencies required — only uses `os`, `fmt`, `path/filepath` from stdlib
- No changes to the TUI, runner, session, or types packages
