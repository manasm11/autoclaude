# Implementation Plan: Auto-Documentation Step

Add a `Documenting` phase to the command execution lifecycle that runs Claude to update `CLAUDE.md` and `README.md` after successful verification but before git commit. The step is non-fatal — failures produce a warning but don't block the commit.

---

## File Changes

### 1. `internal/types/types.go`

**Add `StatusDocumenting` constant** — insert between `StatusVerifying` (3) and `StatusCommitting` (4):

```
const (
    StatusPending    CommandStatus = iota // 0
    StatusPlanning                        // 1
    StatusRunning                         // 2
    StatusVerifying                       // 3
    StatusDocumenting                     // 4  ← NEW
    StatusCommitting                      // 5  (was 4)
    StatusSuccess                         // 6  (was 5)
    StatusFailed                          // 7  (was 6)
    StatusRetrying                        // 8  (was 7)
)
```

**Update `String()` method** — insert `"Documenting"` into the `labels` slice at index 4 (between `"Verifying"` and `"Committing"`):

```go
labels := []string{
    "Pending",
    "Planning",
    "Running",
    "Verifying",
    "Documenting",  // ← NEW
    "Committing",
    "Success",
    "Failed",
    "Retrying",
}
```

**Update `ParseCommandStatus()`** — add a case for `"Documenting"`:

```go
case "Documenting":
    return StatusDocumenting
```

This is important because session persistence round-trips status through strings. Without this, a session saved during the documenting phase would deserialize to `StatusPending` on resume.

### 2. `internal/config/config.go`

**Add `UpdateDocs` field to `ConfigFile` struct** (line 20-24):

```go
type ConfigFile struct {
    MaxRetries int             `toml:"max_retries"`
    WorkDir    string          `toml:"work_dir"`
    UpdateDocs *bool           `toml:"update_docs"`  // ← NEW (pointer to distinguish unset from false)
    Commands   []ConfigCommand `toml:"command"`
}
```

Use `*bool` so that:
- `nil` = not set in TOML → defaults to `true`
- `&true` = explicitly enabled
- `&false` = explicitly disabled

**No changes to `LoadConfig()`** — TOML unmarshaling handles `*bool` correctly (sets nil when key is absent, sets `*bool` when present). No default-setting logic needed since we apply the default downstream.

**No changes to `ToCommands()`** — `UpdateDocs` is a global setting, not per-command.

### 3. `internal/runner/runner.go`

#### Add `NoDocs` field to `Runner` struct (line 41-49):

```go
type Runner struct {
    Commands     []*types.Command
    CurrentIndex int
    WorkDir      string
    MaxRetries   int
    NoDocs       bool            // ← NEW: skip documentation update step
    program      *tea.Program
    ctx          context.Context
    cancel       context.CancelFunc
}
```

#### Add documentation phase in `executeSingle()` — between verification (ends line 204) and commit (starts line 218):

Insert after the `success = true; break` block (line 207-209) and before the commit section (line 218). The new code goes at approximately line 210, after `success` check but before the commit block:

```
// After the success check (line 216) and before "// 4. COMMIT" (line 218):

// 4. DOCUMENTATION (non-fatal)
if !r.NoDocs {
    cmd.Status = types.StatusDocumenting
    r.sendUpdate(i, types.StatusDocumenting, cmd.Output)
    r.saveSession()

    docPrompt := `Review the changes just made in this project. Update the following documentation files to reflect these changes:

1. CLAUDE.md — This is the project memory file for Claude Code. Update it with any new conventions, architecture decisions, file structure changes, dependencies added, or important patterns established by the recent changes. Create the file if it doesn't exist. Keep it concise and useful as a reference for future Claude Code sessions.

2. README.md — Update the user-facing documentation to reflect any new features, usage changes, API changes, or configuration options introduced by the recent changes. Create the file if it doesn't exist. Do not remove existing content unless it's outdated due to the changes.

