# Implementation Plan: Failure Report Output

## Overview

Add failure report file logging and an enhanced TUI failure panel when commands permanently fail (exhaust retries). Three areas of change: runner (file writing + enriched error message), TUI (failure panel with expandable attempt detail), and project metadata (.gitignore, README).

---

## 1. `internal/runner/runner.go` — Write failure report to file and enrich error message

### 1a. Add `"os"` and `"path/filepath"` imports

Add to the import block (lines 3–17). Both are needed for file operations.

### 1b. Add `writeFailureReport` helper method

New private method on `*Runner`:

```go
func (r *Runner) writeFailureReport(cmd *types.Command) string
```

**Behavior:**
1. Build a header block:
   ```
   ════════════════════════════════════════
   FAILURE REPORT — 2026-02-16T14:30:05Z
   Command: "first 100 chars of prompt..."
   ════════════════════════════════════════
   ```
   - Timestamp: `time.Now().Format(time.RFC3339)`
   - Prompt: truncated to 100 characters — if longer, append `"..."`
2. Call `cmd.FormatFailureReport()` to get the body
3. Combine: `header + "\n" + body + "\n\n"` into `fullReport`
4. Open file `filepath.Join(r.WorkDir, "autoclaude-error.log")` with `os.OpenFile(..., os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)`
5. Write `fullReport` to the file, close it
6. If file write fails: append a warning line to `cmd.Output` (e.g. `"[warn] failed to write error log: <err>"`). Do NOT alter the failure flow. Same pattern as `captureGitContext` and `saveSession`.
7. Return `fullReport` (caller uses it for the TUI error message)

### 1c. Enrich `ExecutionErrorMsg` with failure report data

Modify `ExecutionErrorMsg` struct (lines 31–34) to add a `FailureReport` field:

```go
type ExecutionErrorMsg struct {
    CmdIndex      int
    Err           error
    FailureReport string  // NEW: full formatted failure report
}
```

### 1d. Call `writeFailureReport` and send enriched error at permanent failure points

There are **four** places where `executeSingle` sets `StatusFailed` and returns false:

1. **Planning failure, retries exhausted** (~line 228): `cmd.Status = types.StatusFailed` then `return false`
2. **Running failure, retries exhausted** (~line 260): same pattern
3. **Verifying failure, retries exhausted** (~line 291): same pattern
4. **Post-loop `!success` guard** (~line 304): safety net, same pattern

At each of these four points, **after** `r.sendUpdate(...)` and `r.saveSession()` but **before** `return false`, insert:

```go
report := r.writeFailureReport(cmd)
if r.program != nil {
    r.program.Send(ExecutionErrorMsg{
        CmdIndex:      i,
        Err:           fmt.Errorf("command failed after %d attempt(s)", cmd.Attempts),
        FailureReport: report,
    })
}
```

Only one of these four paths executes per call, so no duplication risk.

---

## 2. `internal/tui/model.go` — Failure panel with expandable attempt details

### 2a. Add new fields to `Model` struct (lines 108–132)

```go
failureReport    string  // Full failure report text from runner
failedCmdIndex   int     // Index of the failed command (-1 if none)
showExpandedLog  bool    // 'l' toggle: show all attempts' full stdout/stderr
failureScrollOff int     // Scroll offset for failure panel viewport
```

Initialize `failedCmdIndex` to `-1` in `NewModel()` (line 135).

### 2b. Handle enriched `ExecutionErrorMsg` in `Update()` (lines 379–381)

Currently:
```go
case runner.ExecutionErrorMsg:
    m.err = msg.Err
    return m, nil
```

Change to:
```go
case runner.ExecutionErrorMsg:
    m.err = msg.Err
    m.failureReport = msg.FailureReport
    m.failedCmdIndex = msg.CmdIndex
    m.failureScrollOff = 0
    m.showExpandedLog = false
    return m, nil
```

### 2c. Add `'l'` keybinding in `handleRunningKey()` (lines 571–597)

Add a new case in the switch at line 574:

```go
case "l":
    if m.done && m.failedCmdIndex >= 0 {
        m.showExpandedLog = !m.showExpandedLog
        m.failureScrollOff = 0  // reset scroll on toggle
    }
    return m, nil
```

### 2d. Modify up/down/j/k scroll in `handleRunningKey()` for done+failure state

Currently (lines 583–593) up/down always scroll `m.scrollOffset`. When `m.done && m.failedCmdIndex >= 0`, they should scroll `m.failureScrollOff` instead (the failure panel viewport).

Change the existing cases:

```go
case "up", "k":
    if m.done && m.failedCmdIndex >= 0 {
        if m.failureScrollOff > 0 {
            m.failureScrollOff--
        }
    } else {
        if m.scrollOffset > 0 {
            m.scrollOffset--
        }
    }
    return m, nil
case "down", "j":
    if m.done && m.failedCmdIndex >= 0 {
        // Cap will happen during rendering
        m.failureScrollOff++
    } else {
        max := m.maxScrollOffset()
        if m.scrollOffset < max {
            m.scrollOffset++
        }
    }
    return m, nil
```

### 2e. Add `failureViewportHeight()` helper

New method:

```go
func (m Model) failureViewportHeight() int
```

Calculate available height for the scrollable portion of the failure panel:
- Total terminal height minus: title (2 lines) + summary header (4 lines) + command list (`len(m.commands)` lines) + failure panel chrome (~8 lines: section header, info fields, separator, footer note, blank lines) + help bar (2 lines)
- Minimum 3 lines
- Used to cap `m.failureScrollOff` and determine visible line count

### 2f. Add `viewFailurePanel()` method

New method:

```go
func (m Model) viewFailurePanel() string
```

**Compact view** (default, `showExpandedLog == false`):

```
═══ FAILURE DETAILS ═══

  Command #3:  "Add error handling to the API endpoi..."
  Attempts:    3 / 3
  Last failed: Verifying
  Exit code:   1

─── Last attempt stderr ───
<scrollable viewport of last attempt's Stderr>

Full report written to autoclaude-error.log
```

Populated from:
- `cmd := m.commands[m.failedCmdIndex]`
- `lastAttempt := cmd.AttemptLogs[len(cmd.AttemptLogs)-1]`
- Command number: `m.failedCmdIndex + 1` (1-based display)
- Prompt: `truncate(cmd.Prompt, 80)` (existing helper)
- Attempts: `cmd.Attempts` / `cmd.MaxRetries`
- Last failed step: `lastAttempt.FailedStep`
- Exit code: `lastAttempt.ExitCode`
- Stderr content: split `lastAttempt.Stderr` into lines, render scrollable viewport using `m.failureScrollOff` and `m.failureViewportHeight()`

**Expanded view** (`showExpandedLog == true`):

Replace the "Last attempt stderr" section with the full `m.failureReport` (header + all attempts with stdout/stderr). Split into lines, render scrollable using same `m.failureScrollOff` / `m.failureViewportHeight()`.

Keep the same info block and footer.

**Edge cases:**
- If `cmd.AttemptLogs` is empty: show "No attempt data available" instead of indexing into empty slice
- If `lastAttempt.Stderr` is empty: show `(no stderr captured)`
- Cap `m.failureScrollOff` to `max(0, totalLines - viewportHeight)` during rendering (prevents over-scroll)
- Style the section headers with the existing `statusFailed` lipgloss style (red)

### 2g. Integrate `viewFailurePanel()` into `viewRunningDone()` (lines 1009–1056)

After the per-command results loop (line 1045), before the existing error display (line 1048), insert:

```go
if m.failedCmdIndex >= 0 {
    b.WriteString("\n")
    b.WriteString(m.viewFailurePanel())
}
```

Remove or conditionalize the existing `m.err` display (lines 1048–1050) since the failure panel now shows richer information. If `m.failedCmdIndex >= 0`, the panel handles error display. Otherwise, keep the `m.err` fallback for unexpected errors.

### 2h. Update help bar in `viewRunningDone()` (line 1053)

Change from:
```go
b.WriteString(helpStyle.Render("q: quit"))
```

