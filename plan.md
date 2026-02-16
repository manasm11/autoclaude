# Plan: Remove old retry mechanism and clean up codebase

## Summary

Remove `StatusRetrying` from the codebase entirely, update all "retry" wording in user-facing strings to "fix attempt" / "auto-fix", and update documentation (CLAUDE.md and README.md) to consistently describe the auto-fix behavior. The `max_retries` TOML/flag/field name stays the same to avoid breaking configs.

---

## Step 1: Remove `StatusRetrying` from `internal/types/types.go`

### 1a. Remove the iota constant

**Problem:** `StatusRetrying` (iota 8) exists solely for backward compatibility, but removing it shifts `StatusFixing` from 9 to 8. This is safe because:
- `StatusRetrying` is not used anywhere in Go code (no switch cases, no TUI rendering, no runner logic reference it)
- Session files store status as a *string* (`"Fixing"`, `"Failed"`, etc.), not as an integer — the iota value doesn't matter for deserialization
- `ParseCommandStatus()` already doesn't handle `"Retrying"` — it falls through to `StatusPending`

**Changes in the const block (line 12-23):**
- Remove line 21: `StatusRetrying                        // 8`
- `StatusFixing` will now become iota 8 (was 9). This is safe since no code depends on the numeric value.

After:
```go
const (
    StatusPending    CommandStatus = iota // 0
    StatusPlanning                        // 1
    StatusRunning                         // 2
    StatusVerifying                       // 3
    StatusDocumenting                     // 4
    StatusCommitting                      // 5
    StatusSuccess                         // 6
    StatusFailed                          // 7
    StatusFixing                          // 8
)
```

### 1b. Remove "Retrying" from the `String()` labels slice

**Changes in `String()` method (line 27-43):**
- Remove line 36: `"Retrying",`
- The labels slice becomes: `["Pending", "Planning", "Running", "Verifying", "Documenting", "Committing", "Success", "Failed", "Fixing"]`

After:
```go
func (s CommandStatus) String() string {
    labels := []string{
        "Pending",
        "Planning",
        "Running",
        "Verifying",
        "Documenting",
        "Committing",
        "Success",
        "Failed",
        "Fixing",
    }
    ...
}
```

### 1c. No changes needed to `ParseCommandStatus()`

`ParseCommandStatus()` (line 160-183) already does NOT have a case for `"Retrying"` — unrecognized values fall through to `StatusPending`. No change needed.

---

## Step 2: Update "retry" wording in Go source files

### 2a. `internal/tui/model.go`

Three comments contain "retry" wording that should be updated:

1. **Line 132** — Field comment on `resetAttempts`:
   - Old: `// --reset-attempts: reset retry budget on resume`
   - New: `// --reset-attempts: reset fix attempt budget on resume`

2. **Line 211** — `SetResetAttempts()` doc comment:
   - Old: `// SetResetAttempts configures the model to reset attempt counters on resume, giving a full fresh retry budget.`
   - New: `// SetResetAttempts configures the model to reset attempt counters on resume, giving a full fresh fix attempt budget.`

3. **Line 283** — Comment in `resumeRunMsg` handler:
   - Old: `// Reset the resume-from command to pending, preserving retry budget`
   - New: `// Reset the resume-from command to pending, preserving fix attempt budget`

### 2b. `main.go`

Two user-facing strings contain "retry":

1. **Line 51** — Usage text for `--reset-attempts`:
   - Old: `      --reset-attempts    Reset attempt counters on resume (gives full retry budget)`
   - New: `      --reset-attempts    Reset attempt counters on resume (gives full fix attempt budget)`

2. **Line 113** — `flag.BoolVar` description for `--reset-attempts`:
   - Old: `flag.BoolVar(&resetAttempts, "reset-attempts", false, "reset attempt counters on resume (gives full retry budget)")`
   - New: `flag.BoolVar(&resetAttempts, "reset-attempts", false, "reset attempt counters on resume (gives full fix attempt budget)")`

### 2c. `internal/config/init.go`

1. **Line 35** — Sample config prompt text:
   - Old: `prompt = "Describe a task with custom retry limit."`
   - New: `prompt = "Describe a task with custom fix attempt limit."`

---

## Step 3: Update `example.autoclaude.toml`

1. **Line 14** — Comment:
   - Old: `# Maximum retry attempts per command (default: 3).`
   - New: `# Maximum fix attempts per command (default: 3).`

2. **Line 15** — stays the same (references `max_retries` which is the TOML key name, not user-facing wording)

3. **Line 33-34** — Comment about retrying:
   - Old: `# command is retried (up to the global max_retries).`
   - New: `# command triggers auto-fix attempts (up to the global max_retries).`

4. **Line 39** — Comment:
   - Old: `# A command with its own retry limit that overrides the global value.`
   - New: `# A command with its own fix attempt limit that overrides the global value.`

---

## Step 4: Update `README.md`

### 4a. Config table description (line 100)

- Old: `prompt = "Quick one-shot task that should not retry"`
- New: `prompt = "Quick one-shot task that should not auto-fix"`

### 4b. Config table field descriptions (lines 106, 112)

1. **Line 106:**
   - Old: `| `max_retries` | global | no | `3` | Default retry limit for all commands |`
   - New: `| `max_retries` | global | no | `3` | Default fix attempt limit for all commands |`

2. **Line 112:**
   - Old: `| `max_retries` | command | no | global value | Per-command retry override |`
   - New: `| `max_retries` | command | no | global value | Per-command fix attempt limit override |`

### 4c. Flag reference table (line 168)

