# Plan: Separate stdout/stderr capture in runner.go

## Overview

Refactor the output capture in `runCommandStreaming` (`internal/runner/runner.go:294`) to collect stdout and stderr into separate `bytes.Buffer` variables and extract a structured exit code. Both `runClaude` and `runVerify` delegate to this single function, so changing it covers all `exec.Cmd` usage. No changes to overall execution flow.

---

## File to Modify

**`internal/runner/runner.go`** — this is the only file that needs changes.

---

## Step-by-step Changes

### Step 1: Add `"bytes"` to the import block (lines 3–16)

Add `"bytes"` to the standard library import group. It goes alphabetically between `"bufio"` and `"context"`.

### Step 2: Define a `CommandResult` struct (after existing message types, ~line 38)

Add a new struct after `OutputLineMsg`:

```go
type CommandResult struct {
    Stdout   string
    Stderr   string
    ExitCode int
}
```

- `Stdout` — captured stdout text only
- `Stderr` — captured stderr text only
- `ExitCode` — 0 on success, actual exit code from `*exec.ExitError` on process failure, -1 for non-exit errors (command not found, pipe failures, context cancellation)

### Step 3: Change `runCommandStreaming` signature (line 294)

From:
```go
func (r *Runner) runCommandStreaming(cmdIndex int, name string, args ...string) (string, error)
```
To:
```go
func (r *Runner) runCommandStreaming(cmdIndex int, name string, args ...string) (string, CommandResult, error)
```

Three returns:
1. `string` — interleaved stdout+stderr combined output (identical to current behavior, preserves chronological line ordering for the TUI)
2. `CommandResult` — separated stdout, stderr, and exit code for future structured use
3. `error` — same as current

### Step 4: Refactor `runCommandStreaming` body

#### 4a: Add two `bytes.Buffer` variables (after pipe creation, before `cmd.Start()`)

```go
var stdoutBuf, stderrBuf bytes.Buffer
```

#### 4b: Wrap pipes with `io.TeeReader` (before passing to scanners)

Replace the direct pipe usage with tee readers so that raw bytes are captured in the buffers while still being scanned line-by-line for the TUI:

```go
stdoutReader := io.TeeReader(stdoutPipe, &stdoutBuf)
stderrReader := io.TeeReader(stderrPipe, &stderrBuf)
```

Then in the two `readPipe` goroutine calls, pass `stdoutReader` and `stderrReader` respectively instead of `stdoutPipe` and `stderrPipe`.

The existing `readPipe` closure, `lines` slice, mutex, and `OutputLineMsg` streaming all remain exactly as-is. The only difference is the pipe data now flows through the tee reader on its way to the scanner.

#### 4c: Extract exit code after `cmd.Wait()` (after line 338)

After the existing `err = cmd.Wait()`:

```go
exitCode := 0
if err != nil {
    if exitErr, ok := err.(*exec.ExitError); ok {
        exitCode = exitErr.ExitCode()
    } else {
        exitCode = -1
    }
}
```

#### 4d: Update the return statement (currently line 344)

From:
```go
return output, err
```
To:
```go
return output, CommandResult{
    Stdout:   stdoutBuf.String(),
    Stderr:   stderrBuf.String(),
    ExitCode: exitCode,
}, err
```

#### 4e: Update early-return error paths

There are three early returns that currently return `("", error)`. Each must be updated to return the three-value signature:

- **Line 300** (stdout pipe error): `return "", CommandResult{ExitCode: -1}, fmt.Errorf("stdout pipe: %w", err)`
- **Line 303** (stderr pipe error): `return "", CommandResult{ExitCode: -1}, fmt.Errorf("stderr pipe: %w", err)`
- **Line 308** (start error): `return "", CommandResult{ExitCode: -1}, fmt.Errorf("start: %w", err)`

### Step 5: Update `runClaude` (lines 347–349)

From:
```go
func (r *Runner) runClaude(cmdIndex int, prompt string) (string, error) {
    return r.runCommandStreaming(cmdIndex, "claude", "--dangerously-skip-permissions", "-p", prompt)
}
```
To:
```go
func (r *Runner) runClaude(cmdIndex int, prompt string) (string, error) {
    output, _, err := r.runCommandStreaming(cmdIndex, "claude", "--dangerously-skip-permissions", "-p", prompt)
    return output, err
}
```

The `CommandResult` is discarded with `_` since we're not changing execution flow yet. The `(string, error)` return signature of `runClaude` is preserved, so all callers in `executeSingle` remain untouched.

### Step 6: Update `runVerify` (lines 351–353)

From:
```go
func (r *Runner) runVerify(cmdIndex int, verifyCmd string) (string, error) {
    return r.runCommandStreaming(cmdIndex, "sh", "-c", verifyCmd)
}
```
To:
```go
func (r *Runner) runVerify(cmdIndex int, verifyCmd string) (string, error) {
    output, _, err := r.runCommandStreaming(cmdIndex, "sh", "-c", verifyCmd)
    return output, err
}
```

Same treatment — `CommandResult` discarded for now.

---

## Edge Cases

1. **Command not found / exec failure**: `cmd.Start()` returns error before any output. Return empty buffers and `ExitCode: -1`.

2. **Signal-killed process (SIGKILL, SIGTERM)**: `*exec.ExitError` will be present. `ExitCode()` returns -1 on some platforms for signal death — this is correct and expected.

3. **Context cancellation** (`r.ctx` cancelled): The command is killed. `cmd.Wait()` returns an error. If it's an `*exec.ExitError`, use its exit code; otherwise -1.

4. **Empty stdout or stderr**: Buffers will be empty strings. No special handling needed.

5. **Very large output**: `bytes.Buffer` grows dynamically, same memory characteristics as the existing `[]string` approach. No regression.

6. **TeeReader + Scanner interaction**: `io.TeeReader` writes bytes to the buffer as they're read by the scanner. Since the scanner reads until the pipe closes (process exits), all bytes will be captured. No data loss.

---

## What Does NOT Change

- `executeSingle` method and its retry/flow logic — completely untouched
- `runClaude` and `runVerify` public signatures — still return `(string, error)`
- All callers of `runClaude`/`runVerify` in `executeSingle` — untouched
- TUI message types (`StatusUpdateMsg`, `OutputLineMsg`, etc.) — untouched
- Streaming behavior (lines sent to TUI via `OutputLineMsg`) — untouched
- `types.Command` struct — untouched
- Session persistence — untouched
- No new files created
- No new dependencies (only `"bytes"` added to imports, which is stdlib)
