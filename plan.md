# Plan: Replace Retry Mechanism with Auto-Fix Loop

## Overview

Replace the existing retry-from-planning loop in `internal/runner/runner.go` with an auto-fix loop that uses `BuildFixPrompt()` and only re-runs verification after a fix, not the full planning+execution cycle.

---

## File 1: `internal/runner/runner.go`

### Change 1: Add `lastNLines` helper function

Add a new unexported helper function near the bottom of the file (before or after `runVerify`):

```go
func lastNLines(s string, n int) string
```

- Splits `s` by `\n`, takes the last `n` lines, joins them back with `\n`.
- If the string has fewer than `n` lines, returns the full string.
- Used to truncate stderr to 50 lines in fix attempt separators.

### Change 2: Rewrite `executeSingle` — new structure

The current `executeSingle` (lines 141–386) is completely replaced. The new function has two major sections: a **linear initial attempt** (plan → execute → verify) and an **auto-fix loop** that only runs if the initial attempt fails.

#### Section A: Closures and accumulators (keep and modify)

Keep the existing `attemptStdout`/`attemptStderr` `strings.Builder` accumulators.

Keep the `finalizeAttempt` closure as-is — it records an AttemptLog entry.

Keep the `updateLastAttempt` closure as-is — used for documenting/committing post-loop.

Add a new `recordFailure` closure:

```go
recordFailure := func(step string, result CommandResult) {
    cmd.LastFailedStep = step
    cmd.LastExitCode = result.ExitCode
    cmd.LastStdout = result.Stdout
    cmd.LastStderr = result.Stderr
}
```

This populates the fields that `cmd.BuildFixPrompt()` reads. Called before entering the fix loop or before each fix-loop retry.

#### Section B: Initial attempt — linear flow (no loop)

Remove the `for cmd.Attempts < cmd.MaxRetries` loop. Replace with a linear sequence:

**Set `cmd.Attempts = 1`.** (Not a loop counter anymore — just records that one initial attempt was made.)

Reset per-attempt accumulators. Capture git context. Create `attemptLog` with `AttemptNumber: 1`.

**Step 1 — Planning** (skip if `cmd.PlanOutput != ""`):
- Set `cmd.Status = StatusPlanning`, `r.sendUpdate(i, StatusPlanning, "", "")`. No `StatusDetail` for the initial run.
- Save session.
- `planOutput, planResult, planErr := r.runClaude(i, planPrompt)`
- Accumulate stdout/stderr.
- **On failure**: `recordFailure("planning", planResult)`, `finalizeAttempt(attemptLog, "Planning", planResult.ExitCode)`, then `goto fixLoop`.
- **On success**: `cmd.PlanOutput = planOutput`, save session.

**Step 2 — Execution**:
- Set `cmd.Status = StatusRunning`, append `═══ PLAN ═══\n` + plan + `\n═══ EXECUTION ═══\n` separators, send update.
- Save session.
- `execOutput, execResult, execErr := r.runClaude(i, execPrompt)`
- Accumulate stdout/stderr, `cmd.Output += execOutput`.
- **On failure**: `recordFailure("execution", execResult)`, `finalizeAttempt(attemptLog, "Running", execResult.ExitCode)`, then `goto fixLoop`.
- **On success**: continue.

**Step 3 — Verification** (skip if `cmd.Verify == ""`):
- Set `cmd.Status = StatusVerifying`, send update, save session.
- `verifyOutput, verifyResult, verifyErr := r.runVerify(i, cmd.Verify)`
- Accumulate stdout/stderr, `cmd.Output += "\n" + verifyOutput`.
- **On failure**: `recordFailure("verification", verifyResult)`, `finalizeAttempt(attemptLog, "Verifying", verifyResult.ExitCode)`, then `goto fixLoop`.
- **On success**: continue.

**All three pass**: `finalizeAttempt(attemptLog, "", 0)`. Jump to `goto postLoop` (documenting/committing).

#### Section C: Auto-fix loop (`fixLoop:` label)

