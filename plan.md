# Plan: Update session handling for auto-fix state persistence

## Summary

Persist the auto-fix fields (`LastFailedStep`, `LastExitCode`, `LastStderr`, `LastStdout`, `FixAttempts`) through session serialization/deserialization so that a resumed session correctly restores fix state, and remove the obsolete `StatusRetrying` from session deserialization.

## Analysis of Current State

### What already works
- `SessionCommand` in `internal/types/types.go:202-212` already has `FixAttempts int` with JSON tag `fix_attempts,omitempty`
- `ToSessionCommand()` (line 215) already copies `FixAttempts` to `SessionCommand`
- `FromSessionCommand()` (line 247) already restores `FixAttempts` from `SessionCommand`
- The TUI resume handler (`internal/tui/model.go:278-307`) already resets `FixAttempts = 0` when `--reset-attempts` is true

### What's missing
- `SessionCommand` does **not** persist `LastFailedStep`, `LastExitCode`, `LastStderr`, or `LastStdout`
- `ToSessionCommand()` does not copy these four fields
- `FromSessionCommand()` does not restore these four fields
- `StatusRetrying` is still handled in `ParseCommandStatus()` (line 177) — should be removed so old "Retrying" sessions map to `StatusPending`
- The `--reset-attempts` path clears `Attempts`, `AttemptLogs`, and `FixAttempts` but does not clear the `Last*` fields

### Resume from `StatusFixing` — how it works
When a command is persisted with `Status: "Fixing"`, on resume the TUI handler resets status to `StatusPending` (line 285). The runner's `executeSingle` starts a fresh plan→execute→verify cycle. The preserved `FixAttempts` value reduces the fix budget: on the next failure, the fix loop checks `cmd.FixAttempts >= cmd.MaxRetries` (runner.go:291), so the remaining budget is `MaxRetries - FixAttempts`. The `Last*` fields are restored so `BuildFixPrompt()` and the TUI enhanced fixing view have accurate context if the command enters the fix loop again.

---

## Changes

### File 1: `internal/types/types.go`

#### Change 1a: Add 4 fields to `SessionCommand` struct (line 202-212)

Add `LastFailedStep`, `LastExitCode`, `LastStderr`, `LastStdout` after the existing `FixAttempts` field:

```go
type SessionCommand struct {
    Prompt         string              `json:"prompt"`
    Verify         string              `json:"verify,omitempty"`
    MaxRetries     int                 `json:"max_retries"`
    Status         string              `json:"status"`
    Attempts       int                 `json:"attempts"`
    FixAttempts    int                 `json:"fix_attempts,omitempty"`
    LastFailedStep string              `json:"last_failed_step,omitempty"`   // NEW
    LastExitCode   int                 `json:"last_exit_code,omitempty"`     // NEW
    LastStderr     string              `json:"last_stderr,omitempty"`        // NEW
    LastStdout     string              `json:"last_stdout,omitempty"`        // NEW
    Output         string              `json:"output,omitempty"`
    PlanOutput     string              `json:"plan_output,omitempty"`
    AttemptLogs    []SessionAttemptLog `json:"attempt_logs,omitempty"`
}
```

#### Change 1b: Update `ToSessionCommand()` (line 233-243)

Add the four new fields to the `SessionCommand` literal:

```go
return SessionCommand{
    ...existing fields...
    FixAttempts:    c.FixAttempts,
    LastFailedStep: c.LastFailedStep,   // NEW
    LastExitCode:   c.LastExitCode,     // NEW
    LastStderr:     c.LastStderr,       // NEW
    LastStdout:     c.LastStdout,       // NEW
    ...existing fields...
}
```

#### Change 1c: Update `FromSessionCommand()` (line 267-278)

Add the four new fields to the returned `Command` literal:

```go
return &Command{
    ...existing fields...
    FixAttempts:    sc.FixAttempts,
    LastFailedStep: sc.LastFailedStep,   // NEW
    LastExitCode:   sc.LastExitCode,     // NEW
    LastStderr:     sc.LastStderr,       // NEW
    LastStdout:     sc.LastStdout,       // NEW
    ...existing fields...
}
```

#### Change 1d: Remove `StatusRetrying` from `ParseCommandStatus()` (line 176-177)

Remove this case:
```go
case "Retrying":
    return StatusRetrying
```

