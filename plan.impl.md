# Implementation Plan: TUI Auto-Fix Flow Support

## Overview

Update `internal/tui/model.go` and `internal/types/types.go` to fully support the auto-fix flow in the TUI. This involves removing dead `StatusRetrying` references from the TUI, enhancing the live execution view for `StatusFixing`, improving the failure panel with per-attempt summaries, and updating queue view icons.

**Files to modify:**
1. `internal/tui/model.go` — Main changes (6 areas)
2. `internal/types/types.go` — Minor: keep `StatusRetrying` in iota but no TUI changes needed there

**Files NOT modified:**
- `internal/types/types.go` — `StatusRetrying` stays in iota for backward compat (preserves `StatusFixing = iota 9`). The `ParseCommandStatus` case for `"Retrying"` also stays so persisted sessions with that status can be deserialized. No changes needed.
- `internal/runner/runner.go` — Already sends `StatusFixing` and `StatusDetail` correctly. No changes needed.

---

## Change 1: Update queue view status icon for Fixing

**File:** `internal/tui/model.go`
**Function:** `statusIcon()` (line 1278-1301)

**Current code (line 1296-1297):**
```go
case types.StatusFixing:
    return "\u21bb" // ↻
```

**Change to:**
```go
case types.StatusFixing:
    return "\u26a1" // ⚡
```

This satisfies requirement 6: "Fixing: orange ⚡ symbol". The orange color is already handled by `statusFixing` style (gold `#FFD700`), which is applied via `styledStatusIcon()`.

---

## Change 2: Enhanced live execution view for StatusFixing

**File:** `internal/tui/model.go`
**Function:** `viewRunningLive()` (lines 1121-1169)

**Current code (lines 1132-1146):**
```go
if m.currentCmd >= 0 && m.currentCmd < len(m.commands) {
    cmd := m.commands[m.currentCmd]
    b.WriteString(promptLabelStyle.Render("Prompt: "))
    b.WriteString(truncate(cmd.Prompt, 200))
    b.WriteString("\n")
    b.WriteString(m.spinner.View())
    b.WriteString(" ")
    b.WriteString(styledStatus(cmd.Status))
    if m.statusDetail != "" {
        b.WriteString("  ")
        b.WriteString(helpStyle.Render(m.statusDetail))
    }
    b.WriteString("\n\n")
}
```

**Replace with:**
```go
if m.currentCmd >= 0 && m.currentCmd < len(m.commands) {
    cmd := m.commands[m.currentCmd]
    b.WriteString(promptLabelStyle.Render("Prompt: "))
    b.WriteString(truncate(cmd.Prompt, 200))
    b.WriteString("\n")
    b.WriteString(m.spinner.View())
    b.WriteString(" ")
    b.WriteString(styledStatus(cmd.Status))
    if m.statusDetail != "" {
        b.WriteString("  ")
        b.WriteString(helpStyle.Render(m.statusDetail))
    }
    b.WriteString("\n")

    // When fixing, show what failed and condensed error
    if cmd.Status == types.StatusFixing {
        b.WriteString("\n")
        // Show what failed
        if cmd.LastFailedStep != "" {
            failedLabel := fmt.Sprintf("%s failed (exit code %d)",
                capitalize(cmd.LastFailedStep), cmd.LastExitCode)
            b.WriteString("  ")
            b.WriteString(statusFailed.Render(failedLabel))
            b.WriteString("\n")
        }
        // Condensed stderr (last 10 lines)
        if cmd.LastStderr != "" {
            stderrLines := lastNLines(cmd.LastStderr, 10)
            b.WriteString(helpStyle.Render("  Last stderr:"))
            b.WriteString("\n")
            for _, line := range strings.Split(stderrLines, "\n") {
                if len(line) > m.width-6 {
                    line = line[:m.width-6]
                }
                b.WriteString(outputStyle.Render("    " + line))
                b.WriteString("\n")
            }
        }
        // Current fix attempt
        b.WriteString("  ")
        b.WriteString(statusFixing.Render(fmt.Sprintf("Fix attempt %d/%d", cmd.FixAttempts, cmd.MaxRetries)))
        b.WriteString("\n")
    }

    b.WriteString("\n")
}
```

