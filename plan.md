# Plan: Audit and Fix Retry Mechanism

## Current State Analysis

### What's already correct
- **Loop arithmetic**: `for cmd.Attempts < cmd.MaxRetries { cmd.Attempts++; ... }` with MaxRetries=3 yields exactly 3 total attempts. No off-by-one.
- **Verification retries full cycle**: On verify failure, `cmd.PlanOutput = ""` forces fresh planning on next iteration. Correct.
- **Doc/commit are non-fatal**: Both are outside the retry loop and use `updateLastAttempt`. Correct.

### Bugs to fix
1. **No sleep between retries** — `continue` fires immediately after `StatusRetrying`
2. **No retry separator in `cmd.Output`** — user sees no visual boundary between attempts
3. **No "Attempt N/M" in TUI** — spinner just shows "Planning", "Running", etc. with no attempt counter
4. **Planning failure doesn't preserve output** — on planning failure in the retry branch (line 206), `cmd.Output` is not set before `sendUpdate`, so the TUI receives `planOutput` as the `Output` field of the message but `cmd.Output` itself is stale
5. **Planning failure doesn't clear `PlanOutput`** — technically harmless (it's already empty), but inconsistent with exec/verify which explicitly clear it
6. **Dead code at lines 282-287** — the post-loop `!success` block is unreachable in practice but should be kept as a safety net

---

## File Changes

### File 1: `internal/runner/runner.go`

#### Change 1.1: Add `StatusDetail` field to `StatusUpdateMsg`

**Lines 20-24.** Add a field for the attempt counter string.

```go
// Before:
type StatusUpdateMsg struct {
    CmdIndex int
    Status   types.CommandStatus
    Output   string
}

// After:
type StatusUpdateMsg struct {
    CmdIndex     int
    Status       types.CommandStatus
    Output       string
    StatusDetail string // e.g. "Attempt 2/3"
}
```

#### Change 1.2: Update `sendUpdate` signature

**Lines 331-339.** Add `detail string` parameter, populate `StatusDetail` in the message.

```go
// Before:
func (r *Runner) sendUpdate(index int, status types.CommandStatus, output string)

// After:
func (r *Runner) sendUpdate(index int, status types.CommandStatus, output string, detail string)
```

Body: set `StatusDetail: detail` in the `StatusUpdateMsg` literal.

#### Change 1.3: Update ALL existing `sendUpdate` call sites

Every call to `sendUpdate` must get the new 4th argument. Here is every call site in the function and what to pass:

**Inside the retry loop** (where `cmd.Attempts` is known):
- Line 194 (planning start): pass `detail` (defined as `fmt.Sprintf("Attempt %d/%d", cmd.Attempts, cmd.MaxRetries)`)
- Line 206 (planning fail, retry): pass `detail`
- Line 212 (planning fail, max reached): pass `""`
- Line 223 (execution start): pass `detail`
- Line 239 (execution fail, retry): pass `detail`
- Line 243 (execution fail, max reached): pass `""`
- Line 251 (verify start): pass `detail`
- Line 266 (verify fail, retry): pass `detail`
- Line 270 (verify fail, max reached): pass `""`

**Post-loop:**
- Line 284 (safety-net fail): pass `""`
- Line 292 (documenting start): pass `detail` (recomputed after loop as `fmt.Sprintf(...)`)
- Line 306 (documenting output update): pass `detail`
- Line 311 (committing start): pass `detail`
- Line 326 (final success): pass `""`

#### Change 1.4: Define `detail` variable at top of each iteration

Immediately after `cmd.Attempts++` (line 175), add:

```go
detail := fmt.Sprintf("Attempt %d/%d", cmd.Attempts, cmd.MaxRetries)
```

And after the retry loop (before doc/commit steps), redefine for post-loop use:

```go
detail := fmt.Sprintf("Attempt %d/%d", cmd.Attempts, cmd.MaxRetries)
```

#### Change 1.5: Add 2-second sleep with context cancellation on retry

At each of the three retry points (planning fail + retry, execution fail + retry, verification fail + retry), insert **after** `r.saveSession()` and **before** `continue`:

```go
select {
case <-time.After(2 * time.Second):
case <-r.ctx.Done():
    cmd.Status = types.StatusFailed
    r.sendUpdate(i, types.StatusFailed, cmd.Output, "")
    r.saveSession()
    return false
}
```

Using `select` instead of `time.Sleep` ensures the runner responds promptly to cancellation (e.g., user presses Ctrl+C). Without this, a cancelled runner would block for up to 2 seconds per retry point.

**Three locations to add this:**
1. Planning failure retry path (after line 208, before `continue`)
2. Execution failure retry path (after line 240, before `continue`)
3. Verification failure retry path (after line 268, before `continue`)

#### Change 1.6: Append retry separator to `cmd.Output` on retry

At each retry point, **before** the `StatusRetrying` sendUpdate, append a separator:

```go
cmd.Output += fmt.Sprintf("\n═══ RETRY %d/%d ═══ (previous attempt failed at: %s, exit code: %d)\n",
    cmd.Attempts+1, cmd.MaxRetries, "<StepName>", result.ExitCode)
```

