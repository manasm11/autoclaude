# Implementation Plan: Populate AttemptLog Entries in Execution Loop

## File to Modify

**`internal/runner/runner.go`** — the only file that needs changes.

No new files. No changes to `internal/types/types.go` (the `AttemptLog` struct already has all needed fields).

---

## Overview of Changes

The `executeSingle` method currently runs the retry loop without recording any `AttemptLog` entries. We need to:

1. Add a helper method to capture git context
2. Modify `runClaude` and `runVerify` to return `CommandResult` (they currently discard it)
3. Restructure the retry loop in `executeSingle` to build an `AttemptLog` per attempt and append it to `cmd.AttemptLogs` on every exit path

---

## Detailed Changes

### Change 1: Add `captureGitContext` helper method

**Location:** After the `runVerify` method (after line 382), add a new method on `*Runner`.

**Function signature:**
```go
func (r *Runner) captureGitContext() (branch string, status string)
```

**Behavior:**
- Run `exec.Command("git", "branch", "--show-current")` with `Dir` set to `r.WorkDir`. Capture stdout via `cmd.Output()`. On any error, return `""` for branch.
- Run `exec.Command("git", "status", "--porcelain")` with `Dir` set to `r.WorkDir`. Capture stdout via `cmd.Output()`. On any error, return `""` for status.
- Trim whitespace (`strings.TrimSpace`) from both outputs before returning.
- These are quick local commands, no need for context cancellation or streaming.

### Change 2: Update `runClaude` to return `CommandResult`

**Location:** Lines 374-377, the `runClaude` method.

**Current signature:**
```go
func (r *Runner) runClaude(cmdIndex int, prompt string) (string, error)
```

**New signature:**
```go
func (r *Runner) runClaude(cmdIndex int, prompt string) (string, CommandResult, error)
```

**Change:** Instead of discarding the `CommandResult` with `_`, return it:
```go
func (r *Runner) runClaude(cmdIndex int, prompt string) (string, CommandResult, error) {
    return r.runCommandStreaming(cmdIndex, "claude", "--dangerously-skip-permissions", "-p", prompt)
}
```

### Change 3: Update `runVerify` to return `CommandResult`

**Location:** Lines 379-382, the `runVerify` method.

**Current signature:**
```go
func (r *Runner) runVerify(cmdIndex int, verifyCmd string) (string, error)
```

**New signature:**
```go
func (r *Runner) runVerify(cmdIndex int, verifyCmd string) (string, CommandResult, error)
```

**Change:** Same pattern — return the `CommandResult`:
```go
func (r *Runner) runVerify(cmdIndex int, verifyCmd string) (string, CommandResult, error) {
    return r.runCommandStreaming(cmdIndex, "sh", "-c", verifyCmd)
}
```

### Change 4: Restructure `executeSingle` to populate AttemptLog

This is the main change. The method at lines 137-258 needs restructuring within the retry loop.

#### 4a: Add helper closures at the top of `executeSingle`

Right after the `success := false` declaration (line 138), add two helper closures:

```go
finalizeAttempt := func(log *types.AttemptLog, failedStep string, exitCode int, stdoutBuf, stderrBuf *strings.Builder) {
    log.FailedStep = failedStep
    log.ExitCode = exitCode
    log.EndedAt = time.Now()
    log.Duration = log.EndedAt.Sub(log.StartedAt)
    log.Stdout = stdoutBuf.String()
    log.Stderr = stderrBuf.String()
    cmd.AttemptLogs = append(cmd.AttemptLogs, *log)
}

updateLastAttempt := func(result CommandResult, failedStep string) {
    if len(cmd.AttemptLogs) == 0 {
        return
    }
    last := &cmd.AttemptLogs[len(cmd.AttemptLogs)-1]
    last.Stdout += result.Stdout
    last.Stderr += result.Stderr
    if failedStep != "" {
        last.FailedStep = failedStep
        last.ExitCode = result.ExitCode
    }
    last.EndedAt = time.Now()
    last.Duration = last.EndedAt.Sub(last.StartedAt)
}
```

#### 4b: At the top of each retry iteration (after `cmd.Attempts++`, line 140)

Add the following steps immediately after `cmd.Attempts++`:

1. Record `attemptStart := time.Now()`
2. Call `gitBranch, gitStatus := r.captureGitContext()`
3. Initialize an `AttemptLog` struct:
   ```go
   attemptLog := types.AttemptLog{
       AttemptNumber: cmd.Attempts,
       StartedAt:     attemptStart,
       WorkDir:       r.WorkDir,
       GitBranch:     gitBranch,
       GitStatus:     gitStatus,
   }
   ```