**New helper functions needed** (add before or after `truncate`):

```go
// capitalize returns s with the first letter uppercased.
func capitalize(s string) string {
    if s == "" {
        return s
    }
    return strings.ToUpper(s[:1]) + s[1:]
}

// lastNLines returns the last n lines of s. If s has fewer than n lines, returns all of s.
func lastNLines(s string, n int) string {
    lines := strings.Split(s, "\n")
    if len(lines) <= n {
        return s
    }
    return strings.Join(lines[len(lines)-n:], "\n")
}
```

**Note:** `lastNLines` already exists in `internal/runner/runner.go` (line 139-149) but is unexported and in a different package. We need a local copy in the TUI package. Same logic: split by newline, take last N, rejoin.

**Edge cases:**
- `cmd.LastStderr` is empty: skip the stderr section entirely
- `cmd.LastFailedStep` is empty: skip the "what failed" line
- `m.width` is very small: `m.width - 6` minimum clamped by existing `maxWidth` pattern (use same `< 40` guard)
- `cmd.MaxRetries` is 0: display would show "Fix attempt 0/0" — this shouldn't happen in practice since runner checks budget before entering fix loop

This satisfies requirement 3: show "Auto-fixing..." with spinner (via spinner + styledStatus), what failed, condensed error, and fix attempt count.

---

## Change 3: Update status flow display in execution view header

**File:** `internal/tui/model.go`
**Function:** `viewRunningLive()` (lines 1121-1169)

Add a status flow breadcrumb after the progress line. Insert after the progress line (`Running command X/N`), before the current command block:

**After line 1129 (`b.WriteString(progress)`), add:**
```go
b.WriteString("\n")
// Status flow breadcrumb
b.WriteString(m.renderStatusFlow())
```

**New function `renderStatusFlow()`:**
```go
func (m Model) renderStatusFlow() string {
    if m.currentCmd < 0 || m.currentCmd >= len(m.commands) {
        return ""
    }
    cmd := m.commands[m.currentCmd]

    steps := []struct {
        label  string
        status types.CommandStatus
    }{
        {"Plan", types.StatusPlanning},
        {"Run", types.StatusRunning},
        {"Verify", types.StatusVerifying},
        {"Fix", types.StatusFixing},
        {"Docs", types.StatusDocumenting},
        {"Commit", types.StatusCommitting},
    }

    var parts []string
    for _, step := range steps {
        label := step.label

        // Skip Fix if no verify command (can't fix without verification)
        if step.status == types.StatusFixing && cmd.Verify == "" {
            continue
        }
        // Skip Verify if no verify command
        if step.status == types.StatusVerifying && cmd.Verify == "" {
            continue
        }

        if step.status == cmd.Status {
            // Active step: highlighted
            parts = append(parts, statusStyleFor(cmd.Status).Bold(true).Render(label))
        } else if isStatusBefore(step.status, cmd.Status) {
            // Completed step: dimmed green
            parts = append(parts, statusSuccess.Render(label))
        } else {
            // Future step: gray
            parts = append(parts, helpStyle.Render(label))
        }
    }

    return "  " + strings.Join(parts, helpStyle.Render(" → ")) + "\n"
}
```

**New helper `isStatusBefore()`:**
```go
// isStatusBefore returns true if step a comes before step b in the execution flow.
// The flow order is: Planning → Running → Verifying → Fixing → Documenting → Committing → Success
func isStatusBefore(a, b types.CommandStatus) bool {
    order := map[types.CommandStatus]int{
        types.StatusPlanning:    1,
        types.StatusRunning:     2,
        types.StatusVerifying:   3,
        types.StatusFixing:      4,
        types.StatusDocumenting: 5,
        types.StatusCommitting:  6,
        types.StatusSuccess:     7,
    }
    return order[a] < order[b]
}
```