```go
fixLoop:
    for {
        cmd.FixAttempts++

        if cmd.FixAttempts >= cmd.MaxRetries {
            cmd.Status = types.StatusFailed
            r.sendUpdate(i, types.StatusFailed, cmd.Output, "")
            r.saveSession()
            report := r.writeFailureReport(cmd)
            if r.program != nil {
                r.program.Send(ExecutionErrorMsg{
                    CmdIndex:      i,
                    Err:           fmt.Errorf("command failed after %d fix attempt(s)", cmd.FixAttempts),
                    FailureReport: report,
                })
            }
            return false
        }

        // Append fix separator
        cmd.Output += fmt.Sprintf("\n═══ FIX ATTEMPT %d/%d ═══\n", cmd.FixAttempts, cmd.MaxRetries)
        cmd.Output += fmt.Sprintf("Failed step: %s | Exit code: %d\n", cmd.LastFailedStep, cmd.LastExitCode)
        cmd.Output += fmt.Sprintf("Stderr: %s\n", lastNLines(cmd.LastStderr, 50))

        // StatusFixing
        fixDetail := fmt.Sprintf("Fix %d/%d", cmd.FixAttempts, cmd.MaxRetries)
        cmd.Status = types.StatusFixing
        r.sendUpdate(i, types.StatusFixing, cmd.Output, fixDetail)
        r.saveSession()

        // Sleep 2 seconds (interruptible)
        select {
        case <-time.After(2 * time.Second):
        case <-r.ctx.Done():
            return false
        }

        // Reset per-attempt accumulators
        attemptStdout.Reset()
        attemptStderr.Reset()

        // New AttemptLog for fix attempt
        gitBranch, gitStatus := r.captureGitContext()
        fixAttemptLog := types.AttemptLog{
            AttemptNumber: cmd.Attempts + cmd.FixAttempts,
            StartedAt:     time.Now(),
            WorkDir:       r.WorkDir,
            GitBranch:     gitBranch,
            GitStatus:     gitStatus,
            Command:       "claude -p <fix prompt>",
        }

        // Run claude with BuildFixPrompt()
        fixPrompt := cmd.BuildFixPrompt()
        fixOutput, fixResult, _ := r.runClaude(i, fixPrompt)
        attemptStdout.WriteString(fixResult.Stdout)
        attemptStderr.WriteString(fixResult.Stderr)
        cmd.Output += fmt.Sprintf("Fix output: %s\n", fixOutput)

        // After fix: re-verify or check exit code
        if cmd.Verify != "" {
            cmd.Status = types.StatusVerifying
            r.sendUpdate(i, types.StatusVerifying, cmd.Output, fixDetail)
            r.saveSession()

            verifyOutput, verifyResult, verifyErr := r.runVerify(i, cmd.Verify)
            attemptStdout.WriteString(verifyResult.Stdout)
            attemptStderr.WriteString(verifyResult.Stderr)
            cmd.Output += "\n" + verifyOutput

            if verifyErr != nil {
                // Verification still fails — record and try again
                recordFailure("verification", verifyResult)
                finalizeAttempt(fixAttemptLog, "auto-fix", verifyResult.ExitCode)
                continue
            }

            // Verification passed!
            finalizeAttempt(fixAttemptLog, "", 0)
            // fall through to postLoop
        } else {
            // No verify command — check fix claude's own exit code
            if fixResult.ExitCode != 0 {
                recordFailure(cmd.LastFailedStep, fixResult)
                finalizeAttempt(fixAttemptLog, "auto-fix", fixResult.ExitCode)
                continue
            }
            finalizeAttempt(fixAttemptLog, "", 0)
            // fall through to postLoop
        }
        break // fix succeeded
    }
```

**Key semantics:**
- `cmd.FixAttempts` incremented at **start** of each iteration.
- Budget: `FixAttempts >= MaxRetries` → fail. With `MaxRetries=3`, fix attempts 1 and 2 run; at 3 it's exhausted.
- The fix claude call uses `cmd.BuildFixPrompt()` which reads `LastFailedStep`, `LastExitCode`, `LastStdout`, `LastStderr`.
- After fix, **only verification is re-run** — no re-planning or re-execution.
- `PlanOutput` is **not cleared** — the fix was applied directly to the code.
- Each fix attempt gets its own `AttemptLog` with `FailedStep = "auto-fix"` on failure.
- `AttemptNumber` = `cmd.Attempts + cmd.FixAttempts` for unique sequential numbering.

#### Section D: Post-loop — documenting and committing (`postLoop:` label)

This section is **unchanged** from the current code. It runs after either:
- The initial attempt succeeded (all three steps passed), or
- The fix loop succeeded (verification passed after a fix).

Steps:
1. **Documentation** (non-fatal) — `StatusDocumenting`, run claude, warn on failure, `updateLastAttempt`.
2. **Commit** (non-fatal) — `StatusCommitting`, run claude, warn on failure, `updateLastAttempt`.
3. Set `StatusSuccess`, send update, save session, `return true`.

### Change 3: Remove all `StatusRetrying` references