4. Initialize accumulator variables for stdout/stderr across steps within this attempt:
   ```go
   var attemptStdout, attemptStderr strings.Builder
   ```

#### 4c: Planning step (lines 143-165)

**Current code calls:**
```go
planOutput, planErr := r.runClaude(i, planPrompt)
```

**Change to:**
```go
planOutput, planResult, planErr := r.runClaude(i, planPrompt)
```

After the call, accumulate stdout/stderr:
```go
attemptStdout.WriteString(planResult.Stdout)
attemptStderr.WriteString(planResult.Stderr)
```

Record the command string in `attemptLog`:
```go
attemptLog.Command = fmt.Sprintf("claude --dangerously-skip-permissions -p [planning prompt, %d chars]", len(planPrompt))
```

On **planning failure** — both the `continue` path (line 155, when retries remain) and the `return false` path (line 161, when retries exhausted) — insert before `continue` or `return false`:
```go
finalizeAttempt(&attemptLog, "Planning", planResult.ExitCode, &attemptStdout, &attemptStderr)
```

#### 4d: Execution step (lines 167-189)

**Current code calls:**
```go
execOutput, execErr := r.runClaude(i, execPrompt)
```

**Change to:**
```go
execOutput, execResult, execErr := r.runClaude(i, execPrompt)
```

After the call, accumulate:
```go
attemptStdout.WriteString(execResult.Stdout)
attemptStderr.WriteString(execResult.Stderr)
```

Update the command string (overwrite the planning command — the log now reflects the last step attempted):
```go
attemptLog.Command = fmt.Sprintf("claude --dangerously-skip-permissions -p [execution prompt, %d chars]", len(execPrompt))
```

On **execution failure** (both `continue` and `return false` paths), insert before `continue` or `return false`:
```go
finalizeAttempt(&attemptLog, "Running", execResult.ExitCode, &attemptStdout, &attemptStderr)
```

#### 4e: Verification step (lines 192-213)

**Current code calls:**
```go
verifyOutput, verifyErr := r.runVerify(i, cmd.Verify)
```

**Change to:**
```go
verifyOutput, verifyResult, verifyErr := r.runVerify(i, cmd.Verify)
```

After the call, accumulate:
```go
attemptStdout.WriteString(verifyResult.Stdout)
attemptStderr.WriteString(verifyResult.Stderr)
```

Update the command string:
```go
attemptLog.Command = cmd.Verify
```

On **verification failure** (both paths), insert before `continue` or `return false`:
```go
finalizeAttempt(&attemptLog, "Verifying", verifyResult.ExitCode, &attemptStdout, &attemptStderr)
```

#### 4f: On successful completion of the retry loop (before `break` at line 217)

When plan + execution + verify all pass, we still need to log the attempt. Before `break`, insert:
```go
finalizeAttempt(&attemptLog, "", 0, &attemptStdout, &attemptStderr)
```

An empty `FailedStep` with exit code 0 indicates success.

#### 4g: Documentation step (lines 228-241) — post-retry-loop, success path only

**Current code calls:**
```go
docOutput, docErr := r.runClaude(i, docPrompt)
```

**Change to:**
```go
docOutput, docResult, docErr := r.runClaude(i, docPrompt)
```

