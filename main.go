package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/manasm11/autoclaude/internal/config"
	"github.com/manasm11/autoclaude/internal/runner"
	"github.com/manasm11/autoclaude/internal/session"
	"github.com/manasm11/autoclaude/internal/tui"
	"github.com/manasm11/autoclaude/internal/types"

	tea "github.com/charmbracelet/bubbletea"
)

// stringSlice implements flag.Value to collect repeated -c flags.
type stringSlice []string

func (s *stringSlice) String() string {
	return strings.Join(*s, ", ")
}

func (s *stringSlice) Set(val string) error {
	*s = append(*s, val)
	return nil
}

func usage() {
	fmt.Fprintf(os.Stderr, `autoclaude — batch runner for Claude Code prompts

Usage:
  autoclaude [flags]

Flags:
  -f, --file string       Path to a TOML config file
  -c, --cmd string        A prompt to run (repeatable). Format: "prompt" or "prompt::verify"
  -r, --max-retries int   Maximum retries per command (default 3)
  -w, --work-dir string   Working directory (default: current directory)
  -a, --auto-run          Skip TUI queue review and start execution immediately
  -R, --no-resume         Skip session detection and start fresh (clears any existing session)
      --clear-session     Delete any existing session file and exit
  -h, --help              Show this help message

Examples:
  autoclaude -f commands.toml
  autoclaude -c "Add error handling to the API layer" -c "Write unit tests for API::go test ./..."
  autoclaude -f base.toml -c "One more fix::go build ./..."
  autoclaude -f commands.toml --auto-run
  autoclaude --clear-session
  autoclaude
`)
}