Remove every occurrence of `types.StatusRetrying` from the file. These were at:
- Line 213: `cmd.Status = types.StatusRetrying` (planning failure retry)
- Line 214: `r.sendUpdate(i, types.StatusRetrying, ...)` (planning failure retry)
- Line 259: `cmd.Status = types.StatusRetrying` (execution failure retry)
- Line 260: `r.sendUpdate(i, types.StatusRetrying, ...)` (execution failure retry)
- Line 300: `cmd.Status = types.StatusRetrying` (verification failure retry)
- Line 301: `r.sendUpdate(i, types.StatusRetrying, ...)` (verification failure retry)

All are replaced by the fix loop's `StatusFixing` usage.

### Change 4: Remove old retry separators

Remove all `═══ RETRY N/M ═══` format strings. These are replaced by `═══ FIX ATTEMPT N/M ═══` in the fix loop.

### Change 5: Remove `cmd.PlanOutput = ""` on failure

Lines 255 and 297 currently clear `PlanOutput` to force re-planning. Remove both. The fix loop does not re-plan — it fixes code directly.

### Change 6: Remove the post-loop `!success` safety net (lines 331–344)

The old code had a `if !success` block after the retry loop to catch the case where the loop exited without success. This is no longer needed because:
- The fix loop either succeeds (breaks to postLoop) or explicitly returns false.
- The initial attempt either succeeds (goes to postLoop) or gotos fixLoop.
- There's no path that falls through without resolution.

---

## File 2: `internal/tui/model.go`

### Change 1: Status update handler — replace `StatusRetrying` with `StatusFixing` (line 368)

```go
// Before (line 367-368):
if (msg.Status == types.StatusPlanning || msg.Status == types.StatusRunning || msg.Status == types.StatusVerifying ||
    msg.Status == types.StatusDocumenting || msg.Status == types.StatusCommitting || msg.Status == types.StatusRetrying) &&

// After:
if (msg.Status == types.StatusPlanning || msg.Status == types.StatusRunning || msg.Status == types.StatusVerifying ||
    msg.Status == types.StatusDocumenting || msg.Status == types.StatusCommitting || msg.Status == types.StatusFixing) &&
```

### Change 2: `statusStyleFor` — replace `StatusRetrying` with `StatusFixing` (lines 1098-1099)

```go
// Before:
case types.StatusRetrying:
    return statusRetrying

// After:
case types.StatusFixing:
    return statusFixing
```

### Change 3: Rename style variable `statusRetrying` → `statusFixing` (lines 89-90)

```go
// Before:
statusRetrying = lipgloss.NewStyle().
    Foreground(lipgloss.Color("#FFD700"))

// After:
statusFixing = lipgloss.NewStyle().
    Foreground(lipgloss.Color("#FFD700"))
```

