# CLAUDE.md

Project memory file for Claude Code sessions on `autoclaude`.

## Project Overview

`autoclaude` is a Go terminal UI that queues and runs Claude Code prompts sequentially with a plan-then-execute flow, optional verification, automatic documentation updates, and git commit/push.

## Build & Test

```sh
go build -o autoclaude .
go test ./...
```

## Project Structure

- `main.go` — CLI entry point (flag parsing, config loading, TUI launch)
- `internal/runner/` — Command execution engine
- `internal/types/` — Shared types (`Command`, `CommandStatus`, `SessionCommand`, `AttemptLog`)
- `internal/session/` — Session persistence (`.autoclaude-session.json`)
- `internal/tui/` — BubbleTea TUI views (input, queue, running, resume)
- `internal/config/` — TOML config parsing

## Architecture Notes

### Command execution (internal/runner/runner.go)

- `runCommandStreaming` is the core function for all subprocess execution (Claude invocations and verify commands). It streams output line-by-line to the TUI via BubbleTea messages.
- **Separated stdout/stderr capture**: `runCommandStreaming` returns a `CommandResult` struct containing separate `Stdout`, `Stderr`, and `ExitCode` fields. Uses `io.TeeReader` to simultaneously stream to the TUI and buffer stdout/stderr independently.
- **Exit code extraction**: On error, the exit code is extracted via type assertion to `*exec.ExitError`. If the error is not an `ExitError` (e.g. process couldn't start), exit code is `-1`.
- `runClaude` and `runVerify` are thin wrappers around `runCommandStreaming`. They currently use only the combined output string and error; the `CommandResult` is available for future use.

### Attempt logging (internal/types/types.go)

- `AttemptLog` records per-attempt details: timing (start/end/duration), failed step, exit code, stdout/stderr, working directory, and git state (branch, status).
- `Command.AttemptLogs` accumulates one entry per retry attempt.
- `Command.FormatFailureReport()` renders all attempt logs into a human-readable debug report with indented stdout/stderr.
- Session persistence uses `SessionAttemptLog` (JSON-friendly mirror of `AttemptLog`) with time as RFC3339 strings and duration as milliseconds. Conversion helpers: `ToSessionCommand()` / `FromSessionCommand()` handle the mapping.

### Execution flow

Each command goes through: Pending -> Planning -> Running -> Verifying -> Documenting -> Committing -> Success. On failure, it retries from Planning with a fresh plan up to `max_retries` times.

### Dependencies

- `github.com/charmbracelet/bubbletea` — TUI framework
- `github.com/BurntSushi/toml` — Config parsing
