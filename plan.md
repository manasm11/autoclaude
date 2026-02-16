# Implementation Plan: AttemptLog Struct & FormatFailureReport

## Overview

Add per-attempt structured logging to commands so that each retry captures detailed context (stdout, stderr, exit code, git state, timing). Expose a `FormatFailureReport()` method for readable debug output. Persist attempt logs through session resume.

**Only one file is modified: `internal/types/types.go`.** The session layer (`internal/session/session.go`) requires no changes because it already serializes/deserializes `[]types.SessionCommand` via JSON — the new fields flow through automatically.

---

## File: `internal/types/types.go`

### Change 1: Add imports

Add a new import block at the top (the file currently has no imports):

```go
import (
    "fmt"
    "strings"
    "time"
)
```

- `time` — needed by `AttemptLog` fields (`time.Time`, `time.Duration`)
- `fmt` — needed by `FormatFailureReport()` for `Sprintf`
- `strings` — needed by `FormatFailureReport()` for `Builder` and `Repeat`

---

### Change 2: Add `AttemptLog` struct

Insert after the `String()` method on `CommandStatus` (after line 35), before the `Command` struct:

```go
// AttemptLog captures details of a single execution attempt for debugging.
type AttemptLog struct {
    AttemptNumber int
    StartedAt     time.Time
    EndedAt       time.Time
    Duration      time.Duration
    FailedStep    string // "planning", "execution", "verification", or "" for success
    Command       string // the prompt or shell command that was run
    ExitCode      int
    Stdout        string
    Stderr        string
    WorkDir       string
    GitBranch     string
    GitStatus     string
}
```

12 fields as specified. `FailedStep` uses empty string to indicate success.

---

### Change 3: Add `AttemptLogs` field to `Command` struct

Add after the existing `Attempts int` field (line 46):

```go
AttemptLogs []AttemptLog // structured log of each attempt
```

Full struct becomes:

```go
type Command struct {
    Prompt      string
    Verify      string
    MaxRetries  int
    Status      CommandStatus
    Output      string
    PlanOutput  string
    Attempts    int
    AttemptLogs []AttemptLog
}
```

No change to `NewCommand()` — `AttemptLogs` will be `nil` by default, which is correct.

---

### Change 4: Add `FormatFailureReport()` method on `*Command`

Insert after `NewCommand()` (after line 54). Signature:

```go
func (c *Command) FormatFailureReport() string
```

**Logic:**

1. If `len(c.AttemptLogs) == 0`, return `"No attempt logs recorded.\n"`.
2. Create a `strings.Builder`.
3. Write header: `fmt.Sprintf("Failure Report for: %s\n", truncatedPrompt)` where `truncatedPrompt` is `c.Prompt` truncated to 80 characters with `"..."` appended if longer.
4. Write separator: `strings.Repeat("=", 60) + "\n"`.
5. Write summary: `fmt.Sprintf("Total attempts: %d\n", len(c.AttemptLogs))`.
6. For each `log` in `c.AttemptLogs`:
   - `fmt.Sprintf("\n--- Attempt %d ---\n", log.AttemptNumber)`
   - `fmt.Sprintf("  Started:   %s\n", log.StartedAt.Format("2006-01-02 15:04:05"))`
   - `fmt.Sprintf("  Ended:     %s\n", log.EndedAt.Format("2006-01-02 15:04:05"))`
   - `fmt.Sprintf("  Duration:  %s\n", log.Duration)`
   - `fmt.Sprintf("  Exit Code: %d\n", log.ExitCode)`
   - If `log.FailedStep != ""`: `fmt.Sprintf("  Failed Step: %s\n", log.FailedStep)`
   - If `log.WorkDir != ""`: `fmt.Sprintf("  Work Dir:  %s\n", log.WorkDir)`
   - If `log.GitBranch != ""`: `fmt.Sprintf("  Git Branch: %s\n", log.GitBranch)`
   - If `log.GitStatus != ""`: `fmt.Sprintf("  Git Status: %s\n", log.GitStatus)`
   - If `log.Command != ""`: `fmt.Sprintf("  Command:   %s\n", log.Command)`
   - `"\n  --- Stdout ---\n"` then either `log.Stdout` or `"  (empty)"`, followed by `"\n"`
   - `"\n  --- Stderr ---\n"` then either `log.Stderr` or `"  (empty)"`, followed by `"\n"`
7. Write final separator: `strings.Repeat("=", 60) + "\n"`.
8. Return `builder.String()`.

---

### Change 5: Add `SessionAttemptLog` struct for JSON serialization

Insert just before the existing `SessionCommand` struct (before line 84):

```go
// SessionAttemptLog is a JSON-friendly representation of AttemptLog for session persistence.
type SessionAttemptLog struct {
    AttemptNumber int    `json:"attempt_number"`
    StartedAt     string `json:"started_at"`            // RFC3339 format
    EndedAt       string `json:"ended_at"`              // RFC3339 format
    DurationMs    int64  `json:"duration_ms"`           // milliseconds
    FailedStep    string `json:"failed_step,omitempty"`
    Command       string `json:"command,omitempty"`
    ExitCode      int    `json:"exit_code"`
    Stdout        string `json:"stdout,omitempty"`
    Stderr        string `json:"stderr,omitempty"`
    WorkDir       string `json:"work_dir,omitempty"`
    GitBranch     string `json:"git_branch,omitempty"`
    GitStatus     string `json:"git_status,omitempty"`
}
```