After the call (regardless of success/failure), update the last AttemptLog:
- If `docErr != nil`: `updateLastAttempt(docResult, "Documenting")`
- If `docErr == nil`: `updateLastAttempt(docResult, "")` (just accumulate output, don't mark failure since doc is non-fatal — but we still want stdout/stderr captured)

**Note:** Since documentation is non-fatal, even if we set FailedStep to "Documenting", the overall command still proceeds to commit. The FailedStep field here records what went wrong, not that the whole command failed.

#### 4h: Commit step (lines 244-257) — post-retry-loop, success path only

**Current code calls:**
```go
commitOutput, commitErr := r.runClaude(i, "Git add all changes...")
```

**Change to:**
```go
commitOutput, commitResult, commitErr := r.runClaude(i, "Git add all changes...")
```

After the call, update the last AttemptLog:
- If `commitErr != nil`: `updateLastAttempt(commitResult, "Committing")`
- If `commitErr == nil`: `updateLastAttempt(commitResult, "")`

---

## Summary of All Call Sites Changed

| Step | Current Call | New Call | On Failure | AttemptLog.FailedStep |
|------|-------------|----------|------------|----------------------|
| Planning | `runClaude(i, planPrompt)` → `(string, error)` | `runClaude(i, planPrompt)` → `(string, CommandResult, error)` | `finalizeAttempt(...)` + continue/return | `"Planning"` |
| Execution | `runClaude(i, execPrompt)` → `(string, error)` | `runClaude(i, execPrompt)` → `(string, CommandResult, error)` | `finalizeAttempt(...)` + continue/return | `"Running"` |
| Verification | `runVerify(i, cmd.Verify)` → `(string, error)` | `runVerify(i, cmd.Verify)` → `(string, CommandResult, error)` | `finalizeAttempt(...)` + continue/return | `"Verifying"` |
| Documentation | `runClaude(i, docPrompt)` → `(string, error)` | `runClaude(i, docPrompt)` → `(string, CommandResult, error)` | `updateLastAttempt(...)` | `"Documenting"` |
| Commit | `runClaude(i, commitPrompt)` → `(string, error)` | `runClaude(i, commitPrompt)` → `(string, CommandResult, error)` | `updateLastAttempt(...)` | `"Committing"` |

---

## Import Changes

No new imports needed. `runner.go` already imports all required packages:
- `"os/exec"` — for `captureGitContext`
- `"strings"` — for `strings.TrimSpace` and `strings.Builder`
- `"time"` — for `time.Now()`
- `"fmt"` — for `fmt.Sprintf`
- `"github.com/manasm11/autoclaude/internal/types"` — for `types.AttemptLog`

---

## Edge Cases

1. **Git not available:** `captureGitContext` must handle `exec.Command` returning an error (e.g., git not installed, not a git repo). Return empty strings — already covered by ignoring errors from `cmd.Output()`.

2. **First attempt with resumed session that already has a plan:** The planning step is skipped (`cmd.PlanOutput != ""`), so no planning `CommandResult` is generated. The `attemptLog.Command` will only reflect the execution step. `attemptStdout`/`attemptStderr` will only contain execution output. This is correct behavior — the skipped step contributes nothing to capture.

3. **Documentation/commit failures are non-fatal:** These happen outside the retry loop. `updateLastAttempt` updates the already-appended last AttemptLog entry in-place. If the doc step fails but commit succeeds, the FailedStep gets overwritten to `""` by the successful commit `updateLastAttempt` call. To avoid this, only call `updateLastAttempt` with a non-empty failedStep when there's an error; pass `""` otherwise so the FailedStep isn't cleared if previously set.

   **Refinement for `updateLastAttempt`:** Change the conditional to: only overwrite `FailedStep` and `ExitCode` if `failedStep != ""`. This way a successful commit won't clear a "Documenting" failure.

4. **Context cancellation:** If `r.ctx` is cancelled mid-execution, `runCommandStreaming` returns an error with ExitCode=-1. The standard flow handles it since we finalize on any error.

5. **Empty stdout/stderr:** `CommandResult.Stdout` and `.Stderr` may be empty strings. This is fine — `AttemptLog` fields accept empty strings and `FormatFailureReport` already guards against them.

6. **The `!success` fallthrough after the retry loop (line 220-225):** This path is reached when `cmd.Attempts >= cmd.MaxRetries` without a `break`. The last iteration's AttemptLog was already appended inside the loop body (at each `continue` or `return false` site), so no additional append is needed here.

7. **AttemptLog.Command field — full prompt vs abbreviated:** The full `planPrompt`/`execPrompt` strings can be very long. Store an abbreviated form: `fmt.Sprintf("claude --dangerously-skip-permissions -p [planning prompt, %d chars]", len(planPrompt))`. This keeps logs readable. For verification, store the actual verify command string since those are typically short.

8. **`saveSession()` calls after AttemptLog appends:** The existing `saveSession()` calls remain in their current positions. Since `cmd.AttemptLogs` is appended before `saveSession()` is called (the `finalizeAttempt` call is inserted before the `continue`/`return false` which are before the `saveSession()` calls... wait, checking the code):
   - On the retry+continue path: the code does `r.sendUpdate(...)` then `r.saveSession()` then `continue`. The `finalizeAttempt` call should go **before** `r.sendUpdate` so the session save captures the new AttemptLog.
   - On the return false path: same pattern — `finalizeAttempt` before `r.sendUpdate` and `r.saveSession`.
   - This ensures AttemptLogs are persisted to the session file.

---

## Testing Considerations

- Run `go build -o autoclaude .` to verify compilation.
- Run `go test ./...` to verify existing tests pass with the new `runClaude`/`runVerify` signatures.
- If any existing tests call `runClaude` or `runVerify` directly, they'll need updating for the 3-return-value signatures.