Where `<StepName>` is "Planning", "Running", or "Verifying" depending on the failure point, and `result` is `planResult`, `execResult`, or `verifyResult` respectively. `cmd.Attempts+1` is the upcoming attempt number (since `cmd.Attempts` is the just-finished attempt).

**Three locations:**
1. Planning failure retry: `"Planning"`, `planResult.ExitCode`
2. Execution failure retry: `"Running"`, `execResult.ExitCode`
3. Verification failure retry: `"Verifying"`, `verifyResult.ExitCode`

#### Change 1.7: Fix `cmd.Output` on planning failure

**Current behavior (line 206):** On planning failure with retries remaining, `r.sendUpdate(i, types.StatusRetrying, planOutput)` sends `planOutput` as the output. But `cmd.Output` is never set, so it's stale (empty or from a prior attempt).

**Fix:** Before appending the retry separator, set:
```go
cmd.Output += planOutput
```

This ensures the separator is appended to the actual output, and `cmd.Output` reflects reality.

Also do the same on the max-retries-exceeded path (line 211 currently does `cmd.Output = planOutput` which is correct but should be `cmd.Output += planOutput` for consistency with multi-attempt accumulation).

**Wait, reconsider:** On the first attempt, `cmd.Output` is `""`. Using `+=` is fine. On subsequent attempts, `cmd.Output` already has the retry separator and previous output. Using `+=` correctly accumulates. But line 211 currently uses `=` (assignment), which would **overwrite** prior output. Change line 211 from `cmd.Output = planOutput` to `cmd.Output += planOutput` as well.

#### Change 1.8: Adjust execution step `cmd.Output` assignment

**Line 222:** Currently:
```go
cmd.Output = "═══ PLAN ═══\n" + cmd.PlanOutput + "\n═══ EXECUTION ═══\n"
```

This **overwrites** `cmd.Output`, discarding any prior retry separator and accumulated output. On the first attempt this is fine, but on retry attempts the retry separator was just appended.

**Fix:** Change to:
```go
cmd.Output += "═══ PLAN ═══\n" + cmd.PlanOutput + "\n═══ EXECUTION ═══\n"
```

This preserves the retry separator and any prior output.

#### Change 1.9: Ensure `PlanOutput` is cleared on planning failure retry

