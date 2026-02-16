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
- `internal/types/` — Shared types (`Command`, `CommandStatus`, `SessionCommand`, `AttemptLog`, `BuildFixPrompt`)
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
- **Population** (`internal/runner/runner.go`): `executeSingle` populates `cmd.AttemptLogs` on every attempt (initial + fix attempts). Three closures handle this:
  - `finalizeAttempt` — called once per attempt (initial or fix). Sets EndedAt/Duration/ExitCode/FailedStep, copies accumulated stdout/stderr from `strings.Builder` accumulators, and appends to `cmd.AttemptLogs`.
  - `updateLastAttempt` — called for post-loop steps (documenting, committing) that extend the final attempt's log in place. Only overwrites FailedStep/ExitCode if the step actually failed (non-empty failedStep), preventing a successful commit from clearing a prior "Documenting" failure.
  - `recordFailure` — stores failure details (`LastFailedStep`, `LastExitCode`, `LastStdout`, `LastStderr`) on `cmd` for `BuildFixPrompt()` consumption.
- Pre-attempt context: each attempt (initial and fix) captures git branch/status via `captureGitContext()` and records WorkDir before any steps run.
- Stdout/stderr from each step (`runClaude`/`runVerify`) are accumulated into per-attempt `strings.Builder` buffers, then snapshotted into the AttemptLog at finalization.

### Auto-fix mechanism (internal/runner/runner.go)

The old retry-from-planning loop has been replaced with an auto-fix system. On failure, Claude analyzes the error and makes targeted code fixes, then only re-runs verification (not the full plan+execute cycle).

#### Type-level support (internal/types/types.go)

- **`StatusFixing`** (iota 9): Command status for when Claude is auto-fixing a failure. Appended after `StatusRetrying` — no existing iota values shift.
- **Auto-fix fields on `Command`**: `LastFailedStep` (string: `"planning"`, `"execution"`, or `"verification"`), `LastExitCode` (int), `LastStderr`/`LastStdout` (string), `FixAttempts` (int counter). These track the most recent failure and are replaced (not accumulated) on each failure. Zero values mean "no failure recorded".
- **`BuildFixPrompt()`**: Method on `*Command` that constructs a prompt for Claude to fix the issue. Includes: original task prompt, failed step, exit code, plan (if available), stdout, stderr, and a clear instruction to make targeted fixes. Conditionally omits empty sections (PlanOutput, LastStdout, LastStderr).
- **`StatusRetrying`** is still present in types for backward compatibility (preserves iota values for persisted sessions). Not used in runner or TUI — will be removed in a future cleanup step.

#### Runner auto-fix flow (internal/runner/runner.go)

- **`executeSingle` structure**: Linear initial attempt (plan → execute → verify) followed by a `fixLoop:` label for auto-fix iterations. Uses `goto fixLoop` from three failure points (planning, execution, verification) and `goto success` when the initial attempt passes. No retry loop wrapping the initial attempt.
- **`recordFailure` closure**: Populates `cmd.LastFailedStep`, `cmd.LastExitCode`, `cmd.LastStdout`, `cmd.LastStderr` before entering the fix loop. These fields are read by `cmd.BuildFixPrompt()`.
- **`sendFailed` closure**: Encapsulates the failure path — sets `StatusFailed`, saves session, writes failure report, sends `ExecutionErrorMsg`.
- **Fix loop semantics**: `cmd.FixAttempts` incremented at start of each iteration. Budget check: `FixAttempts >= MaxRetries` → fail. With `MaxRetries=3`: initial attempt + 2 fix attempts = 3 total tries.
- **Fix attempt flow**: `StatusFixing` → 2s sleep (interruptible) → run Claude with `cmd.BuildFixPrompt()` → re-verify only (or check fix exit code if no verify command). Does NOT re-plan or re-execute.
- **Fix attempt output separators**: `═══ FIX ATTEMPT N/M ═══` with failed step, exit code, and last 50 lines of stderr. `lastNLines(s, n)` helper truncates stderr.
- **Fix attempt logging**: Each fix attempt gets its own `AttemptLog` with `AttemptNumber = cmd.Attempts + cmd.FixAttempts` (unique sequential). `FailedStep` is `"auto-fix"` on failure, `""` on success.
- **After successful fix**: `cmd.PlanOutput` is cleared (fix was applied directly to code). Proceeds to documenting/committing.
- **No verify command**: If `cmd.Verify == ""`, fix is assumed successful when Claude exits with code 0. Non-zero fix exit code triggers another fix attempt.
- **Non-fatal steps** (Documenting, Committing): unchanged — failures are warnings, never trigger fix attempts.
- **TUI fix detail**: `StatusUpdateMsg.StatusDetail` carries `"Fix N/M"` during fix attempts. The TUI displays this next to the spinner.

