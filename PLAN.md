# Implementation Plan: Add StatusFixing and Auto-Fix Types to types.go

Add `StatusFixing` constant, auto-fix tracking fields on `Command`, and a `BuildFixPrompt()` method. This is Step 1 of the auto-fix feature, scoped entirely to `internal/types/types.go`.

---

## File Modified

### `internal/types/types.go`

#### 1. Add `StatusFixing` constant (line 22)

Insert `StatusFixing` after `StatusRetrying` in the `const` block:

```
const (
    StatusPending    CommandStatus = iota // 0
    StatusPlanning                        // 1
    StatusRunning                         // 2
    StatusVerifying                       // 3
    StatusDocumenting                     // 4
    StatusCommitting                      // 5
    StatusSuccess                         // 6
    StatusFailed                          // 7
    StatusRetrying                        // 8
    StatusFixing                          // 9  ← NEW
)
```

**iota value**: `StatusFixing = 9`. No existing constants shift — this is appended at the end, so no breakage.

#### 2. Update `String()` method (line 26-41)

Append `"Fixing"` to the `labels` slice:

```go
labels := []string{
    "Pending",
    "Planning",
    "Running",
    "Verifying",
    "Documenting",
    "Committing",
    "Success",
    "Failed",
    "Retrying",
    "Fixing",  // ← NEW
}
```

The slice is indexed by the iota value, so `StatusFixing` (9) maps to index 9 → `"Fixing"`. No other entries shift.

#### 3. Update `ParseCommandStatus()` (line 126-149)

Add a case for `"Fixing"` in the switch statement:

```go
case "Fixing":
    return StatusFixing
```

Insert after the `"Retrying"` case (line 145). This is required for session persistence round-tripping — without it, a session saved during a fix attempt would deserialize to `StatusPending` on resume.

#### 4. Add auto-fix fields to `Command` struct (line 60-69)

Add five new fields after the existing `AttemptLogs` field:

```go
type Command struct {
    Prompt      string        // the claude code prompt
    Verify      string        // optional verification command
    MaxRetries  int           // default 3
    Status      CommandStatus // current execution status
    Output      string        // captured stdout/stderr
    PlanOutput  string        // output from the planning phase
    Attempts    int           // number of attempts made
    AttemptLogs []AttemptLog  // detailed log of each attempt
    // Auto-fix state
    LastFailedStep string     // which step failed: "planning", "execution", or "verification"
    LastExitCode   int        // exit code of the failed process
    LastStderr     string     // stderr from the failed step
    LastStdout     string     // stdout from the failed step
    FixAttempts    int        // number of auto-fix attempts made so far
}
```

**Field semantics:**
- `LastFailedStep`: Set by the runner when a step fails. Values: `"planning"`, `"execution"`, `"verification"`. Empty string means no failure recorded yet.
- `LastExitCode`: Exit code from the failed subprocess. `0` if no failure, `-1` if process couldn't start.
- `LastStderr` / `LastStdout`: Full captured output from the failed step. These are replaced (not accumulated) on each failure — they represent the *most recent* failure only.
- `FixAttempts`: Counter incremented before each fix attempt. Used with `MaxRetries` to determine remaining budget (`FixAttempts < MaxRetries`).

**Note:** `LastExitCode` defaults to Go's zero value (`0`), which is fine — a zero exit code means "no failure recorded" since we only populate these fields on actual failures.

#### 5. Add `BuildFixPrompt()` method

Add a new method on `*Command` after the existing `FormatFailureReport()` method (after line 122). Signature:

```go
func (c *Command) BuildFixPrompt() string
```

**Implementation details:**

Uses `fmt.Sprintf` or `strings.Builder` to construct a multi-section prompt string. The prompt structure:

```
Original task:
<c.Prompt>

Plan that was used:
<c.PlanOutput>    ← only included if PlanOutput is non-empty

Step that failed: <c.LastFailedStep>
Exit code: <c.LastExitCode>

Stdout:
<c.LastStdout>

Stderr:
<c.LastStderr>

The above command failed. Analyze the error output carefully and fix the issue. Do not start from scratch — identify what went wrong and make targeted fixes to resolve the error.
```