**Planning failure retry path:** Add `cmd.PlanOutput = ""` for consistency (it's already empty since we only enter planning when `cmd.PlanOutput == ""`, but be explicit).

---

### File 2: `internal/tui/model.go`

#### Change 2.1: Add `statusDetail` field to `Model` struct

**Around line 120** (in the struct definition), add:

```go
statusDetail string // transient "Attempt N/M" text from runner
```

#### Change 2.2: Store `StatusDetail` in `StatusUpdateMsg` handler

**Lines 344-371** (`case runner.StatusUpdateMsg:` in `Update()`).

After `m.commands[msg.CmdIndex].Output = msg.Output` (line 347), add:

```go
if msg.CmdIndex == m.currentCmd {
    m.statusDetail = msg.StatusDetail
}
```

Also, inside the block where `m.currentCmd` changes (line 352-356):

```go
if ... && msg.CmdIndex != m.currentCmd {
    m.currentCmd = msg.CmdIndex
    m.statusDetail = msg.StatusDetail  // carry detail to new command
    m.scrollOffset = 0
    m.outputLines = nil
}
```

And clear it on terminal statuses (success/failed) so it doesn't linger:

```go
if msg.Status == types.StatusSuccess || msg.Status == types.StatusFailed {
    m.statusDetail = ""
    // ... existing output reconciliation ...
}
```

#### Change 2.3: Display `statusDetail` next to spinner in `viewRunning()`

**Lines 974-977.** After the status text, conditionally append the detail:

```go
// Current:
b.WriteString(m.spinner.View())
b.WriteString(" ")
b.WriteString(styledStatus(cmd.Status))
b.WriteString("\n\n")

// New:
b.WriteString(m.spinner.View())
b.WriteString(" ")
b.WriteString(styledStatus(cmd.Status))
if m.statusDetail != "" {
    b.WriteString("  ")
    b.WriteString(lipgloss.NewStyle().Faint(true).Render(m.statusDetail))
}
b.WriteString("\n\n")
```

Result: `⠋ Planning  Attempt 2/3` where "Attempt 2/3" is dimmed.

---

## No changes needed

- **`internal/types/types.go`** — No changes. `Command`, `AttemptLog`, `CommandStatus`, session types all sufficient.
- **`internal/config/config.go`** — No changes. MaxRetries parsing is correct.
- **`main.go`** — No changes. Flag handling and config precedence are correct.

---

## Complete Trace: Retry Flow with MaxRetries=3

### Attempt 1 (first run)
1. `cmd.Attempts` = 0. Loop: `0 < 3` → true. `cmd.Attempts++` → 1.
2. `detail = "Attempt 1/3"`
3. Planning: `sendUpdate(StatusPlanning, "", "Attempt 1/3")`
4. Planning succeeds → `cmd.PlanOutput` set
5. Execution: `sendUpdate(StatusRunning, ..., "Attempt 1/3")`
6. Execution succeeds
7. Verification: `sendUpdate(StatusVerifying, ..., "Attempt 1/3")`
8. **Verification fails** (exit code 1)
9. `cmd.PlanOutput = ""`
10. `finalizeAttempt(attemptLog, "Verifying", 1)`
11. `cmd.Attempts` (1) `< cmd.MaxRetries` (3) → true → will retry
12. Append separator: `"\n═══ RETRY 2/3 ═══ (previous attempt failed at: Verifying, exit code: 1)\n"`
13. `sendUpdate(StatusRetrying, cmd.Output, "Attempt 1/3")`
14. `saveSession()`
15. Sleep 2 seconds (with ctx cancellation check)
16. `continue`

### Attempt 2 (retry)
1. `cmd.Attempts` = 1. Loop: `1 < 3` → true. `cmd.Attempts++` → 2.
2. `detail = "Attempt 2/3"`
3. `cmd.PlanOutput` is "" → Planning runs again: `sendUpdate(StatusPlanning, ..., "Attempt 2/3")`
4. Planning succeeds
5. `cmd.Output += "═══ PLAN ═══\n..."` (appends to existing output with separator)
6. Execution: `sendUpdate(StatusRunning, ..., "Attempt 2/3")`
7. **Execution fails** (exit code 2)
8. `cmd.PlanOutput = ""`
9. `finalizeAttempt(attemptLog, "Running", 2)`
10. `cmd.Attempts` (2) `< cmd.MaxRetries` (3) → true → will retry
11. Append separator: `"\n═══ RETRY 3/3 ═══ (previous attempt failed at: Running, exit code: 2)\n"`
12. `sendUpdate(StatusRetrying, cmd.Output, "Attempt 2/3")`
13. Sleep 2 seconds
14. `continue`

### Attempt 3 (final attempt)
1. `cmd.Attempts` = 2. Loop: `2 < 3` → true. `cmd.Attempts++` → 3.
2. `detail = "Attempt 3/3"`
3. Planning: `sendUpdate(StatusPlanning, ..., "Attempt 3/3")`
4. **Planning fails** (exit code 1)
5. `finalizeAttempt(attemptLog, "Planning", 1)`
6. `cmd.Attempts` (3) `< cmd.MaxRetries` (3) → **false** → max reached
7. `cmd.Status = StatusFailed`
8. `sendUpdate(StatusFailed, cmd.Output, "")`
9. `return false`

### MaxRetries=1 (no retries)
1. `cmd.Attempts` = 0. Loop: `0 < 1` → true. `cmd.Attempts++` → 1.
2. If any step fails: `cmd.Attempts` (1) `< cmd.MaxRetries` (1) → false → `StatusFailed`, `return false`
3. Retry separator is never appended. Sleep never happens. Correct.

### Success on first attempt
1. `cmd.Attempts` = 0 → 1.
2. Planning, execution, verification all pass.
3. `finalizeAttempt(attemptLog, "", 0)`, `success = true`, `break`.
4. Doc step: `sendUpdate(StatusDocumenting, ..., "Attempt 1/3")`
5. Commit step: `sendUpdate(StatusCommitting, ..., "Attempt 1/3")`
6. Final: `sendUpdate(StatusSuccess, ..., "")`

---

## Edge Cases

1. **MaxRetries=0**: Loop never enters. `success` stays false. Post-loop safety net sets `StatusFailed`. Config code prevents this (defaults 0→3), but safety net handles it.

2. **Context cancelled during 2s sleep**: The `select` on `r.ctx.Done()` fires immediately, sets `StatusFailed`, returns false. Runner stops cleanly within milliseconds.

3. **Resumed session with existing `cmd.Output`**: The `+=` operators for `cmd.Output` correctly accumulate. The retry separator will appear after whatever output existed from the session.

4. **Resumed session resets `Attempts=0`** (model.go line 274): Gives a fresh retry budget. The loop starts from scratch. Correct existing behavior, no change needed.

5. **Doc failure followed by commit success**: `updateLastAttempt("Documenting", docExitCode)` sets FailedStep. Then `updateLastAttempt("", 0)` for commit success doesn't overwrite FailedStep (due to the `if failedStep != ""` guard). The AttemptLog correctly records "Documenting" as the failed step even though the command overall succeeds. No change needed.

---

## Verification After Implementation

1. `go build -o autoclaude .` — must compile
2. `go test ./...` — must pass
3. Manual test with `max_retries = 2` and a failing verify command:
   - Spinner shows "Planning  Attempt 1/2", "Running  Attempt 1/2", "Verifying  Attempt 1/2"
   - Output shows `═══ RETRY 2/2 ═══ (previous attempt failed at: Verifying, exit code: 1)`
   - 2-second pause before retry
   - Fresh planning on attempt 2
   - If attempt 2 also fails: "Failed" with no "Attempt N/M"
4. Manual test with `max_retries = 1`:
   - No retry separator, no sleep, single attempt
5. Manual test with doc/commit failure:
   - Warning logged, no retry triggered, command still succeeds