Only update sections relevant to the recent changes. Do not rewrite unrelated sections. If no documentation updates are needed, make no changes.

Recent task that was executed: ` + cmd.Prompt

    docOutput, docErr := r.runClaude(i, docPrompt)
    if docErr != nil {
        cmd.Output = cmd.Output + "\n═══ DOCUMENTATION ═══\n" +
            fmt.Sprintf("[warn] documentation update failed: %v", docErr)
    } else {
        cmd.Output = cmd.Output + "\n═══ DOCUMENTATION ═══\n" + docOutput
    }
    r.sendUpdate(i, cmd.Status, cmd.Output)
}
```

**Key design decisions:**
- Documentation failure does NOT cause retry or failure — it appends a `[warn]` and proceeds to commit
- The doc output is always appended to `cmd.Output` with the `═══ DOCUMENTATION ═══` separator (matching the existing `═══ PLAN ═══` and `═══ EXECUTION ═══` pattern)
- After the doc step completes (success or fail), we continue to the commit phase. The status is left as `StatusDocumenting` momentarily before the commit phase sets it to `StatusCommitting`

**Renumber the commit comment** from `// 4. COMMIT` to `// 5. COMMIT` for clarity.

### 4. `main.go`

#### Add `--no-docs` flag variable (line 89-98):

Add `noDocs bool` to the flag variables block:

```go
var (
    configFile   string
    cmds         stringSlice
    maxRetries   int
    workDir      string
    autoRun      bool
    noResume     bool
    clearSession bool
    noDocs       bool    // ← NEW
    showHelp     bool
)
```

#### Register the flag (after line 107, with the other long flags):

```go
flag.BoolVar(&noDocs, "no-docs", false, "skip automatic documentation updates (CLAUDE.md, README.md)")
```

No short flag alias — `-d` could conflict with future flags and `--no-docs` is clear enough.

#### Apply the flag + config to the runner (after line 269 `r.MaxRetries = maxRetries`):

```go
// Determine whether to skip docs: --no-docs flag takes priority, then TOML config
if noDocs {
    r.NoDocs = true
} else if configFile != "" {
    // cfg was loaded earlier — need to check cfg.UpdateDocs
    // But cfg is scoped inside the if-block at line 214...
}
```

**Problem:** The `cfg` variable from config loading (line 223) is scoped inside the `if configFile != ""` block and isn't accessible at runner construction time (line 268).

**Solution:** Hoist the `UpdateDocs` value out of the config loading block. Add a variable at the top of the command-building section:

```go
var updateDocsFromConfig *bool  // nil = not set in config
```

Inside the config loading block (after line 237), extract the value:

```go
updateDocsFromConfig = cfg.UpdateDocs
```

Then when setting up the runner (after line 269):

```go
if noDocs {
    r.NoDocs = true
} else if updateDocsFromConfig != nil && !*updateDocsFromConfig {
    r.NoDocs = true
}
```

This gives flag priority over config, with default `true` (docs enabled) when neither is specified.

#### Update `usage()` function (line 33-61):

Add the `--no-docs` flag to the flags section:

```
      --no-docs           Skip automatic documentation updates (CLAUDE.md, README.md)
```

Insert after the `--clear-session` line (line 51).

### 5. `internal/tui/model.go`

#### Add `statusDocumenting` style (between `statusVerifying` and `statusCommitting`, around line 76):

```go
statusDocumenting = lipgloss.NewStyle().
    Foreground(lipgloss.Color("#87CEEB"))  // Sky blue / light blue
```

#### Update `statusStyleFor()` function (line 914-935):

Add a case for `StatusDocumenting` between `StatusVerifying` and `StatusCommitting`:

```go
case types.StatusDocumenting:
    return statusDocumenting
```

#### Update `statusIcon()` function (line 1098-1119):