**Rationale:** `time.Time` and `time.Duration` need explicit formatting for clean JSON. RFC3339 for timestamps, milliseconds (int64) for duration.

---

### Change 6: Add `AttemptLogs` field to `SessionCommand` struct

Add after the existing `PlanOutput` field:

```go
AttemptLogs []SessionAttemptLog `json:"attempt_logs,omitempty"`
```

The `omitempty` ensures backward compatibility — old session files without this field will deserialize cleanly (nil slice), and new session files without attempt logs won't bloat the JSON.

---

### Change 7: Update `ToSessionCommand()` to convert AttemptLogs

In the existing method (line 95–105), after building the `SessionCommand` literal, add conversion of `AttemptLogs`:

- Declare a `var sessionLogs []SessionAttemptLog`.
- If `len(c.AttemptLogs) > 0`:
  - Allocate `sessionLogs = make([]SessionAttemptLog, len(c.AttemptLogs))`.
  - For each index `i`, map:
    - `AttemptNumber` → direct copy
    - `StartedAt` → `c.AttemptLogs[i].StartedAt.Format(time.RFC3339)`
    - `EndedAt` → `c.AttemptLogs[i].EndedAt.Format(time.RFC3339)`
    - `DurationMs` → `c.AttemptLogs[i].Duration.Milliseconds()`
    - All other fields (`FailedStep`, `Command`, `ExitCode`, `Stdout`, `Stderr`, `WorkDir`, `GitBranch`, `GitStatus`) → direct copy
- Set the `AttemptLogs` field on the returned `SessionCommand`.

---

### Change 8: Update `FromSessionCommand()` to convert AttemptLogs

In the existing function (line 108–118), after building the `Command` literal, add conversion of `AttemptLogs`:

- Declare a `var logs []AttemptLog`.
- If `len(sc.AttemptLogs) > 0`:
  - Allocate `logs = make([]AttemptLog, len(sc.AttemptLogs))`.
  - For each index `i`, map:
    - `AttemptNumber` → direct copy
    - `StartedAt` → `time.Parse(time.RFC3339, sc.AttemptLogs[i].StartedAt)` — ignore error, zero `time.Time` is acceptable fallback for corrupted data
    - `EndedAt` → `time.Parse(time.RFC3339, sc.AttemptLogs[i].EndedAt)` — same
    - `Duration` → `time.Duration(sc.AttemptLogs[i].DurationMs) * time.Millisecond`
    - All other fields → direct copy
- Set the `AttemptLogs` field on the returned `*Command`.

---

## File: `internal/session/session.go`

### No changes needed

`SessionState.Commands` is `[]types.SessionCommand`. The `Save()` function uses `json.MarshalIndent` and `Load()` uses `json.Unmarshal`. Since the new `AttemptLogs` field is added to `SessionCommand` with proper JSON tags, serialization and deserialization happen automatically. The `ToCommands()` helper already calls `types.FromSessionCommand()` which we're updating in types.go.

---

## Edge Cases

1. **Empty AttemptLogs**: `FormatFailureReport()` returns `"No attempt logs recorded.\n"` when no logs exist. `ToSessionCommand()` and `FromSessionCommand()` handle nil/empty slices via the length check.

2. **Backward compatibility (old session files)**: Old JSON files won't have `attempt_logs`. `json.Unmarshal` leaves the field as `nil` — this is correct. Old code reading new session files ignores unknown fields. Full bidirectional compatibility.

3. **Time parse errors on resume**: If `StartedAt`/`EndedAt` strings are malformed (corrupted session), `time.Parse` returns zero time. `FormatFailureReport()` will show `"0001-01-01 00:00:00"` which clearly signals corruption without crashing.

4. **Large stdout/stderr**: No truncation in the struct or `FormatFailureReport()`. The data should be preserved complete for debugging. Callers can truncate for display if needed.

5. **Long prompts in report header**: Truncated to 80 characters with `"..."` to keep the report header readable.

6. **Successful attempts**: `AttemptLog` with `FailedStep: ""` represents success. `FormatFailureReport()` omits the "Failed Step" line for these entries.

7. **Duration precision**: Stored as milliseconds in JSON (int64). Sufficient for command execution timing. No floating-point issues.

8. **Zero-value AttemptLog**: All fields have sensible zero values. An uninitialized `AttemptLog` won't cause panics in `FormatFailureReport()`.

---

## Summary

| Location | Change |
|---|---|
| `internal/types/types.go` | Add `"fmt"`, `"strings"`, `"time"` imports |
| `internal/types/types.go` | Add `AttemptLog` struct (12 fields) |
| `internal/types/types.go` | Add `AttemptLogs []AttemptLog` field to `Command` |
| `internal/types/types.go` | Add `FormatFailureReport() string` method on `*Command` |
| `internal/types/types.go` | Add `SessionAttemptLog` struct (12 JSON-tagged fields) |
| `internal/types/types.go` | Add `AttemptLogs []SessionAttemptLog` field to `SessionCommand` |
| `internal/types/types.go` | Update `ToSessionCommand()` to convert `[]AttemptLog` → `[]SessionAttemptLog` |
| `internal/types/types.go` | Update `FromSessionCommand()` to convert `[]SessionAttemptLog` → `[]AttemptLog` |
| `internal/session/session.go` | **No changes** — automatic via existing JSON marshaling |