To:
```go
if m.failedCmdIndex >= 0 {
    b.WriteString(helpStyle.Render("l: toggle full log  |  up/down: scroll  |  q: quit"))
} else {
    b.WriteString(helpStyle.Render("q: quit"))
}
```

---

## 3. `.gitignore` — Add error log entry

After the session state entry (line 34), add:

```
# Error logs
autoclaude-error.log
```

---

## 4. `README.md` — Document failure logging and new keybinding

### 4a. Add "Failure logging" section

Insert a new section **before** the "Keybindings" section (before line 323 `## Keybindings`):

```markdown
## Failure logging

When a command permanently fails (all retry attempts exhausted), autoclaude writes a detailed failure report to `autoclaude-error.log` in the working directory. The file is append-only — each failure adds a timestamped entry, so logs accumulate across runs.

Each entry includes:
- Timestamp and command prompt
- Per-attempt details: failed step, exit code, duration, stdout/stderr
- Git context (branch, status) at the time of each attempt

The log file is included in `.gitignore` by default.

When execution finishes with failures, the TUI shows a failure panel with:
- Command number, prompt, attempt count, last failed step, and exit code
- Scrollable last-attempt stderr
- Press `l` to expand the full failure report showing all attempts with stdout/stderr

This file is useful for debugging failures in `--auto-run` mode where the TUI is non-interactive.
```

### 4b. Update Running view keybindings table (lines 361–368)

Add the `l` keybinding row to the Running view table:

```markdown
### Running view

| Key | Action |
|-----|--------|
| `j` / `Down` | Scroll output down |
| `k` / `Up` | Scroll output up |
| `l` | Toggle expanded failure log (after execution) |
| `q` | Quit (after execution completes) |
| `Ctrl+C` | Force quit |
```

---

## 5. Edge Cases and Considerations

1. **File write permission errors**: `writeFailureReport` handles `os.OpenFile` errors gracefully — appends a warning to `cmd.Output` but doesn't alter the failure flow or panic.

2. **Empty AttemptLogs**: Defensive check in `viewFailurePanel()` — if `cmd.AttemptLogs` has length 0, show "No attempt data available" instead of panicking on slice access.

3. **Very long stderr / report**: The scrollable viewport in the failure panel handles arbitrary length. Scroll offset is capped during rendering.

4. **Multiple failed commands**: Currently execution halts on first permanent failure (`executeAll` returns when `executeSingle` returns false). So there will only ever be one `failedCmdIndex`. If this changes in the future, the field stores the most recently failed command.

5. **Concurrent file access**: Not an issue — only one runner writes to the log file, and it opens/writes/closes synchronously.

6. **Auto-run mode**: The failure report file is especially useful here since the TUI is non-interactive. The file persists after exit.

7. **`failureScrollOff` vs `scrollOffset`**: Separate fields. During live execution, up/down scrolls `scrollOffset` (output viewport). In done state with failure, up/down scrolls `failureScrollOff` (failure panel). No confusion.

8. **Toggle reset**: When pressing `l` to toggle between compact/expanded view, `failureScrollOff` resets to 0 so the user starts at the top of whichever view they switched to.

9. **No-failure case**: When all commands succeed, `failedCmdIndex` stays at `-1`. The failure panel is never rendered. The help bar shows just "q: quit". No behavioral change for the happy path.

---

## Files Modified

| File | Change Summary |
|------|----------------|
| `internal/runner/runner.go` | Add `os`, `path/filepath` imports; add `writeFailureReport()` method; add `FailureReport` field to `ExecutionErrorMsg`; send enriched error message at all 4 permanent-failure return points |
| `internal/tui/model.go` | Add `failureReport`, `failedCmdIndex`, `showExpandedLog`, `failureScrollOff` fields to `Model`; handle enriched `ExecutionErrorMsg`; add `l` keybinding; modify up/down to scroll failure panel when done; add `viewFailurePanel()` and `failureViewportHeight()` methods; integrate panel into `viewRunningDone()`; update help bar |
| `.gitignore` | Add `autoclaude-error.log` entry |
| `README.md` | Add "Failure logging" section before Keybindings; add `l` keybinding to Running view table |

**No new files created.**
