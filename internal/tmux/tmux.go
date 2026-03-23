package tmux

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strings"

	"agentmux/internal/resolve"
)

const (
	envTmuxBinary = "AGENTMUX_TMUX_BIN"
	envTmuxSocket = "AGENTMUX_TMUX_SOCKET"
)

var shellEnvRefPattern = regexp.MustCompile(`(^|[^\\])\$(\{[A-Za-z_][A-Za-z0-9_]*\}|[A-Za-z_][A-Za-z0-9_]*)`)

type Options struct {
	Attach  bool
	Replace bool
	Verbose bool
}

type createdWindow struct {
	ID    string
	Panes []string
}

func Execute(cfg resolve.Config, opts Options) error {
	exists, err := SessionExists(cfg.Session)
	if err != nil {
		return err
	}
	if exists {
		if !opts.Replace {
			return fmt.Errorf("tmux session %q already exists; rerun with --replace", cfg.Session)
		}
		if err := run(opts.Verbose, "kill-session", "-t", cfg.Session); err != nil {
			return fmt.Errorf("replace existing session %q: %w", cfg.Session, err)
		}
	}

	windows := make([]createdWindow, 0, len(cfg.Windows))
	focusWindowID := ""
	focusPaneID := ""

	for windowIndex, window := range cfg.Windows {
		created, err := createWindow(cfg.Session, windowIndex == 0, window, opts.Verbose)
		if err != nil {
			return err
		}
		windows = append(windows, created)

		if len(created.Panes) != len(window.Panes) {
			return errors.New("internal error: pane count mismatch")
		}

		for paneIndex, paneID := range created.Panes {
			pane := window.Panes[paneIndex]
			if err := configurePane(paneID, pane, opts.Verbose); err != nil {
				return err
			}
			if pane.Focus {
				focusWindowID = created.ID
				focusPaneID = paneID
			}
		}

		if window.Layout != "" {
			if err := run(opts.Verbose, "select-layout", "-t", created.ID, window.Layout); err != nil {
				return fmt.Errorf("apply layout %q to window %q: %w", window.Layout, window.Name, err)
			}
		}
	}

	if focusPaneID != "" {
		if err := run(opts.Verbose, "select-window", "-t", focusWindowID); err != nil {
			return fmt.Errorf("focus window: %w", err)
		}
		if err := run(opts.Verbose, "select-pane", "-t", focusPaneID); err != nil {
			return fmt.Errorf("focus pane: %w", err)
		}
	}

	if opts.Attach {
		return attachSession(cfg.Session, opts.Verbose)
	}

	return nil
}

func ListSessions() ([]string, error) {
	cmd := command("list-sessions", "-F", "#{session_name}")
	out, err := cmd.CombinedOutput()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 1 {
			if strings.Contains(string(out), "no server running") {
				return nil, nil
			}
		}
		return nil, err
	}

	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) == 1 && lines[0] == "" {
		return nil, nil
	}
	sort.Strings(lines)
	return lines, nil
}