Add a case for `StatusDocumenting` between `StatusVerifying` and `StatusCommitting`. Use `📝` semantically, but since the codebase uses Unicode code points, use `\u270d` (✍ writing hand) or `\u2261` (≡ triple bar):

```go
case types.StatusDocumenting:
    return "\u270d"  // ✍
```

### 6. `internal/config/init.go`

#### Update `sampleConfig` constant to include `update_docs`:

Add after the `# work_dir` line (line 15):

```toml
# update_docs = true  # auto-update CLAUDE.md and README.md after each command (default: true)
```

The line is commented out so it documents the option without changing the default behavior.

### 7. `README.md`

#### Add documentation for the auto-documentation feature

**In the Flag Reference table** (around line 150-161), add a row:

```
| `--no-docs`          |                | Skip automatic documentation updates |
```

**Add a new section** after "Retry Behavior" (line 190) and before "Session Resume" (line 192):

```markdown
### Automatic Documentation Updates

After each successful command execution (including verification), autoclaude runs an additional Claude Code pass to update project documentation:

- **CLAUDE.md** — Updated with new conventions, architecture decisions, file structure changes, dependencies, and patterns established by the recent changes. This file serves as a project memory for future Claude Code sessions.
- **README.md** — Updated with new features, usage changes, API changes, or configuration options introduced by the recent changes. Existing content is preserved unless it's outdated.

This step is **non-fatal**: if the documentation update fails, a warning is appended to the output and the commit proceeds normally.

#### Disabling Documentation Updates

Via CLI flag:
```sh
autoclaude --no-docs -f commands.toml
```

Via TOML config:
```toml
update_docs = false
```

The `--no-docs` flag takes priority over the TOML setting.
```

**Update the Command Lifecycle section** (around line 163-182) to include the Documenting state:

Change the state diagram to show:
```
Planning → Running → Verifying → Documenting → Committing → Success
```

Add `Documenting` to the state descriptions:
```
| Documenting | ✍ | Updating CLAUDE.md and README.md (non-fatal on failure) |
```

---

## Edge Cases

1. **`--no-docs` + `update_docs = true` in TOML**: Flag wins, docs are skipped
2. **`update_docs = false` in TOML + no `--no-docs` flag**: Config wins, docs are skipped
3. **Neither specified**: Default is `true`, docs are generated
4. **Doc step fails**: Warning appended to output (`[warn] documentation update failed: <error>`), commit proceeds normally
5. **Session resume during documenting phase**: `ParseCommandStatus("Documenting")` correctly round-trips. On resume, the status will be `StatusDocumenting` — the runner's retry loop will re-execute from the current attempt, which means the doc step would run again before commit. This is fine since docs are idempotent.
6. **No verification command**: The flow skips verification and goes directly from Running → Documenting → Committing
7. **iota renumbering**: All status constants after `StatusVerifying` shift by +1. This is safe because:
   - Session persistence uses string labels (`"Committing"`, etc.), not integer values
   - The `String()` method uses a slice indexed by the iota value, so the labels slice must be updated in sync
   - No external consumers store raw integer values

---

## Files Modified (Summary)

| File | Change |
|------|--------|
| `internal/types/types.go` | Add `StatusDocumenting` constant, update `String()`, update `ParseCommandStatus()` |
| `internal/config/config.go` | Add `UpdateDocs *bool` field to `ConfigFile` |
| `internal/config/init.go` | Add `update_docs` comment to sample config |
| `internal/runner/runner.go` | Add `NoDocs` field to `Runner`, add documentation phase in `executeSingle()` |
| `internal/tui/model.go` | Add `statusDocumenting` style, update `statusStyleFor()`, update `statusIcon()` |
| `main.go` | Add `--no-docs` flag, wire config `UpdateDocs` to runner `NoDocs` |
| `README.md` | Document the feature, flag, and config option |

---

## Build Verification

After all changes, run:
```sh
go vet ./...
go build ./...
go mod tidy
```
