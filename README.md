# autoclaude

A terminal UI for running a series of [Claude Code](https://docs.anthropic.com/en/docs/claude-code) commands with optional verification and automatic retry support. Queue up multiple prompts, attach shell-based verification commands, and let autoclaude execute them sequentially with git commit/push on success.

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

## Usage

autoclaude supports three input methods that can be mixed and matched.

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

## Flag reference

| Short | Long | Type | Default | Description |
|-------|------|------|---------|-------------|
| `-f` | `--file` | string | — | Path to a TOML config file |
| `-c` | `--cmd` | string | — | Prompt to run (repeatable). Format: `"prompt"` or `"prompt::verify"` |
| `-r` | `--max-retries` | int | `3` | Maximum retries per command |
| `-w` | `--work-dir` | string | current dir | Working directory for execution |
| `-a` | `--auto-run` | bool | `false` | Skip TUI review, start immediately |
| `-h` | `--help` | bool | `false` | Show help message |

## Command lifecycle

Each command moves through a sequence of states:

```
Pending → Running → Verifying → Committing → Success
                  ↘            ↘           ↘
                   Retrying ──→ Failed
```

| State | Description |
|-------|-------------|
| **Pending** | Queued and waiting to execute |
| **Running** | Claude Code is processing the prompt |
| **Verifying** | The `verify` shell command is running (skipped if no verify command is set) |
| **Committing** | Claude is committing and pushing the changes via git |
| **Success** | Command completed and changes were committed |
| **Failed** | All retry attempts exhausted — execution stops |
| **Retrying** | Claude or verification failed; trying again |

### Retry behavior

- When Claude or the verification step fails, the command enters **Retrying** and the full cycle repeats from **Running**.
- Each command tracks its own attempt count against its `max_retries` limit.
- Per-command `max_retries` in the TOML config overrides the global value.
- The CLI `--max-retries` flag sets the global default.
- On the **first permanent failure**, execution halts — remaining commands stay Pending.

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

### Running view

| Key | Action |
|-----|--------|
| `j` / `Down` | Scroll output down |
| `k` / `Up` | Scroll output up |
| `q` | Quit (after execution completes) |
| `Ctrl+C` | Force quit |

## License

See [LICENSE](LICENSE) for details.
