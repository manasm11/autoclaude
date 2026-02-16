# Implementation Plan: Auto-Fix Configuration Options

## Overview

Add a configurable `auto_fix` option that controls whether the auto-fix loop runs on failure. Can be set globally in TOML config, overridden per-command, or disabled via CLI flag `--no-auto-fix`. When disabled, any failure at Planning/Running/Verifying goes directly to `StatusFailed` without entering the fix loop.

---

## File Changes

### 1. `internal/config/config.go`

**Add `AutoFix *bool` to both config structs:**

- Add `AutoFix *bool \`toml:"auto_fix"\`` to `ConfigCommand` struct (after `MaxRetries` on line 16)
- Add `AutoFix *bool \`toml:"auto_fix"\`` to `ConfigFile` struct (after `UpdateDocs` on line 23)

Using `*bool` (pointer) allows distinguishing "not set" (`nil`) from "explicitly set to false" — same pattern as the existing `UpdateDocs *bool`.

**Modify `ToCommands()` (lines 77-92) — resolve per-command override:**

In the loop body, after determining `retries` (line 80-83), add logic to resolve the `AutoFix` boolean:

```
autoFix := true                           // default
if cfg.AutoFix != nil {
    autoFix = *cfg.AutoFix                // global override
}
if cc.AutoFix != nil {
    autoFix = *cc.AutoFix                 // per-command override wins
}
```

Add `AutoFix: autoFix` to the `&types.Command{...}` literal (line 84-89).

### 2. `internal/types/types.go`

**Add `AutoFix bool` to `Command` struct (lines 62-76):**

- Add `AutoFix bool` field after `FixAttempts` (line 75). Comment: `// whether auto-fix is enabled (default true)`
- Plain `bool`, not `*bool` — by the time it reaches `Command`, the tri-state resolution is done.

**Update `NewCommand()` (lines 79-84):**

- Add `AutoFix: true` to the returned `&Command{...}` literal. This ensures commands created via `-c` CLI flags (which bypass TOML) default to auto-fix enabled.

**Add `AutoFix` to `SessionCommand` (lines 200-214):**

- Add `AutoFix *bool \`json:"auto_fix,omitempty"\`` field after `FixAttempts` (line 206).
- Using `*bool` (not plain `bool`) for session persistence. Reason: Go's `json.Unmarshal` sets missing `bool` fields to `false`, but we want the default to be `true`. With `*bool`, a missing key deserializes to `nil`, which we can detect and default to `true`.

**Update `ToSessionCommand()` (lines 217-250):**

- Create a local: `autoFix := c.AutoFix`
- Add `AutoFix: &autoFix` to the returned `SessionCommand{...}` literal (after `FixAttempts` on line 241).

**Update `FromSessionCommand()` (lines 253-288):**

- Resolve the `*bool` to `bool` with default `true`:
  ```
  autoFix := true
  if sc.AutoFix != nil {
      autoFix = *sc.AutoFix
  }
  ```
- Add `AutoFix: autoFix` to the returned `&Command{...}` literal (after `FixAttempts` on line 279).

**Edge case — old session files:** Missing `auto_fix` key → `sc.AutoFix` is `nil` → defaults to `true`. Correct behavior: old sessions get auto-fix enabled.

### 3. `internal/runner/runner.go`

**Modify `executeSingle()` — guard the three `goto fixLoop` jumps:**

At each of the three `goto fixLoop` sites, add a check that skips the fix loop when auto-fix is disabled:

**Line 237 (planning failure):**
```go
// Before:
goto fixLoop

// After:
if !cmd.AutoFix {
    sendFailed()
    return false
}
goto fixLoop
```

**Line 260 (execution failure):**
Same pattern — add `if !cmd.AutoFix { sendFailed(); return false }` before `goto fixLoop`.

**Line 279 (verification failure):**
Same pattern — add `if !cmd.AutoFix { sendFailed(); return false }` before `goto fixLoop`.

No changes to the fix loop itself — if `AutoFix` is false, we never enter it. The `sendFailed()` closure already handles StatusFailed, session save, failure report, and `ExecutionErrorMsg`.

### 4. `main.go`

**Add `noAutoFix` flag variable (lines 91-102):**

- Add `noAutoFix bool` to the `var` block (after `noDocs` on line 99).

**Register the flag (lines 104-123):**

- Add after line 112 (`--no-docs`):
  ```go
  flag.BoolVar(&noAutoFix, "no-auto-fix", false, "disable auto-fix on failure (fail immediately)")
  ```
- No short alias — consistent with `--no-docs`, `--reset-attempts`, `--clear-session`.

**Update `usage()` function (lines 33-63):**

- Add to the Flags section after `--no-docs` (line 52):
  ```
        --no-auto-fix       Disable auto-fix — fail immediately without fix attempts
  ```

**Apply `--no-auto-fix` to commands (after line 268):**

Add a new step after the existing "Apply global --max-retries" step:

```go
// 5. Apply --no-auto-fix globally
if noAutoFix {
    for _, cmd := range commands {
        cmd.AutoFix = false
    }
}
```

Renumber the existing step 5 ("Validate --auto-run requires commands") to step 6.

