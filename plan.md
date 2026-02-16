# Plan: Fix Session Resume to Properly Handle Retry History

## Problem Summary

When resuming a session, `internal/tui/model.go:280` unconditionally sets `cmds[m.resumeIndex].Attempts = 0`, giving the command a full fresh retry budget. If a command used 2/3 attempts before interruption, it gets 3 more on resume instead of 1. The `AttemptLogs` are already serialized/deserialized correctly, but the `Attempts` counter is reset.

---

## File Changes

### 1. `internal/session/session.go` — Verify AttemptLogs serialization (NO CHANGES NEEDED)

**Finding**: AttemptLogs are already fully serialized and deserialized:
- `SessionCommand` (line 176) has `AttemptLogs []SessionAttemptLog` with `json:"attempt_logs,omitempty"` tag
- `Command.ToSessionCommand()` (line 180) converts all `AttemptLog` entries to `SessionAttemptLog`
- `FromSessionCommand()` (line 211) reconstructs all `AttemptLog` entries from `SessionAttemptLog`
- `SessionState` (line 22) stores `[]types.SessionCommand` which includes the attempt logs
- `Attempts int` is also serialized via `json:"attempts"` tag (line 173)

**No changes required in this file.** The serialization is already correct.

---

### 2. `internal/tui/model.go` — Fix resume to preserve attempt count + show history

#### 2a. Add `resetAttempts` field to `Model` struct (after line 135)

Add a new boolean field:

```go
resetAttempts    bool   // --reset-attempts: give resumed commands a fresh retry budget
```

#### 2b. Add `SetResetAttempts` method (after `SetAutoResume`, after line 208)

```go
func (m *Model) SetResetAttempts() {
    m.resetAttempts = true
}
```

#### 2c. Fix `resumeRunMsg` handler (lines 277-281)

**Current code:**
```go
// Reset the resume-from command to pending with fresh retry budget
if m.resumeIndex < len(cmds) {
    cmds[m.resumeIndex].Status = types.StatusPending
    cmds[m.resumeIndex].Attempts = 0
}
```

**Replace with:**
```go
if m.resumeIndex < len(cmds) {
    cmds[m.resumeIndex].Status = types.StatusPending
    if m.resetAttempts {
        cmds[m.resumeIndex].Attempts = 0
        cmds[m.resumeIndex].AttemptLogs = nil
    } else {
        cmds[m.resumeIndex].Attempts = len(cmds[m.resumeIndex].AttemptLogs)
    }
}
```

**Rationale:**
- Default path: `Attempts = len(AttemptLogs)`. The runner loop at `runner.go:178` does `for cmd.Attempts < cmd.MaxRetries { cmd.Attempts++; ... }`. So if AttemptLogs has 2 entries and MaxRetries is 3, Attempts starts at 2, loop increments to 3 and runs once — giving exactly 1 remaining attempt.
- `--reset-attempts` path: Clear both `Attempts` AND `AttemptLogs` for consistency. Without clearing AttemptLogs, old entries would remain but the counter restarts, leading to confusing numbering (e.g., "Attempt 1/3" in the runner but AttemptLogs already has 2 entries).

#### 2d. Update `viewResume()` to show attempt history (lines 886-887 area)

**Current code** (lines 884-887):
```go
prompt := truncate(sc.Prompt, 70)
idx := indexStyle.Render(fmt.Sprintf("%d.", i+1))

content := fmt.Sprintf("%s %s  %s %s", idx, prompt, icon, label)
```

**Replace the `content` line with:**
```go
content := fmt.Sprintf("%s %s  %s %s", idx, prompt, icon, label)

// Show attempt history for commands that used retries
if sc.Attempts > 0 && status != types.StatusSuccess {
    attemptInfo := helpStyle.Render(fmt.Sprintf("(%d/%d attempts used)", sc.Attempts, sc.MaxRetries))
    content += " " + attemptInfo
}
```

**Note:** `sc` is `types.SessionCommand` which has both `Attempts int` and `MaxRetries int` — both available directly. `status` is already computed on line 881: `status := types.ParseCommandStatus(sc.Status)`. This will display e.g. `"Command 3: Failed (2/3 attempts used)"` with the attempt info rendered in the gray `helpStyle`.

---

### 3. `main.go` — Add `--reset-attempts` flag

#### 3a. Add flag variable (inside the `var` block, line 98 area)

