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

```sh
autoclaude [--max-retries N] [--work-dir DIR]
```

| Flag | Default | Description |
|------|---------|-------------|
| `--max-retries` | `3` | Maximum retry attempts per command |
| `--work-dir` | current directory | Working directory for command execution |

## Workflow

1. **Input** -- Type a Claude Code prompt and press `ctrl+s` to submit.
2. **Verify** -- Optionally enter a shell command to verify the result (e.g. `go test ./...`). Press `Enter` to add to the queue or `Esc` to go back.
3. **Queue** -- Review queued commands, reorder or delete entries. Press `Tab` to switch between Input and Queue views.
4. **Run** -- Press `ctrl+r` to start execution. Each command runs Claude Code, optionally verifies, retries on failure, and commits/pushes on success.

## Keybindings

### Input View

| Key | Action |
|-----|--------|
| `ctrl+s` | Submit prompt and enter verify step |
| `Tab` | Switch to Queue view |
| `ctrl+r` | Run all queued commands |
| `ctrl+q` | Quit |

### Verify View

| Key | Action |
|-----|--------|
| `Enter` | Add command to queue |
| `Esc` | Cancel and return to prompt |
| `ctrl+q` | Quit |

### Queue View

| Key | Action |
|-----|--------|
| `j` / `Down` | Move selection down |
| `k` / `Up` | Move selection up |
| `d` | Delete selected command |
| `Tab` / `Esc` | Return to Input view |
| `ctrl+r` | Run all queued commands |
| `ctrl+q` | Quit |

### Running View

| Key | Action |
|-----|--------|
| `j` / `Down` | Scroll output down |
| `k` / `Up` | Scroll output up |
| `q` | Quit (after execution completes) |
| `ctrl+c` | Force quit |

## License

See [LICENSE](LICENSE) for details.
