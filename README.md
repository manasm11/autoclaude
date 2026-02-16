# autoclaude

A terminal UI for running a series of [Claude Code](https://docs.anthropic.com/en/docs/claude-code) commands with optional verification and automatic retry support. Each command goes through a two-step plan-then-execute flow: Claude first generates a detailed implementation plan (read-only, no file changes), then executes that plan in a second invocation. Queue up multiple prompts, attach shell-based verification commands, and let autoclaude execute them sequentially with git commit/push on success.

## Prerequisites

- [Go](https://go.dev/dl/) 1.21+
- [Claude Code CLI](https://docs.anthropic.com/en/docs/claude-code) installed and available in your `PATH`

## Installation

### From source

```sh
git clone https://github.com/manasm11/autoclaude.git
cd autoclaude
go build -o autoclaude .
```

### Using go install

```sh
go install github.com/manasm11/autoclaude@latest
```

## Quick Start

```sh
# 1. Generate a sample config file (recommended first step)
autoclaude init

# 2. Edit the generated autoclaude.toml with your commands
$EDITOR autoclaude.toml

# 3. Run autoclaude
autoclaude
```

## Usage

autoclaude supports three input methods that can be mixed and matched.

### Generating a config file

Use the `init` subcommand to create a sample `autoclaude.toml` in the current directory:

```sh
autoclaude init
```

If `autoclaude.toml` already exists, the command will refuse to overwrite it. Use `--force` to overwrite:

```sh
autoclaude init --force
```

### 1. Interactive TUI

Launch without arguments to get the full interactive terminal UI:

```sh
autoclaude
```

1. **Input** — Type a Claude Code prompt and press `Ctrl+S` to submit.
2. **Verify** — Optionally enter a shell command to verify the result (e.g. `go test ./...`). Press `Enter` to add to the queue or `Esc` to skip.
3. **Queue** — Review queued commands, reorder or delete entries. Press `Tab` to switch between Input and Queue views.
4. **Run** — Press `Ctrl+R` to start execution.

### 2. TOML config file

Define commands in a TOML file and load them with `-f`:

```sh
autoclaude -f autoclaude.toml
```

#### Config file format

```toml
# Global settings
max_retries = 5
work_dir = "/path/to/project"

# Each [[command]] block is one Claude Code prompt.
[[command]]
prompt = "Add error handling to the API layer"

[[command]]
prompt = "Write unit tests for the auth module"
verify = "go test ./internal/auth/..."

[[command]]
prompt = "Refactor the DB pool"
verify = "go test -race ./internal/db/..."
max_retries = 10
```

| Field | Scope | Required | Default | Description |
|-------|-------|----------|---------|-------------|
| `max_retries` | global | no | `3` | Default retry limit for all commands |
| `work_dir` | global | no | current dir | Working directory for execution |
| `update_docs` | global | no | `true` | Auto-update CLAUDE.md and README.md after each command |
| `prompt` | command | **yes** | — | The Claude Code prompt to run |
| `verify` | command | no | — | Shell command to verify success (exit 0 = pass) |
| `max_retries` | command | no | global value | Per-command retry override |

See [`example.autoclaude.toml`](example.autoclaude.toml) for a fully commented example.

### 3. CLI flags

Pass commands directly on the command line with `-c`. Use `::` to attach a verification command:

```sh
# Single command
autoclaude -c "Add error handling to the API layer"

# Multiple commands
autoclaude -c "Add error handling" -c "Write unit tests::go test ./..."

# Command with verification
autoclaude -c "Refactor auth module::go test ./internal/auth/..."
```

### Combined usage

File and CLI commands can be mixed — CLI commands are appended after file commands:

```sh
autoclaude -f base.toml -c "One more fix::go build ./..."
```

### Auto-run (CI / scripted usage)

Skip the TUI queue review and start execution immediately with `--auto-run`:

```sh
autoclaude -f commands.toml --auto-run
autoclaude -c "Fix the bug::go test ./..." --auto-run
```

`--auto-run` requires at least one command via `--file` or `--cmd`.

## Subcommands

| Subcommand | Description |
|------------|-------------|
| `autoclaude init` | Generate a sample `autoclaude.toml` in the current directory |
| `autoclaude init --force` | Overwrite an existing `autoclaude.toml` |

## Flag reference

| Short | Long | Type | Default | Description |
|-------|------|------|---------|-------------|
| `-f` | `--file` | string | — | Path to a TOML config file |
| `-c` | `--cmd` | string | — | Prompt to run (repeatable). Format: `"prompt"` or `"prompt::verify"` |
| `-r` | `--max-retries` | int | `3` | Maximum retries per command |
| `-w` | `--work-dir` | string | current dir | Working directory for execution |
| `-a` | `--auto-run` | bool | `false` | Skip TUI review, start immediately |
| `-R` | `--no-resume` | bool | `false` | Skip session detection and start fresh |
| | `--no-docs` | bool | `false` | Skip automatic documentation update step |
| | `--clear-session` | bool | `false` | Delete any existing session file and exit |
| `-h` | `--help` | bool | `false` | Show help message |

## Command lifecycle

Each command moves through a sequence of states:

```
Pending → Planning → Running → Verifying → Documenting → Committing → Success
                   ↘         ↘            ↘              ↘           ↘
                    Retrying ─────────────────────────→ Failed
```

| State | Description |
|-------|-------------|
| **Pending** | Queued and waiting to execute |
| **Planning** | Claude is generating a step-by-step implementation plan (read-only, no file changes) |
| **Running** | Claude Code is executing the implementation plan |
| **Verifying** | The `verify` shell command is running (skipped if no verify command is set) |
| **Documenting** | Claude is updating CLAUDE.md and README.md to reflect the changes (non-fatal if it fails) |
| **Committing** | Claude is committing and pushing the changes via git |
| **Success** | Command completed and changes were committed |
| **Failed** | All retry attempts exhausted — execution stops |
| **Retrying** | Claude or verification failed; trying again |

### Retry behavior

- When Claude or the verification step fails, the command enters **Retrying** and the full cycle repeats from **Planning** with a fresh plan.
- Each command tracks its own attempt count against its `max_retries` limit.
- Per-command `max_retries` in the TOML config overrides the global value.
- The CLI `--max-retries` flag sets the global default.
- On the **first permanent failure**, execution halts — remaining commands stay Pending.

### Attempt logging

Each retry attempt is recorded in a detailed attempt log capturing:

- Timing (start, end, duration)
- Which step failed (Planning, Running, Verifying, etc.)
- Exit code, stdout, and stderr
- Working directory and git state (branch, status)

Attempt logs are persisted in the session file and survive session resume. When a command fails permanently, `FormatFailureReport()` produces a readable debug report showing all attempts with their outputs — useful for diagnosing flaky tests or intermittent failures.

## Automatic documentation updates

After each command completes verification (or execution if no verify step is set), autoclaude automatically asks Claude to update project documentation before committing:

- **CLAUDE.md** — The project memory file for Claude Code. Updated with new conventions, architecture decisions, file structure changes, dependencies, and patterns established by the recent changes. Created if it doesn't exist.
- **README.md** — User-facing documentation. Updated with new features, usage changes, API changes, or configuration options. Created if it doesn't exist. Existing content is preserved unless outdated.

Only sections relevant to the recent changes are modified. If no documentation updates are needed, no changes are made.

### Non-fatal behavior

Documentation updates are **non-fatal** — if the step fails, a warning is appended to the command output and execution proceeds to the commit step. The actual work is done; docs are nice-to-have.

### Disabling documentation updates

Use the `--no-docs` flag to skip the documentation step entirely:

```sh
autoclaude -f commands.toml --no-docs
```

Or disable it in your TOML config file:

```toml
update_docs = false
```

The `--no-docs` flag takes priority over the TOML `update_docs` setting.

## Session resume

autoclaude automatically saves execution progress to a session file so you can resume after interruptions (Ctrl+C, crash, terminal close, etc.).

### How it works

During execution, autoclaude writes `.autoclaude-session.json` to the working directory after every status change. This file records each command's status, output, attempts, and the current position.

If execution is interrupted, the next time you run autoclaude in the same directory, a **Resume** screen appears showing:

- Which commands completed, failed, or are still pending
- A relative timestamp ("2 hours ago", "yesterday") showing when the session was interrupted
- The exact command where execution will resume from

You can then choose to:
- **`r`** — Resume from where it left off
- **`n`** — Discard the session and start fresh
- **`q`** — Quit (session file is preserved for next time)

### Auto-resume with `--auto-run`

When `--auto-run` is used and a session file exists, autoclaude skips the TUI prompt and automatically resumes from where it left off. This is the expected behavior for CI/scripted usage. A message is logged to stdout:

```
Resuming previous session from command 3/5
```

### When the session file is cleared

- **All commands succeed** — the session file is automatically deleted
- **`--no-resume` flag** — the session file is deleted on startup
- **`--clear-session` flag** — the session file is deleted and autoclaude exits
- **Pressing `n`** on the Resume screen — the session file is deleted

### Edge cases

- **Corrupted file**: If the session file contains invalid JSON, autoclaude prints a warning ("Session file corrupted, starting fresh"), deletes it, and proceeds normally.
- **Directory mismatch**: If the session was saved in a different working directory, autoclaude warns you and asks for confirmation before resuming.
- **All commands already succeeded**: If a session file exists but every command has status "Success" (e.g. the cleanup step after completion failed), autoclaude silently clears it and proceeds normally.

### Session file location

The session file is always named `.autoclaude-session.json` and is placed in the working directory. It is included in `.gitignore` by default — you should not commit it.

## Config file auto-detection

When no `--file` flag is provided, autoclaude automatically looks for a config file in the working directory in this order:

1. `autoclaude.toml`
2. `.autoclaude.toml`

If found, commands are loaded automatically — the same as passing `--file`. The TUI queue view shows a subtle info line (e.g. "Loaded 3 commands from autoclaude.toml") so you know where the commands came from.

If the auto-detected file has parse errors, autoclaude prints the error and exits. Since you placed the file there intentionally, silent failures would be confusing.

If `--file` is explicitly provided, auto-detection is skipped — the explicit flag always wins.

```sh
# Just run autoclaude in a directory with autoclaude.toml — commands load automatically
cd my-project
autoclaude
```

## Config file naming convention

The suggested name is **`autoclaude.toml`**, placed in your project root. This makes it easy to commit alongside your code and share with teammates. Use `.autoclaude.toml` if you prefer a hidden dotfile.

The `-f` flag accepts any path, so you can organize configs however you like:

```sh
autoclaude -f autoclaude.toml              # project root
autoclaude -f configs/refactor.toml        # subdirectory
autoclaude -f ~/templates/standard.toml    # absolute path
```

If you keep multiple config files per project, a descriptive name helps:

```
autoclaude.toml          # default / main
autoclaude.tests.toml    # test-focused commands
autoclaude.refactor.toml # refactoring batch
```

## Keybindings

### Input view

| Key | Action |
|-----|--------|
| `Ctrl+S` | Submit prompt and enter verify step |
| `Tab` | Switch to Queue view |
| `Ctrl+R` | Run all queued commands |
| `Ctrl+Q` | Quit |

### Verify view

| Key | Action |
|-----|--------|
| `Enter` | Add command to queue |
| `Esc` | Cancel and return to prompt |
| `Ctrl+Q` | Quit |

### Queue view

| Key | Action |
|-----|--------|
| `j` / `Down` | Move selection down |
| `k` / `Up` | Move selection up |
| `d` | Delete selected command |
| `Tab` / `Esc` | Return to Input view |
| `Ctrl+R` | Run all queued commands |
| `Ctrl+Q` | Quit |

### Resume view

| Key | Action |
|-----|--------|
| `r` | Resume execution from where it left off |
| `n` | Discard session and start fresh |
| `q` | Quit (session preserved) |

### Running view

| Key | Action |
|-----|--------|
| `j` / `Down` | Scroll output down |
| `k` / `Up` | Scroll output up |
| `q` | Quit (after execution completes) |
| `Ctrl+C` | Force quit |

## License

See [LICENSE](LICENSE) for details.