**Edge cases in BuildFixPrompt:**
- If `c.PlanOutput` is empty, omit the "Plan that was used" section entirely (don't show an empty section).
- If `c.LastStdout` is empty, still include the "Stdout:" header with empty content (the runner may have captured empty stdout, and its absence is informative).
- If `c.LastStderr` is empty, same treatment — include the header.
- No truncation of stdout/stderr in the prompt — the runner will handle any size concerns. The prompt should be complete so Claude has full context for debugging.

#### 6. Update `SessionCommand` struct (line 168-177)

Add corresponding JSON-serializable fields for session persistence:

```go
type SessionCommand struct {
    Prompt         string              `json:"prompt"`
    Verify         string              `json:"verify,omitempty"`
    MaxRetries     int                 `json:"max_retries"`
    Status         string              `json:"status"`
    Attempts       int                 `json:"attempts"`
    Output         string              `json:"output,omitempty"`
    PlanOutput     string              `json:"plan_output,omitempty"`
    AttemptLogs    []SessionAttemptLog `json:"attempt_logs,omitempty"`
    // Auto-fix state
    LastFailedStep string              `json:"last_failed_step,omitempty"`
    LastExitCode   int                 `json:"last_exit_code,omitempty"`
    LastStderr     string              `json:"last_stderr,omitempty"`
    LastStdout     string              `json:"last_stdout,omitempty"`
    FixAttempts    int                 `json:"fix_attempts,omitempty"`
}
```

All new fields use `omitempty` to keep the JSON clean when no fix has been attempted.

#### 7. Update `ToSessionCommand()` method (line 180-208)

Add the five new fields to the `SessionCommand` literal returned:

```go
return SessionCommand{
    Prompt:         c.Prompt,
    Verify:         c.Verify,
    MaxRetries:     c.MaxRetries,
    Status:         c.Status.String(),
    Attempts:       c.Attempts,
    Output:         c.Output,
    PlanOutput:     c.PlanOutput,
    AttemptLogs:    sessionLogs,
    LastFailedStep: c.LastFailedStep,
    LastExitCode:   c.LastExitCode,
    LastStderr:     c.LastStderr,
    LastStdout:     c.LastStdout,
    FixAttempts:    c.FixAttempts,
}
```

No transformation needed — all five fields are directly assignable (string, int).

#### 8. Update `FromSessionCommand()` method (line 211-241)

Add the five new fields to the `*Command` literal returned:

```go
return &Command{
    Prompt:         sc.Prompt,
    Verify:         sc.Verify,
    MaxRetries:     sc.MaxRetries,
    Status:         ParseCommandStatus(sc.Status),
    Attempts:       sc.Attempts,
    Output:         sc.Output,
    PlanOutput:     sc.PlanOutput,
    AttemptLogs:    logs,
    LastFailedStep: sc.LastFailedStep,
    LastExitCode:   sc.LastExitCode,
    LastStderr:     sc.LastStderr,
    LastStdout:     sc.LastStdout,
    FixAttempts:    sc.FixAttempts,
}
```

Same as `ToSessionCommand` — direct assignment, no conversion needed.

#### 9. Regarding "Remove existing plain retry logic references from types"

**Assessment:** The current `types.go` has no retry *logic* — it only has:
- `StatusRetrying` constant — **keep it** for now. Removing it would break `internal/runner/runner.go` and `internal/tui/model.go` which reference it. The task says "from types if present" — the retry *logic* lives in the runner, not in types. The `StatusRetrying` constant, `String()` label, and `ParseCommandStatus()` case are just type definitions, not logic. They will be removed in Step 6 (the cleanup step) after the runner and TUI have been migrated.
- `MaxRetries` and `Attempts` fields on `Command` — **keep them**. `MaxRetries` is reused as the fix attempt budget. `Attempts` tracks total attempts (used by session resume logic). These are not "retry logic" — they're counters.
- No retry-specific methods or functions exist in types.go.

**Conclusion:** No deletions needed in this step. `StatusRetrying` removal happens in Step 6 after all consumers are updated.

---

## Edge Cases

1. **iota stability**: `StatusFixing` is appended after `StatusRetrying` (not inserted between existing constants), so all existing iota values are unchanged. No downstream breakage.
2. **Session round-trip**: `ParseCommandStatus("Fixing")` → `StatusFixing` → `String()` → `"Fixing"`. The cycle is complete.
3. **Zero-value fields**: New `Command` fields default to Go zero values (`""` for strings, `0` for ints). This is correct behavior — zero values mean "no failure recorded" / "no fix attempts made".
4. **Empty PlanOutput in BuildFixPrompt**: The method conditionally includes the plan section only when `PlanOutput != ""`.
5. **Large stderr/stdout in BuildFixPrompt**: No truncation — full output is included. The runner or Claude's context window will be the natural limit.
6. **Backward compatibility**: Old session files (without the new JSON fields) will deserialize correctly — `omitempty` fields default to zero values when missing from JSON.

---

## Build Verification

After all changes, run:
```sh
go vet ./...
go build ./...
```

No new imports needed — `fmt` and `strings` are already imported.
