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
- `runClaude` and `runVerify` are thin wrappers around `runCommandStreaming`. They return `(string, CommandResult, error)` — the `CommandResult` carries separated stdout/stderr/exit code for attempt logging.
- `captureGitContext()` runs `git branch --show-current` and `git status --porcelain` via `exec.Command` (not streaming). Errors are silently ignored (returns empty strings if git is unavailable).
- **`sendUpdate` signature**: `sendUpdate(index, status, output, detail)` — the `detail` string (e.g. "Attempt 2/3") is passed as `StatusUpdateMsg.StatusDetail` and displayed next to the spinner in the TUI. Pass `""` for non-attempt-tracked steps (documenting, committing, success, final failure).

### Attempt logging

- **Type definitions** (`internal/types/types.go`): `AttemptLog` records per-attempt details: timing (start/end/duration), failed step, exit code, stdout/stderr, working directory, and git state (branch, status). `Command.FormatFailureReport()` renders all attempt logs into a human-readable debug report. Session persistence uses `SessionAttemptLog` (JSON-friendly mirror) with RFC3339 times and millisecond durations.
- **Population** (`internal/runner/runner.go`): `executeSingle` populates `cmd.AttemptLogs` on every attempt. Two closures handle this:
  - `finalizeAttempt` — called once per retry-loop iteration (both success and failure). Sets EndedAt/Duration/ExitCode/FailedStep, copies accumulated stdout/stderr from `strings.Builder` accumulators, and appends to `cmd.AttemptLogs`.
  - `updateLastAttempt` — called for post-loop steps (documenting, committing) that extend the final attempt's log in place. Only overwrites FailedStep/ExitCode if the step actually failed (non-empty failedStep), preventing a successful commit from clearing a prior "Documenting" failure.
- Pre-attempt context: each iteration captures git branch/status via `captureGitContext()` and records WorkDir before any steps run.
- Stdout/stderr from each step (`runClaude`/`runVerify`) are accumulated into per-attempt `strings.Builder` buffers, then snapshotted into the AttemptLog at finalization.

### Retry mechanism (internal/runner/runner.go)

- **MaxRetries means total attempts**, not additional retries. `MaxRetries=3` means at most 3 executions. The loop runs `for cmd.Attempts = 1; cmd.Attempts <= cmd.MaxRetries; cmd.Attempts++`.
- **Retry-eligible failures** (Planning, Running, Verifying): increment attempts, check `cmd.Attempts < cmd.MaxRetries`, and if retries remain: append a retry separator to `cmd.Output` (`═══ RETRY N/M ═══ (previous attempt failed at: <step>, exit code: <code>)`), set `StatusRetrying`, sleep 2s (interruptible via `r.ctx.Done()`), clear `cmd.PlanOutput`, and `continue` to restart from Planning.
- **Non-fatal steps** (Documenting, Committing): failures are logged as warnings in output but do NOT trigger retries. The command still reaches `StatusSuccess`.
- **Verification failure retries the full cycle** — `cmd.PlanOutput` is cleared so the next attempt starts from Planning, not just re-verification.
- **Attempt detail in TUI**: `StatusUpdateMsg.StatusDetail` carries "Attempt N/M" for retry-eligible steps. The TUI displays this next to the spinner via `model.statusDetail`.

### Execution flow

Each command goes through: Pending -> Planning -> Running -> Verifying -> Documenting -> Committing -> Success. On failure, it retries from Planning with a fresh plan up to `max_retries` times (where `max_retries` is the total attempt count, not additional retries).

### Dependencies

- `github.com/charmbracelet/bubbletea` — TUI framework
- `github.com/BurntSushi/toml` — Config parsing