func main() {
	// Define flag variables
	var (
		configFile   string
		cmds         stringSlice
		maxRetries   int
		workDir      string
		autoRun      bool
		noResume     bool
		clearSession bool
		showHelp     bool
	)

	// Long flags
	flag.StringVar(&configFile, "file", "", "path to a TOML config file")
	flag.Var(&cmds, "cmd", `a prompt to run (repeatable). Format: "prompt" or "prompt::verify"`)
	flag.IntVar(&maxRetries, "max-retries", 3, "maximum number of retries per command")
	flag.StringVar(&workDir, "work-dir", "", "working directory for command execution (defaults to current directory)")
	flag.BoolVar(&autoRun, "auto-run", false, "skip TUI queue review and start execution immediately")
	flag.BoolVar(&noResume, "no-resume", false, "skip session detection and start fresh")
	flag.BoolVar(&clearSession, "clear-session", false, "delete any existing session file and exit")
	flag.BoolVar(&showHelp, "help", false, "show usage with examples")

	// Short flag aliases
	flag.StringVar(&configFile, "f", "", "path to a TOML config file (shorthand)")
	flag.Var(&cmds, "c", `a prompt to run (shorthand)`)
	flag.IntVar(&maxRetries, "r", 3, "maximum retries per command (shorthand)")
	flag.StringVar(&workDir, "w", "", "working directory (shorthand)")
	flag.BoolVar(&autoRun, "a", false, "skip TUI queue review (shorthand)")
	flag.BoolVar(&noResume, "R", false, "skip session detection (shorthand)")
	flag.BoolVar(&showHelp, "h", false, "show help (shorthand)")

	flag.Usage = usage
	flag.Parse()

	if showHelp {
		usage()
		os.Exit(0)
	}

	// Resolve working directory
	var wd string
	var err error
	if workDir != "" {
		wd, err = filepath.Abs(workDir)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error resolving working directory: %v\n", err)
			os.Exit(1)
		}
	} else {
		wd, err = os.Getwd()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error getting working directory: %v\n", err)
			os.Exit(1)
		}
	}

	if _, err := os.Stat(wd); err != nil {
		fmt.Fprintf(os.Stderr, "Error: working directory is not accessible: %v\n", err)
		os.Exit(1)
	}

	// --clear-session: delete session file and exit
	if clearSession {
		session.Clear(wd)
		if session.Exists(wd) {
			fmt.Fprintln(os.Stderr, "Error: failed to clear session file")
			os.Exit(1)
		}
		fmt.Println("Session file cleared.")
		os.Exit(0)
	}

	if _, err := exec.LookPath("claude"); err != nil {
		fmt.Fprintln(os.Stderr, "Error: 'claude' CLI not found in PATH.")
		fmt.Fprintln(os.Stderr, "Install Claude Code: https://docs.anthropic.com/en/docs/claude-code")
		os.Exit(1)
	}

	// Session resume detection (before loading TOML/CLI commands)
	var resumeSession *session.SessionState
	if noResume {
		// --no-resume: clear any existing session and skip detection
		session.Clear(wd)
	} else if session.Exists(wd) {
		sess, err := session.Load(wd)
		if err != nil {
			if errors.Is(err, session.ErrCorrupted) {
				fmt.Fprintln(os.Stderr, "Warning: session file corrupted, starting fresh")
			} else {
				fmt.Fprintf(os.Stderr, "Warning: failed to load session file: %v\n", err)
			}
			session.Clear(wd)
		} else if session.AllSucceeded(sess) {
			// All commands already succeeded — nothing to resume
			session.Clear(wd)
		} else if sess.WorkDir != wd {
			fmt.Fprintf(os.Stderr, "Warning: session was created in %s but current directory is %s\n", sess.WorkDir, wd)
			fmt.Fprint(os.Stderr, "Resume anyway? [y/N] ")
			var answer string
			fmt.Scanln(&answer)
			answer = strings.TrimSpace(strings.ToLower(answer))
			if answer == "y" || answer == "yes" {
				resumeSession = sess
			} else {
				session.Clear(wd)
			}
		} else {
			resumeSession = sess
		}
	}

	// Build commands from file and -c flags
	var commands []*types.Command
	var fileCount, cliCount int
	var configSource string // name of the config file loaded (for TUI display)
	var autoDetected bool   // whether the config file was auto-detected

	// 1. Auto-detect config file if --file not provided
	if configFile == "" {
		if detected := config.DetectConfigFile(wd); detected != "" {
			configFile = detected
			autoDetected = true
		}
	}

	// 2. Load from TOML config file
	if configFile != "" {
		absPath, pathErr := filepath.Abs(configFile)
		if pathErr != nil {
			absPath = configFile
		}
		if _, statErr := os.Stat(configFile); os.IsNotExist(statErr) {
			fmt.Fprintf(os.Stderr, "Error: config file not found: %s\n", absPath)
			os.Exit(1)
		}
		cfg, err := config.LoadConfig(configFile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error loading config file: %v\n", err)
			os.Exit(1)
		}
		configSource = filepath.Base(configFile)
		// Use config work_dir if no --work-dir flag was given
		if workDir == "" && cfg.WorkDir != "" {
			wd = cfg.WorkDir
		}
		// Use config max_retries if it was explicitly set and flag is at default
		if cfg.MaxRetries != 0 && maxRetries == 3 {
			maxRetries = cfg.MaxRetries
		}
		commands = append(commands, cfg.ToCommands()...)
		fileCount = len(cfg.Commands)
	}

	// 3. Append -c flag commands after file commands
	for _, raw := range cmds {
		prompt, verify := parseCmdFlag(raw)
		if prompt == "" {
			fmt.Fprintf(os.Stderr, "Error: empty prompt in -c flag: %q\n", raw)
			os.Exit(1)
		}
		cmd := types.NewCommand(prompt)
		cmd.Verify = verify
		cmd.MaxRetries = maxRetries
		commands = append(commands, cmd)
		cliCount++
	}

	// 4. Apply global --max-retries to any command without its own override
	for _, cmd := range commands {
		if cmd.MaxRetries <= 0 {
			cmd.MaxRetries = maxRetries
		}
	}

	// 5. Validate --auto-run requires commands
	if autoRun && len(commands) == 0 {
		fmt.Fprintln(os.Stderr, "Error: --auto-run requires commands via --file or --cmd")
		os.Exit(1)
	}

	r := runner.NewRunner(wd)
	r.MaxRetries = maxRetries

	// Add all pre-loaded commands to the runner
	for _, cmd := range commands {
		r.AddCommand(cmd)
	}

	model := tui.NewModel(r)

	// If a previous session was detected, configure resume
	if resumeSession != nil {
		if autoRun {
			// Auto-resume: skip TUI prompt, log to stdout, and start from where we left off
			resumeIndex := len(resumeSession.Commands)
			for i, sc := range resumeSession.Commands {
				if types.ParseCommandStatus(sc.Status) != types.StatusSuccess {
					resumeIndex = i
					break
				}
			}
			fmt.Printf("Resuming previous session from command %d/%d\n", resumeIndex+1, len(resumeSession.Commands))
			model.SetAutoResume(resumeSession, resumeIndex)
		} else {
			model.SetResumeSession(resumeSession)
		}
	}

	// If commands were loaded, sync them into the TUI model
	if len(commands) > 0 {
		model.SetCommands(commands)

		// Build startup status message showing source breakdown
		if fileCount > 0 && cliCount > 0 {
			model.SetStatusMsg(fmt.Sprintf("Loaded %d commands from config + %d from CLI flags", fileCount, cliCount))
		} else if fileCount > 0 {
			model.SetStatusMsg(fmt.Sprintf("Loaded %d commands from config", fileCount))
		} else if cliCount > 0 {
			model.SetStatusMsg(fmt.Sprintf("Loaded %d commands from CLI flags", cliCount))
		}
	}

	// If commands were auto-loaded from a detected config file, tell the TUI
	if autoDetected && fileCount > 0 {
		model.SetAutoLoadInfo(fileCount, configSource)
	}

	// If auto-run is set and we have commands, start in running state
	if autoRun && len(commands) > 0 {
		model.SetAutoRun()
	}

	p := tea.NewProgram(model, tea.WithAltScreen())
	r.SetProgram(p)

	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

// parseCmdFlag splits a -c flag value on "::" into prompt and optional verify command.
func parseCmdFlag(raw string) (prompt, verify string) {
	parts := strings.SplitN(raw, "::", 2)
	prompt = strings.TrimSpace(parts[0])
	if len(parts) == 2 {
		verify = strings.TrimSpace(parts[1])
	}
	return
}