func SessionExists(name string) (bool, error) {
	cmd := command("has-session", "-t", name)
	if err := cmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 1 {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func createWindow(session string, first bool, window resolve.Window, verbose bool) (createdWindow, error) {
	if len(window.Panes) == 0 {
		return createdWindow{}, fmt.Errorf("window %q has no panes", window.Name)
	}

	args := []string{}
	firstPane := window.Panes[0]
	if first {
		args = append(args, "new-session", "-d", "-s", session, "-n", window.Name, "-P", "-F", "#{window_id}:#{pane_id}")
	} else {
		args = append(args, "new-window", "-t", session, "-n", window.Name, "-P", "-F", "#{window_id}:#{pane_id}")
	}
	if firstPane.Cwd != "" {
		args = append(args, "-c", firstPane.Cwd)
	} else if window.Root != "" {
		args = append(args, "-c", window.Root)
	}
	args = append(args, buildPaneCommand(firstPane))

	out, err := capture(verbose, args...)
	if err != nil {
		return createdWindow{}, fmt.Errorf("create window %q: %w", window.Name, err)
	}

	windowID, firstPaneID, err := parseWindowAndPane(out)
	if err != nil {
		return createdWindow{}, fmt.Errorf("create window %q: %w", window.Name, err)
	}

	created := createdWindow{
		ID:    windowID,
		Panes: []string{firstPaneID},
	}

	for paneIndex := 1; paneIndex < len(window.Panes); paneIndex++ {
		pane := window.Panes[paneIndex]
		args := []string{"split-window", "-t", windowID, "-P", "-F", "#{pane_id}"}
		if pane.Cwd != "" {
			args = append(args, "-c", pane.Cwd)
		} else if window.Root != "" {
			args = append(args, "-c", window.Root)
		}

		out, err := capture(verbose, args...)
		if err != nil {
			return createdWindow{}, fmt.Errorf("create pane %d in window %q: %w", paneIndex+1, window.Name, err)
		}
		created.Panes = append(created.Panes, strings.TrimSpace(out))
	}

	return created, nil
}

func configurePane(paneID string, pane resolve.Pane, verbose bool) error {
	if pane.Title != "" {
		if err := run(verbose, "select-pane", "-t", paneID, "-T", pane.Title); err != nil {
			return fmt.Errorf("set title for pane %s: %w", paneID, err)
		}
	}

	return nil
}

func buildPaneCommand(pane resolve.Pane) string {
	parts := make([]string, 0, 3)
	if pane.Cwd != "" {
		parts = append(parts, "cd "+singleQuote(pane.Cwd))
	}
	if len(pane.Env) > 0 {
		keys := make([]string, 0, len(pane.Env))
		for key := range pane.Env {
			keys = append(keys, key)
		}
		sort.Strings(keys)

		assignments := make([]string, 0, len(keys))
		for _, key := range keys {
			assignments = append(assignments, key+"="+quoteEnvValue(pane.Env[key]))
		}
		parts = append(parts, "export "+strings.Join(assignments, " "))
	}

	command := pane.Command
	if pane.Shell != "" {
		command = singleQuote(pane.Shell) + " -lc " + singleQuote(pane.Command)
	}
	parts = append(parts, command)

	return strings.Join(parts, " && ") + "; exec " + singleQuote(interactiveShell())
}

func quoteEnvValue(value string) string {
	if shellEnvRefPattern.MatchString(value) {
		return `"` + escapeDouble(value) + `"`
	}
	return singleQuote(value)
}

func singleQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'"'"'`) + "'"
}

func escapeDouble(value string) string {
	replacer := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "`", "\\`")
	return replacer.Replace(value)
}

func parseWindowAndPane(value string) (string, string, error) {
	parts := strings.Split(strings.TrimSpace(value), ":")
	if len(parts) != 2 {
		return "", "", fmt.Errorf("unexpected tmux output %q", value)
	}
	return parts[0], parts[1], nil
}

func attachSession(name string, verbose bool) error {
	cmd := command("attach-session", "-t", name)
	if verbose {
		fmt.Fprintf(os.Stderr, "tmux %s\n", strings.Join(cmd.Args[1:], " "))
	}
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func run(verbose bool, args ...string) error {
	cmd := command(args...)
	if verbose {
		fmt.Fprintf(os.Stderr, "tmux %s\n", strings.Join(args, " "))
	}
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func capture(verbose bool, args ...string) (string, error) {
	cmd := command(args...)
	if verbose {
		fmt.Fprintf(os.Stderr, "tmux %s\n", strings.Join(args, " "))
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if stderr.Len() > 0 {
			return "", fmt.Errorf("%s", strings.TrimSpace(stderr.String()))
		}
		return "", err
	}
	return strings.TrimSpace(stdout.String()), nil
}

func command(args ...string) *exec.Cmd {
	commandArgs := make([]string, 0, len(args)+2)
	if socket := os.Getenv(envTmuxSocket); socket != "" {
		commandArgs = append(commandArgs, "-S", socket)
	}
	commandArgs = append(commandArgs, args...)
	return exec.Command(tmuxBinary(), commandArgs...)
}

func tmuxBinary() string {
	if binary := strings.TrimSpace(os.Getenv(envTmuxBinary)); binary != "" {
		return binary
	}
	return "tmux"
}

func interactiveShell() string {
	if shell := strings.TrimSpace(os.Getenv("SHELL")); shell != "" {
		return shell
	}
	return "/bin/sh"
}

func Binary() string {
	return tmuxBinary()
}

func Socket() string {
	return strings.TrimSpace(os.Getenv(envTmuxSocket))
}