**Precedence logic:**
- TOML per-command `auto_fix` is resolved in `ToCommands()`.
- CLI `-c` commands get `AutoFix: true` from `NewCommand()`.
- `--no-auto-fix` flag overrides ALL commands (loops through and sets `false`).
- This matches the `--no-docs` pattern: CLI flag wins over TOML config.

### 5. `internal/config/init.go`

**Update `sampleConfig` constant (lines 9-37):**

Add a commented-out `auto_fix` line in the global settings section, after the `update_docs` comment (line 16):

```
# auto_fix = true  # auto-fix failures using Claude (default: true)
```

### 6. `README.md`

**Update Config file format example TOML block (lines 80-97):**

Add a commented-out `auto_fix` line in the global settings area:

```toml
# auto_fix = true  # auto-fix failures (default: true, set false to fail immediately)
```

**Update Config file format table (lines 99-107):**

Add two new rows after the `update_docs` row:

| `auto_fix` | global | no | `true` | Enable auto-fix: feed errors back to Claude for targeted fixes |
| `auto_fix` | command | no | global value | Per-command auto-fix override |

**Update Flag reference table (lines 153-164):**

Add a new row after `--no-docs`:

| | `--no-auto-fix` | bool | `false` | Disable auto-fix — fail immediately without fix attempts |

**Update "Retry and auto-fix behavior" section (lines 188-198):**

Add a bullet point explaining the `auto_fix` option:

```
- `auto_fix` controls whether the auto-fix loop runs on failure. Set to `false` globally
  or per-command in TOML, or use `--no-auto-fix` on the CLI. When disabled, any failure
  goes directly to Failed — `max_retries` is ignored and the command gets exactly one attempt.
```

---

## Files NOT Modified

| File | Reason |
|------|--------|
| `internal/tui/model.go` | `AutoFix` is a config setting, not execution state. The `--reset-attempts` handler should NOT reset it. The TUI displays fix state correctly via existing `StatusFixing` / `FixAttempts` logic — no changes needed. |
| `internal/session/session.go` | Thin persistence layer. All field additions are in `types` package. Session functions work generically over structs. |
| `internal/runner/runner.go` (Runner struct) | No new fields on `Runner`. `AutoFix` lives on each `Command`, not on the runner. |

---

## Data Flow Summary

```
TOML file:
  auto_fix = false           →  ConfigFile.AutoFix = ptr(false)
  [[command]]
    auto_fix = true          →  ConfigCommand.AutoFix = ptr(true)

config.ToCommands():
  per-command ptr(true) wins →  Command.AutoFix = true

CLI --no-auto-fix:
  overrides all commands     →  Command.AutoFix = false

CLI -c "prompt":
  NewCommand() default       →  Command.AutoFix = true

Session round-trip:
  Command.AutoFix (bool)     →  SessionCommand.AutoFix (*bool, non-nil)
  SessionCommand.AutoFix     →  Command.AutoFix (bool, nil defaults to true)

Runner executeSingle():
  if !cmd.AutoFix → sendFailed() immediately, skip fixLoop
```

---

## Edge Cases

1. **Old session files missing `auto_fix` key**: `SessionCommand.AutoFix` is `*bool`. Missing key → `nil` → `FromSessionCommand()` defaults to `true`. Correct.

2. **`--no-auto-fix` with `--reset-attempts`**: Independent. `--no-auto-fix` sets `AutoFix=false` during loading. `--reset-attempts` clears attempt counters on resume. No conflict.

3. **`--no-auto-fix` with `max_retries > 1`**: `max_retries` is effectively ignored when auto-fix is disabled. The command gets exactly one try. No need to change `max_retries`; the fix loop is simply never entered.

4. **Per-command `auto_fix = true` with global `auto_fix = false`**: Per-command wins in `ToCommands()`. But `--no-auto-fix` CLI flag overrides all (loops through every command).

5. **`auto_fix = false` with no verify command**: Same behavior — if execution fails (non-zero exit), fail immediately without fix attempt.

6. **Session resume with `AutoFix = false`**: Preserved through persistence. On resume, the command still has `AutoFix = false` and won't enter fix loop. `--reset-attempts` does NOT change `AutoFix`.

---

## Files Modified Summary

| File | Changes |
|------|---------|
| `internal/config/config.go` | Add `AutoFix *bool` to `ConfigFile` and `ConfigCommand`; resolve in `ToCommands()` |
| `internal/types/types.go` | Add `AutoFix bool` to `Command`; add `AutoFix *bool` to `SessionCommand`; update `NewCommand()`, `ToSessionCommand()`, `FromSessionCommand()` |
| `internal/runner/runner.go` | Guard 3 `goto fixLoop` sites with `if !cmd.AutoFix` check |
| `main.go` | Add `--no-auto-fix` flag; apply to all commands; update `usage()` |
| `internal/config/init.go` | Add commented `# auto_fix = true` to sample config |
| `README.md` | Document `auto_fix` in config table, flag table, and auto-fix behavior section |

---

## Verification

After implementation:
- `go vet ./...` — should pass with no issues
- `go build -o autoclaude .` — should compile successfully
- `go mod tidy` — no changes expected (no new dependencies)