- Old: `| | `--reset-attempts` | bool | `false` | Reset attempt counters on resume (gives full retry budget) |`
- New: `| | `--reset-attempts` | bool | `false` | Reset attempt counters on resume (gives full fix attempt budget) |`

### 4d. Section heading (line 196)

- Old: `### Retry and auto-fix behavior`
- New: `### Auto-fix behavior`

### 4e. Attempt logging section (line 248)

- Old: `Each retry attempt is recorded in a detailed attempt log capturing:`
- New: `Each attempt (initial + fix attempts) is recorded in a detailed attempt log capturing:`

### 4f. Session resume section (line 328)

- Old: `When resuming a session, autoclaude preserves the full retry and fix state from the previous run.`
- New: `When resuming a session, autoclaude preserves the full fix attempt state from the previous run.`

### 4g. Reset attempts section (line 330)

- Old: `If you've manually fixed an issue and want to give the command a full fresh retry budget, use `--reset-attempts`:`
- New: `If you've manually fixed an issue and want to give the command a full fresh fix attempt budget, use `--reset-attempts`:`

### 4h. Failure logging section (line 384)

- Old: `When a command permanently fails (all retry attempts exhausted), autoclaude writes a detailed failure report`
- New: `When a command permanently fails (all fix attempts exhausted), autoclaude writes a detailed failure report`

---

## Step 5: Update `CLAUDE.md`

### 5a. StatusRetrying references

1. **Line 52** — Remove mention of StatusRetrying:
   - Old: `- **`StatusFixing`** (iota 9): Command status for when Claude is auto-fixing a failure. Appended after `StatusRetrying` — no existing iota values shift.`
   - New: `- **`StatusFixing`** (iota 8): Command status for when Claude is auto-fixing a failure.`

2. **Line 56** — Remove entire bullet about StatusRetrying:
   - Old: `- **`StatusRetrying`** is still present in the iota block and `String()` labels for backward compatibility...`
   - **Delete this entire line/bullet.**

### 5b. "retry" wording in CLAUDE.md

1. **Line 48:**
   - Old: `The old retry-from-planning loop has been replaced with an auto-fix system. On failure, Claude analyzes the error and makes targeted code fixes, then only re-runs verification (not the full plan+execute cycle).`
   - New: `On failure, Claude analyzes the error and makes targeted code fixes, then only re-runs verification (not the full plan+execute cycle).`

2. **Line 60** — Comment mentions "No retry loop":
   - Old: `No retry loop wrapping the initial attempt.`
   - New: `No loop wrapping the initial attempt.`

3. **Line 87** — Section heading:
   - Old: `### Session resume and retry budget (internal/tui/model.go)`
   - New: `### Session resume and fix attempt budget (internal/tui/model.go)`

4. **Line 89:**
   - Old: `- **Retry budget preservation on resume**:`
   - New: `- **Fix attempt budget preservation on resume**:`
   - Also in same line: `This ensures the retry budget accounts for` → `This ensures the fix attempt budget accounts for`

5. **Line 90:**
   - Old: `...to give a full fresh retry budget and clean failure context.`
   - New: `...to give a full fresh fix attempt budget and clean failure context.`

---

## Step 6: Run verification commands

```sh
go mod tidy
go vet ./...
go build ./...
```

These confirm no compile errors, unused imports, or dead code were introduced.

---

## Step 7: Review — files NOT changed (and why)

### `internal/runner/runner.go`
- Contains NO references to `StatusRetrying` or "retry" wording in comments/strings. `MaxRetries` field name stays. No changes needed.

### `internal/session/session.go`
- Contains NO references to `StatusRetrying` or "retry" wording. `MaxRetries` JSON key stays. No changes needed.

### `internal/config/config.go`
- Contains NO references to `StatusRetrying` or "retry" wording. `MaxRetries` TOML key stays. No changes needed.

### `internal/types/types.go` (beyond Step 1)
- `MaxRetries` field name on `Command` (line 65) and `SessionCommand` (line 205) — stays the same (TOML/JSON key, would break configs/sessions)

### `autoclaude.toml`
- This is the project's own task config file (the commands that built autoclaude itself). It contains historical references to the retry-to-auto-fix migration as prompt strings. **No changes needed** — this file is a historical record of the build steps.

### `PLAN.md`, `plan.impl.md`
- Historical planning documents from earlier implementation steps. **No changes needed** — these are internal artifacts, not user-facing documentation.

---

## Edge cases

1. **Existing session files with `"Retrying"` status**: `ParseCommandStatus()` already maps unknown strings (including `"Retrying"`) to `StatusPending`, so these commands restart from the beginning. This behavior is unchanged.

2. **Iota value shift**: `StatusFixing` moves from 9 to 8. Session files store status as strings, not integers, so this is safe. No external API depends on the numeric value.

3. **`max_retries` naming**: Intentionally preserved everywhere — TOML config key, CLI flag name, JSON session key, Go struct field names. Renaming would break backward compatibility.

---

## Files changed (summary)

| File | Type of change |
|------|---------------|
| `internal/types/types.go` | Remove `StatusRetrying` constant and `"Retrying"` label |
| `internal/tui/model.go` | Update 3 comments: "retry budget" → "fix attempt budget" |
| `main.go` | Update 2 strings: usage text and flag description |
| `internal/config/init.go` | Update 1 sample prompt string |
| `example.autoclaude.toml` | Update 3 comments |
| `README.md` | Update ~8 instances of "retry" wording |
| `CLAUDE.md` | Remove StatusRetrying references, update ~5 "retry" wordings |