Same color (#FFD700 gold/yellow) — appropriate for a "fixing" state.

### Change 4: `statusIcon` — replace `StatusRetrying` with `StatusFixing` (lines 1295-1296)

```go
// Before:
case types.StatusRetrying:
    return "\u21bb" // ↻

// After:
case types.StatusFixing:
    return "\u2699" // ⚙ (gear)
```

Use `⚙` (U+2699, gear) — safe unicode that renders in most terminals and visually communicates "auto-fix".

### Change 5: Resume handler — also reset `FixAttempts` on `--reset-attempts` (lines 286-288)

```go
// Before:
if m.resetAttempts {
    cmds[m.resumeIndex].Attempts = 0
    cmds[m.resumeIndex].AttemptLogs = nil

// After:
if m.resetAttempts {
    cmds[m.resumeIndex].Attempts = 0
    cmds[m.resumeIndex].FixAttempts = 0
    cmds[m.resumeIndex].AttemptLogs = nil
```

### Change 6: Resume view — show fix attempts instead of raw attempts (line 902)

```go
// Before:
content += helpStyle.Render(fmt.Sprintf("  (%d/%d attempts used)", len(sc.AttemptLogs), sc.MaxRetries))

// After:
content += helpStyle.Render(fmt.Sprintf("  (%d/%d fix attempts)", sc.FixAttempts, sc.MaxRetries))
```

This requires `SessionCommand` to carry `FixAttempts` — see File 3 changes.

---

## File 3: `internal/types/types.go`

### Change 1: Add `FixAttempts` to `SessionCommand` (after line 208, the `Attempts` field)

```go
type SessionCommand struct {
    Prompt      string              `json:"prompt"`
    Verify      string              `json:"verify,omitempty"`
    MaxRetries  int                 `json:"max_retries"`
    Status      string              `json:"status"`
    Attempts    int                 `json:"attempts"`
    FixAttempts int                 `json:"fix_attempts,omitempty"`  // NEW
    Output      string              `json:"output,omitempty"`
    PlanOutput  string              `json:"plan_output,omitempty"`
    AttemptLogs []SessionAttemptLog `json:"attempt_logs,omitempty"`
}
```

### Change 2: Serialize `FixAttempts` in `ToSessionCommand` (line 232 area)

Add `FixAttempts: c.FixAttempts` to the `SessionCommand` literal returned by `ToSessionCommand()`.

### Change 3: Restore `FixAttempts` in `FromSessionCommand` (line 265 area)

Add `FixAttempts: sc.FixAttempts` to the `Command` literal returned by `FromSessionCommand()`.

### Change 4: Keep `StatusRetrying` in iota — DO NOT REMOVE

`StatusRetrying` (iota 8) must stay to preserve the iota value of `StatusFixing` (iota 9). Removing `StatusRetrying` would shift `StatusFixing` to iota 8, breaking any persisted sessions. The `String()` labels array, `ParseCommandStatus()`, and the iota list all keep `StatusRetrying` as dead code. A future cleanup step can remove it once backward compatibility is no longer needed.

---

## Edge Cases

### 1. No verify command set (`cmd.Verify == ""`)

- Initial attempt: planning + execution succeed → go straight to documenting/committing.
- Initial attempt execution fails → enter fix loop. After fix claude call:
  - Exit code 0 → assume fix succeeded, proceed to documenting/committing.
  - Exit code != 0 → record failure, loop for another fix attempt.

### 2. Planning failure triggers fix loop

The fix prompt (`BuildFixPrompt()`) has `LastFailedStep = "planning"`. After fix claude call:
- If `cmd.Verify != ""` → run verification. If passes, proceed.
- If `cmd.Verify == ""` → check fix exit code.

This is correct because: if planning failed but the fix claude directly fixed the code, verification should confirm it works.

### 3. Context cancellation during fix loop

The 2-second sleep uses `select { case <-time.After(2*time.Second): case <-r.ctx.Done(): return false }`. The `runClaude`/`runVerify` calls use `exec.CommandContext(r.ctx, ...)` — they also respect cancellation.

### 4. `FixAttempts` budget semantics

`FixAttempts` starts at 0. Incremented at the **start** of each fix loop iteration. Budget check: `FixAttempts >= MaxRetries`. With `MaxRetries=3`:
- Fix attempt 1: `FixAttempts` becomes 1 (< 3, allowed)
- Fix attempt 2: `FixAttempts` becomes 2 (< 3, allowed)
- Fix attempt 3: `FixAttempts` becomes 3 (>= 3, exhausted → fail)

So you get **2 fix attempts** with `MaxRetries=3`. The initial attempt + 2 fixes = 3 total tries.

### 5. Session resume mid-fix

- `FixAttempts` is persisted via `SessionCommand.FixAttempts`.
- On resume, command status is reset to `StatusPending` → `executeSingle` runs from the top (plan → execute → verify).
- If those fail again, the fix loop starts with the preserved `FixAttempts`, continuing the budget correctly.
- With `--reset-attempts`, `FixAttempts` is also cleared to 0.

### 6. `BuildFixPrompt()` with empty fields

Already handles empty `PlanOutput`, `LastStdout`, `LastStderr` by conditionally omitting sections. No changes needed.

### 7. Fix attempt AttemptLog numbering

Fix attempts use `AttemptNumber = cmd.Attempts + cmd.FixAttempts`. Since `cmd.Attempts = 1` (the initial attempt), fix attempt 1 gets `AttemptNumber = 2`, fix attempt 2 gets `AttemptNumber = 3`, etc. This produces unique, sequential numbers.

### 8. `goto` usage for flow control

Using `goto fixLoop` and `goto postLoop` labels avoids deeply nested if/else chains and makes the control flow explicit. Go allows `goto` as long as it doesn't jump over variable declarations — the accumulators and closures are all declared before the labels.

**Alternative**: Instead of `goto`, use a boolean `needsFix` flag checked after each step, then fall into the fix loop. But `goto` is cleaner here since there are three distinct failure points that all jump to the same fix loop.

---

## Summary of Changes

| File | Changes |
|------|---------|
| `internal/runner/runner.go` | Add `lastNLines` helper; rewrite `executeSingle` with linear initial attempt + fix loop using `goto` labels; remove all `StatusRetrying` references; remove `PlanOutput` clearing on failure; remove old retry separators |
| `internal/tui/model.go` | Replace `StatusRetrying` → `StatusFixing` in 4 places (status handler, style function, style variable, icon function); update resume logic to reset `FixAttempts`; update resume view display text |
| `internal/types/types.go` | Add `FixAttempts` to `SessionCommand`; update `ToSessionCommand`/`FromSessionCommand`; keep `StatusRetrying` in iota for backward compat |

No new files created. No files deleted.
