package tmux

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"agentmux/internal/resolve"
)

const (
	envTmuxBinary  = "AGENTMUX_TMUX_BIN"
	envTmuxSocket  = "AGENTMUX_TMUX_SOCKET"
	paneNameOption = "@agentmux_name"
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

type paneGeometry struct {
	ID   string
	Top  int
	Left int
}

type PaneStatus struct {
	WindowIndex  int
	WindowName   string
	WindowID     string
	WindowActive bool
	PaneIndex    int
	PaneID       string
	PaneActive   bool
	Name         string
	Title        string
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

		if window.Layout != "" && !(window.GridRows > 0 && window.GridColumns > 0) {
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

func Status(session string) ([]PaneStatus, error) {
	out, err := capture(false, "list-panes", "-t", session, "-F", "#{window_index}\t#{window_name}\t#{window_id}\t#{window_active}\t#{pane_index}\t#{pane_id}\t#{pane_active}\t#{"+paneNameOption+"}\t#{pane_title}")
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(out) == "" {
		return nil, nil
	}

	lines := strings.Split(strings.TrimSpace(out), "\n")
	panes := make([]PaneStatus, 0, len(lines))
	for _, line := range lines {
		fields := strings.Split(line, "\t")
		if len(fields) != 9 {
			return nil, fmt.Errorf("unexpected pane status output %q", line)
		}

		windowIndex, err := strconv.Atoi(fields[0])
		if err != nil {
			return nil, fmt.Errorf("parse window index %q: %w", fields[0], err)
		}
		paneIndex, err := strconv.Atoi(fields[4])
		if err != nil {
			return nil, fmt.Errorf("parse pane index %q: %w", fields[4], err)
		}

		panes = append(panes, PaneStatus{
			WindowIndex:  windowIndex,
			WindowName:   fields[1],
			WindowID:     fields[2],
			WindowActive: fields[3] == "1",
			PaneIndex:    paneIndex,
			PaneID:       fields[5],
			PaneActive:   fields[6] == "1",
			Name:         fields[7],
			Title:        fields[8],
		})
	}

	sort.Slice(panes, func(i, j int) bool {
		if panes[i].WindowIndex == panes[j].WindowIndex {
			if panes[i].PaneIndex == panes[j].PaneIndex {
				return panes[i].PaneID < panes[j].PaneID
			}
			return panes[i].PaneIndex < panes[j].PaneIndex
		}
		return panes[i].WindowIndex < panes[j].WindowIndex
	})

	return panes, nil
}

func FocusPaneByName(session string, name string, verbose bool) error {
	panes, err := Status(session)
	if err != nil {
		return err
	}

	var target *PaneStatus
	available := make([]string, 0, len(panes))
	for i := range panes {
		if panes[i].Name != "" {
			available = append(available, panes[i].Name)
		}
		if panes[i].Name == name {
			target = &panes[i]
		}
	}

	if target == nil {
		sort.Strings(available)
		if len(available) == 0 {
			return fmt.Errorf("pane %q not found in session %q; session has no named panes", name, session)
		}
		return fmt.Errorf("pane %q not found in session %q; available named panes: %s", name, session, strings.Join(available, ", "))
	}

	if err := run(verbose, "select-window", "-t", target.WindowID); err != nil {
		return fmt.Errorf("focus window for pane %q: %w", name, err)
	}
	if err := run(verbose, "select-pane", "-t", target.PaneID); err != nil {
		return fmt.Errorf("focus pane %q: %w", name, err)
	}
	return nil
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
	if window.GridRows > 0 && window.GridColumns > 0 {
		return createGridWindow(session, first, window, verbose)
	}
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
		args = append(args, buildPaneCommand(pane))

		out, err := capture(verbose, args...)
		if err != nil {
			return createdWindow{}, fmt.Errorf("create pane %d in window %q: %w", paneIndex+1, window.Name, err)
		}
		created.Panes = append(created.Panes, strings.TrimSpace(out))
	}

	return created, nil
}

func createGridWindow(session string, first bool, window resolve.Window, verbose bool) (createdWindow, error) {
	if len(window.Panes) == 0 {
		return createdWindow{}, fmt.Errorf("window %q has no panes", window.Name)
	}

	rows := window.GridRows
	cols := window.GridColumns
	if rows <= 0 || cols <= 0 {
		return createdWindow{}, fmt.Errorf("window %q grid layout requires positive dimensions", window.Name)
	}
	if len(window.Panes) != rows*cols {
		return createdWindow{}, fmt.Errorf("window %q grid %dx%d requires exactly %d panes, got %d", window.Name, rows, cols, rows*cols, len(window.Panes))
	}

	windowID, firstPaneID, err := createFirstPane(session, first, window, window.Panes[0], verbose)
	if err != nil {
		return createdWindow{}, err
	}

	rowAnchor := firstPaneID
	for row := 1; row < rows; row++ {
		pane := window.Panes[row*cols]
		if _, err := splitPane(rowAnchor, pane, window.Root, true, verbose); err != nil {
			return createdWindow{}, fmt.Errorf("create row %d in window %q: %w", row+1, window.Name, err)
		}
	}
	if rows > 1 {
		if err := run(verbose, "select-layout", "-E", "-t", rowAnchor); err != nil {
			return createdWindow{}, fmt.Errorf("equalize rows in window %q: %w", window.Name, err)
		}
	}

	rowPanes, err := listPaneGeometry(windowID, verbose)
	if err != nil {
		return createdWindow{}, fmt.Errorf("inspect grid rows for window %q: %w", window.Name, err)
	}
	if len(rowPanes) != rows {
		return createdWindow{}, fmt.Errorf("window %q expected %d row anchors, got %d", window.Name, rows, len(rowPanes))
	}

	for row := 0; row < rows; row++ {
		anchor := rowPanes[row].ID
		for col := 1; col < cols; col++ {
			pane := window.Panes[row*cols+col]
			if _, err := splitPane(anchor, pane, window.Root, false, verbose); err != nil {
				return createdWindow{}, fmt.Errorf("create column %d in row %d for window %q: %w", col+1, row+1, window.Name, err)
			}
		}
		if cols > 1 {
			if err := run(verbose, "select-layout", "-E", "-t", anchor); err != nil {
				return createdWindow{}, fmt.Errorf("equalize row %d in window %q: %w", row+1, window.Name, err)
			}
		}
	}

	ordered, err := listPaneGeometry(windowID, verbose)
	if err != nil {
		return createdWindow{}, fmt.Errorf("inspect final grid for window %q: %w", window.Name, err)
	}

	created := createdWindow{
		ID:    windowID,
		Panes: make([]string, 0, len(ordered)),
	}
	for _, pane := range ordered {
		created.Panes = append(created.Panes, pane.ID)
	}
	return created, nil
}

func createFirstPane(session string, first bool, window resolve.Window, pane resolve.Pane, verbose bool) (string, string, error) {
	args := []string{}
	if first {
		args = append(args, "new-session", "-d", "-s", session, "-n", window.Name, "-P", "-F", "#{window_id}:#{pane_id}")
	} else {
		args = append(args, "new-window", "-t", session, "-n", window.Name, "-P", "-F", "#{window_id}:#{pane_id}")
	}
	if pane.Cwd != "" {
		args = append(args, "-c", pane.Cwd)
	} else if window.Root != "" {
		args = append(args, "-c", window.Root)
	}
	args = append(args, buildPaneCommand(pane))

	out, err := capture(verbose, args...)
	if err != nil {
		return "", "", fmt.Errorf("create window %q: %w", window.Name, err)
	}
	return parseWindowAndPane(out)
}

func splitPane(targetPaneID string, pane resolve.Pane, windowRoot string, vertical bool, verbose bool) (string, error) {
	args := []string{"split-window"}
	if vertical {
		args = append(args, "-v")
	} else {
		args = append(args, "-h")
	}
	args = append(args, "-t", targetPaneID, "-P", "-F", "#{pane_id}")
	if pane.Cwd != "" {
		args = append(args, "-c", pane.Cwd)
	} else if windowRoot != "" {
		args = append(args, "-c", windowRoot)
	}
	args = append(args, buildPaneCommand(pane))

	out, err := capture(verbose, args...)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

func configurePane(paneID string, pane resolve.Pane, verbose bool) error {
	if pane.Name != "" {
		if err := run(verbose, "set-option", "-p", "-t", paneID, paneNameOption, pane.Name); err != nil {
			return fmt.Errorf("set name for pane %s: %w", paneID, err)
		}
	}
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

func listPaneGeometry(target string, verbose bool) ([]paneGeometry, error) {
	out, err := capture(verbose, "list-panes", "-t", target, "-F", "#{pane_id}\t#{pane_top}\t#{pane_left}")
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(out) == "" {
		return nil, nil
	}

	lines := strings.Split(strings.TrimSpace(out), "\n")
	panes := make([]paneGeometry, 0, len(lines))
	for _, line := range lines {
		fields := strings.Split(line, "\t")
		if len(fields) != 3 {
			return nil, fmt.Errorf("unexpected pane geometry output %q", line)
		}
		top, err := strconv.Atoi(fields[1])
		if err != nil {
			return nil, fmt.Errorf("parse pane top %q: %w", fields[1], err)
		}
		left, err := strconv.Atoi(fields[2])
		if err != nil {
			return nil, fmt.Errorf("parse pane left %q: %w", fields[2], err)
		}
		panes = append(panes, paneGeometry{
			ID:   fields[0],
			Top:  top,
			Left: left,
		})
	}

	sort.Slice(panes, func(i, j int) bool {
		if panes[i].Top == panes[j].Top {
			if panes[i].Left == panes[j].Left {
				return panes[i].ID < panes[j].ID
			}
			return panes[i].Left < panes[j].Left
		}
		return panes[i].Top < panes[j].Top
	})
	return panes, nil
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