**Edge cases:**
- `StatusFixing` → `StatusVerifying` loop: When re-verifying after a fix, the status goes back to `StatusVerifying`. The breadcrumb will simply highlight "Verify" again. The "Fix" step before it will appear as completed. This is correct behavior.
- No verify command: Both "Verify" and "Fix" steps are omitted from the breadcrumb.
- `StatusFailed`: Not in the breadcrumb (it's a terminal state shown separately).
- `StatusPending`: Not shown (hasn't started).

This satisfies requirement 4.

---

## Change 4: Enhanced failure panel

**File:** `internal/tui/model.go`
**Function:** `viewFailurePanel()` (lines 667-775)

**Replace the entire function body** with an enhanced version:

### Changes to the info fields section (lines 681-695):

**Current:**
```go
prompt := truncate(cmd.Prompt, 80)
b.WriteString(fmt.Sprintf("  Command #%d:  %q\n", m.failedCmdIndex+1, prompt))
b.WriteString(fmt.Sprintf("  Attempts:    %d / %d\n", cmd.Attempts, cmd.MaxRetries))
```

**Replace with:**
```go
prompt := truncate(cmd.Prompt, 80)
b.WriteString(fmt.Sprintf("  Command #%d:  %q\n", m.failedCmdIndex+1, prompt))

totalAttempts := len(cmd.AttemptLogs)
fixCount := cmd.FixAttempts
if fixCount > 0 {
    b.WriteString(statusFailed.Render(fmt.Sprintf("  Command failed after %d auto-fix attempt(s)", fixCount)))
    b.WriteString("\n")
}
b.WriteString(fmt.Sprintf("  Total attempts: %d  (1 initial + %d fix)\n", totalAttempts, fixCount))
```

### Add per-attempt summary section (after the info fields, before the scrollable content):

Insert a new section between the info fields and the scrollable content area:

```go
// Per-attempt summary
if len(cmd.AttemptLogs) > 1 {
    b.WriteString("\n")
    b.WriteString(helpStyle.Render("  Attempt history:"))
    b.WriteString("\n")
    for _, attempt := range cmd.AttemptLogs {
        var stepLabel string
        if attempt.FailedStep == "" {
            stepLabel = statusSuccess.Render("success")
        } else if attempt.FailedStep == "auto-fix" {
            // For auto-fix attempts, show what error persisted
            errSummary := ""
            if attempt.Stderr != "" {
                // First non-empty line of stderr as summary
                for _, line := range strings.Split(attempt.Stderr, "\n") {
                    trimmed := strings.TrimSpace(line)
                    if trimmed != "" {
                        errSummary = truncate(trimmed, 60)
                        break
                    }
                }
            }
            if errSummary != "" {
                stepLabel = statusFailed.Render(fmt.Sprintf("fix failed: %s", errSummary))
            } else {
                stepLabel = statusFailed.Render(fmt.Sprintf("fix failed (exit %d)", attempt.ExitCode))
            }
        } else {
            stepLabel = statusFailed.Render(fmt.Sprintf("%s failed (exit %d)", attempt.FailedStep, attempt.ExitCode))
        }

        duration := attempt.Duration.Round(time.Millisecond)
        b.WriteString(fmt.Sprintf("    #%d  %s  %s\n", attempt.AttemptNumber, duration, stepLabel))
    }
}
```

### Keep the existing scrollable content section (compact/expanded toggle) unchanged.

The final stderr is already shown in the compact mode (last attempt's stderr). The expanded mode shows the full failure report. This satisfies requirement 5's "final stderr output" requirement.

### Update `failureViewportHeight()` (line 657-665) to account for additional lines:

**Current:**
```go
func (m Model) failureViewportHeight() int {
    reserved := 2 + 4 + len(m.commands) + 8 + 2
    ...
}
```

**Change to account for attempt history:**
```go
func (m Model) failureViewportHeight() int {
    attemptHistoryLines := 0
    if m.failedCmdIndex >= 0 && m.failedCmdIndex < len(m.commands) {
        cmd := m.commands[m.failedCmdIndex]
        if len(cmd.AttemptLogs) > 1 {
            attemptHistoryLines = len(cmd.AttemptLogs) + 2 // header + entries + blank line
        }
    }
    // title(2) + summary header(5) + attempt history + command list + failure panel chrome(8) + help bar(2)
    reserved := 2 + 5 + attemptHistoryLines + len(m.commands) + 8 + 2
    h := m.height - reserved
    if h < 3 {
        h = 3
    }
    return h
}
```

**Edge cases:**
- 0 `AttemptLogs`: Shows "No attempt data available" (existing behavior preserved)
- 1 `AttemptLog` (initial attempt only, no fix attempts): Skip the per-attempt summary section (only useful when > 1 attempt)
- `FixAttempts = 0`: Shows "Total attempts: 1 (1 initial + 0 fix)" — correct but slightly verbose. Acceptable.
- Long stderr in attempt summary: Truncated to 60 chars via `truncate()`
- Attempt with empty stderr: Falls back to "fix failed (exit N)"

This satisfies requirement 5.

---

## Change 5: No changes needed for StatusRetrying removal from TUI

Looking at the actual codebase, **`StatusRetrying` is NOT referenced anywhere in `internal/tui/model.go`**. The TUI already uses `StatusFixing` everywhere:

- `statusStyleFor()` line 1099: has `case types.StatusFixing:` — no `StatusRetrying` case
- `statusIcon()` line 1296: has `case types.StatusFixing:` — no `StatusRetrying` case
- Status update handler line 369: uses `types.StatusFixing` — no `StatusRetrying`
- Resume handler line 289: resets `FixAttempts` — no reference to `StatusRetrying`

**The `StatusRetrying` constant in `types.go` is kept for backward compat** (iota stability + session deserialization). No TUI changes needed for requirement 1 — it's already done.

---

## Change 6: Update String() display for StatusFixing

**File:** `internal/types/types.go`
**Function:** `String()` (lines 26-43)

No change needed. The label is already `"Fixing"`. The TUI renders it via `styledStatus()` which calls `s.String()`. The `statusDetail` field already carries `"Fix N/M"` from the runner. Together they display as: `Fixing  Fix 1/3`.

However, to improve clarity per requirement 2 ("display text 'Fixing (attempt N/M)'"), we could optionally make the TUI combine them. But the current approach (status label + separate detail) is already working and matches the existing pattern for all statuses. **No change needed** — the runner's `statusDetail = "Fix N/M"` plus the enhanced live view (Change 2) provides all the required information.

---

## Summary of all changes

| # | File | Location | What changes |
|---|------|----------|-------------|
| 1 | `internal/tui/model.go` | `statusIcon()` L1296 | Change `StatusFixing` icon from `↻` to `⚡` |
| 2 | `internal/tui/model.go` | `viewRunningLive()` L1132-1146 | Add fixing context block: what failed, condensed stderr (last 10 lines), fix attempt count |
| 2a | `internal/tui/model.go` | After `truncate()` | Add `capitalize()`, `lastNLines()` helpers |
| 3 | `internal/tui/model.go` | `viewRunningLive()` after L1129 | Add status flow breadcrumb line |
| 3a | `internal/tui/model.go` | New functions | Add `renderStatusFlow()`, `isStatusBefore()` |
| 4 | `internal/tui/model.go` | `viewFailurePanel()` L681-695 | Replace attempts display with "failed after N auto-fix attempts" message |
| 4a | `internal/tui/model.go` | `viewFailurePanel()` after info fields | Add per-attempt summary section |
| 4b | `internal/tui/model.go` | `failureViewportHeight()` L657-665 | Account for attempt history lines in height calculation |

**No changes to `internal/types/types.go` or `internal/runner/runner.go`.**

---

## New helper functions to add to `internal/tui/model.go`

1. `capitalize(s string) string` — Uppercases first letter
2. `lastNLines(s string, n int) string` — Returns last N lines of a string
3. `renderStatusFlow() string` — Renders the status breadcrumb (method on Model)
4. `isStatusBefore(a, b types.CommandStatus) bool` — Compares execution flow order

---

## Testing

Run `go build -o autoclaude .` and `go test ./...` to verify compilation and existing tests pass. The TUI changes are visual and best verified by running the application.