Add after `clearSession bool`:
```go
resetAttempts bool
```

#### 3b. Register long flag (after line 110 `--clear-session` registration)

```go
flag.BoolVar(&resetAttempts, "reset-attempts", false, "on resume, reset attempt counters for a full fresh retry budget")
```

No short alias needed — this is a rarely-used flag like `--clear-session` and `--no-docs`.

#### 3c. Update `usage()` function (line 52 area)

Add a new line after the `--clear-session` line:
```
      --reset-attempts  On resume, reset attempt counters for a full fresh retry budget
```

#### 3d. Pass flag to TUI model (inside `if resumeSession != nil` block, around line 308)

Add right before the closing `}` of the `if resumeSession != nil` block so it applies to both interactive and auto-resume paths:

```go
if resetAttempts {
    model.SetResetAttempts()
}
```

---

### 4. `README.md` — Document `--reset-attempts`

#### 4a. Flag reference table (line 162 area)

Add a new row after the `--clear-session` row:

```markdown
| | `--reset-attempts` | bool | `false` | On resume, reset attempt counters for a full fresh retry budget |
```

#### 4b. Session resume section — new subsection (before "### Session file location", line 280)

Insert a new subsection after "### Edge cases":

```markdown
### Resetting attempt counters

By default, resuming a session preserves the retry history. If a command used 2 of 3 attempts before interruption, it only gets 1 more attempt on resume.

Use `--reset-attempts` to give resumed commands a full fresh retry budget:

\```sh
autoclaude --reset-attempts
autoclaude -f commands.toml --auto-run --reset-attempts
\```

This is useful after manually fixing an issue that caused repeated failures — the command gets a clean slate without discarding the session's other progress (completed commands are preserved).

When `--reset-attempts` is used, the previous attempt logs are also cleared so that attempt numbering starts fresh.
```

---

## Edge Cases

1. **`Attempts` field already in session JSON**: `SessionCommand` already has `"attempts"` in its JSON tag (types.go:173). `FromSessionCommand` already restores it (types.go:236). The bug is purely in the TUI resume handler overwriting the restored value.

2. **AttemptLogs length vs Attempts mismatch**: In theory these should always match at session-save time since `finalizeAttempt` in `runner.go` appends to AttemptLogs and the loop increments Attempts in lockstep. Using `len(AttemptLogs)` is the more reliable source of truth since it's based on actual recorded data rather than a counter.

3. **Successful commands on resume**: The resume handler only modifies `cmds[m.resumeIndex]`. Commands before resumeIndex already have StatusSuccess and are untouched. Commands after resumeIndex are StatusPending with Attempts=0 — no adjustment needed.

4. **`--reset-attempts` without a session**: No-op. `resumeSession` is nil, the `if resumeSession != nil` block containing `if resetAttempts` never executes.

5. **`--reset-attempts` with `--no-resume`**: `--no-resume` clears the session before resume detection. `resumeSession` stays nil. The flag has no effect. Expected behavior, no special handling needed.

6. **Per-command MaxRetries**: Each command's `MaxRetries` is preserved in the session via `SessionCommand.MaxRetries`. The TUI display uses `sc.MaxRetries` (session command field), not the global `sess.MaxRetries`. This is correct.

7. **Clearing AttemptLogs on reset**: Setting `AttemptLogs = nil` ensures `FormatFailureReport()` and retry separator messages (which reference `cmd.Attempts`) are consistent. Without clearing, you'd have ghost attempt entries from before the reset.

8. **Runner loop compatibility**: The runner loop `for cmd.Attempts < cmd.MaxRetries { cmd.Attempts++ }` works correctly with any starting value of `Attempts`. If Attempts=2 and MaxRetries=3, it runs once (incrementing to 3). If Attempts=0 (reset), it runs 3 times. No runner changes needed.

---

## Files Modified

| File | Change Summary |
|------|----------------|
| `internal/tui/model.go` | Add `resetAttempts` field; add `SetResetAttempts()` method; fix `resumeRunMsg` handler to use `len(AttemptLogs)` instead of hardcoded 0; add attempt history display in `viewResume()` |
| `main.go` | Add `resetAttempts` flag variable, registration, usage text, and pass-through to TUI model |
| `README.md` | Add `--reset-attempts` row in flag reference table; add "Resetting attempt counters" subsection in Session resume section |

**No new files created. No changes to `internal/session/session.go` or `internal/types/types.go`.**