### TUI auto-fix display (internal/tui/model.go)

- **Status flow breadcrumb**: `renderStatusFlow(current, fixAttempts)` renders a colored breadcrumb in the execution view header: `Plan → Run → Verify → [Fix → Verify]* → Docs → Commit`. Completed steps are green, the active step is bold white, future steps are dim. The `Fix → Verify` cycle only appears if `fixAttempts > 0` or `current == StatusFixing`. Uses `isStatusBefore()` with a status→order map to determine coloring.
- **Enhanced fixing view**: When `cmd.Status == StatusFixing`, the execution view shows: (1) what failed — e.g. "Verification failed (exit code 1)" via `capitalize(cmd.LastFailedStep)`, (2) last 10 lines of `cmd.LastStderr` via `lastNLines(s, 10)`, (3) fix attempt counter "Fix attempt N/M" styled with `statusFixing`. Falls back to `maxFix = 3` if `cmd.MaxRetries < 1`.
- **Failure panel**: `viewFailurePanel()` header shows "Command failed after N auto-fix attempt(s)" when `cmd.FixAttempts > 0`. When `len(cmd.AttemptLogs) > 1`, renders a per-attempt history table: attempt number, failed step (or "success"), exit code, and duration.
- **Queue icon**: `statusIcon(StatusFixing)` returns `⚡` (U+26A1). Previously was `↻` (U+21BB).
- **Helper functions**: `capitalize(s)` uppercases first char. `lastNLines(s, n)` returns last N lines of a string. `isStatusBefore(a, b)` compares status ordering via a map. `renderStatusFlow(current, fixAttempts)` builds the colored breadcrumb string.

### Failure reporting (internal/runner/runner.go)

- **`writeFailureReport(cmd)`**: Formats a timestamped failure report header (prompt truncated to 100 chars, RFC3339 timestamp, box-drawing separators) + `cmd.FormatFailureReport()` body. Appends to `autoclaude-error.log` in `r.WorkDir` via `os.OpenFile` with `O_APPEND|O_CREATE|O_WRONLY`. File write errors are non-fatal (warning appended to `cmd.Output`). Returns the full report string.
- **`ExecutionErrorMsg`** carries `FailureReport string` alongside `CmdIndex` and `Err`. Sent from `sendFailed` closure when fix attempts are exhausted.
- The TUI stores `failureReport`, `failedCmdIndex`, `showExpandedLog`, and `failureScrollOff` on `Model`. The failure panel (`viewFailurePanel()`) shows command info + scrollable last-attempt stderr (compact) or full report (expanded via `l` key). Scroll is separate from the main output scroll (`failureScrollOff` vs `scrollOffset`).

### Session resume and retry budget (internal/tui/model.go)

- **Retry budget preservation on resume**: When resuming a session, the `resumeRunMsg` handler sets `Attempts = len(AttemptLogs)` for the resume-from command. This ensures the retry budget accounts for previous attempts — if a command used 2/3 attempts before interruption, it only gets 1 more on resume.
- **`--reset-attempts` flag**: `Model.resetAttempts` (set via `SetResetAttempts()`). When true, the resume handler clears `Attempts`, `FixAttempts`, and `AttemptLogs` to give a full fresh retry budget. All three must be cleared together for consistency — old AttemptLogs entries would conflict with restarted attempt numbering.
- **Resume view attempt history**: `viewResume()` shows `(N/M attempts used)` in `helpStyle` for non-success commands that have attempt logs.
- **Flag wiring** (`main.go`): `--reset-attempts` is a `flag.BoolVar`, passed to the TUI via `model.SetResetAttempts()` inside the `if resumeSession != nil` block. No-op if no session exists.

### Execution flow

Each command goes through: Pending -> Planning -> Running -> Verifying -> Documenting -> Committing -> Success. On failure at any of Planning/Running/Verifying: fail -> StatusFixing -> run Claude with `BuildFixPrompt()` -> re-verify only -> repeat up to `max_retries` times. The fix loop does not re-plan or re-execute — Claude fixes code directly and verification confirms the fix.

### Dependencies

- `github.com/charmbracelet/bubbletea` — TUI framework
- `github.com/BurntSushi/toml` — Config parsing