**Rationale**: `StatusRetrying` is unused in the runner and TUI. Removing it from `ParseCommandStatus` means old sessions with `"Retrying"` status will deserialize to `StatusPending` (the default), which is correct — they should restart from the beginning.

**Do NOT remove** `StatusRetrying` from the `const` iota block (line 21) or from the `String()` labels slice (line 37). These must stay to preserve iota value ordering (`StatusFixing = 9`). Per CLAUDE.md: "StatusRetrying is still present in types for backward compatibility (preserves iota values for persisted sessions)."

---

### File 2: `internal/tui/model.go`

#### Change 2a: Clear `Last*` fields when `--reset-attempts` is used (line 286-289)

Inside the `resumeRunMsg` handler, within the `if m.resetAttempts` block, also clear the auto-fix failure context:

```go
if m.resetAttempts {
    cmds[m.resumeIndex].Attempts = 0
    cmds[m.resumeIndex].AttemptLogs = nil
    cmds[m.resumeIndex].FixAttempts = 0
    cmds[m.resumeIndex].LastFailedStep = ""   // NEW
    cmds[m.resumeIndex].LastExitCode = 0      // NEW
    cmds[m.resumeIndex].LastStderr = ""        // NEW
    cmds[m.resumeIndex].LastStdout = ""        // NEW
}
```

This ensures a complete clean slate — no stale failure context that could confuse `BuildFixPrompt()` or the TUI's enhanced fixing view.

---

### File 3: `internal/session/session.go`

**No changes needed.** The session package is a thin persistence layer that serializes/deserializes `SessionState` containing `[]types.SessionCommand`. All field additions are in the `types` package. The session functions (`Save`, `Load`, `ToCommands`, `AllSucceeded`) work generically over the structs and don't reference individual `SessionCommand` fields.

---

## Edge Cases

### 1. Backward compatibility with existing session files
Old session files won't have `last_failed_step`, `last_exit_code`, `last_stderr`, `last_stdout` JSON keys. Go's `json.Unmarshal` uses zero-value defaults for missing fields: empty string for `string`, `0` for `int`. These are the correct "no failure recorded" semantics, matching the zero-value convention documented in CLAUDE.md.

### 2. `LastExitCode = 0` with `omitempty`
JSON `omitempty` omits `0` for ints. This is fine because exit code 0 means "no failure" (the zero-value semantic). When deserialized from a session missing this field, it defaults to 0 = no failure.

### 3. `StatusRetrying` in old session files
Removing the case from `ParseCommandStatus` means old sessions with `"Retrying"` status deserialize to `StatusPending`. The TUI resume handler then treats this as a command that needs re-execution from scratch, which is correct.

### 4. Large `LastStdout`/`LastStderr` in session files
These could be large strings. The runner already stores them without truncation on `Command`. Session persistence will include the full strings. This matches the existing behavior of `AttemptLog.Stdout`/`Stderr` in `SessionAttemptLog`. No truncation is added to keep the change minimal.

### 5. Resume with preserved `FixAttempts`
When `--reset-attempts` is NOT used: `FixAttempts` is restored from the session. The runner's `executeSingle` starts fresh (plan→execute→verify), but if it fails, the fix loop begins with the preserved `FixAttempts` value. Budget = `MaxRetries - FixAttempts`. Example: `MaxRetries=3`, `FixAttempts=2` from previous run → only 1 more fix attempt allowed (`2 + 1 >= 3`).

### 6. Resume with `--reset-attempts`
All counters and failure context are cleared: `Attempts=0`, `FixAttempts=0`, `AttemptLogs=nil`, `LastFailedStep=""`, `LastExitCode=0`, `LastStderr=""`, `LastStdout=""`. The command gets a full fresh budget.

---

## Files Changed Summary

| File | Change |
|------|--------|
| `internal/types/types.go` | Add 4 fields to `SessionCommand`; update `ToSessionCommand()` and `FromSessionCommand()` to copy them; remove `"Retrying"` case from `ParseCommandStatus()` |
| `internal/tui/model.go` | Clear `Last*` fields in `--reset-attempts` block of `resumeRunMsg` handler |
| `internal/session/session.go` | No changes needed |

## Verification

Run `go build -o autoclaude . && go test ./...` to confirm compilation and any existing tests pass.
