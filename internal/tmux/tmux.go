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
	"time"

	"agentmux/internal/config"
	"agentmux/internal/resolve"
)

const (
	envTmuxBinary       = "AGENTMUX_TMUX_BIN"
	envTmuxSocket       = "AGENTMUX_TMUX_SOCKET"
	paneNameOption      = "@agentmux_name"
	paneStartupOption   = "@agentmux_startup_command"
	paneSourceOption    = "@agentmux_source_type"
	paneAgentOption     = "@agentmux_agent_name"
	paneOnExitOption    = "@agentmux_on_exit"
	paneStartTimeOption = "@agentmux_start_time"
	sessionConfigPath   = "@agentmux_config_path"
	kvPrefix            = "@agentmux_kv_"
)

var (
	shellEnvRefPattern = regexp.MustCompile(`(^|[^\\])\$(\{[A-Za-z_][A-Za-z0-9_]*\}|[A-Za-z_][A-Za-z0-9_]*)`)
	kvKeyPattern       = regexp.MustCompile(`^[A-Za-z0-9_]+$`)
)

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
	WindowIndex  int     `json:"window_index"`
	WindowName   string  `json:"window_name"`
	WindowID     string  `json:"window_id"`
	WindowActive bool    `json:"window_active"`
	PaneIndex    int     `json:"pane_index"`
	PaneID       string  `json:"pane_id"`
	PaneActive   bool    `json:"pane_active"`
	Dead         bool    `json:"dead"`
	Name         string  `json:"name"`
	Title        string  `json:"title"`
	SourceType   string  `json:"source_type,omitempty"`
	AgentName    string  `json:"agent_name,omitempty"`
	OnExit       string  `json:"on_exit,omitempty"`
	CurrentCmd   string  `json:"current_command,omitempty"`
	StartupCmd   string  `json:"startup_command,omitempty"`
	ExitStatus   int     `json:"exit_status,omitempty"`
	Pid          int     `json:"pid,omitempty"`
	StartTime    string  `json:"start_time,omitempty"`
	CPU          float64 `json:"cpu_percent,omitempty"`
	Memory       float64 `json:"memory_percent,omitempty"`
}

type paneMetadata struct {
	Name           string
	StartupCommand string
	SourceType     string
	AgentName      string
	OnExit         string
	StartTime      string
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
		if err := ensureCanReplace(cfg.Session); err != nil {
			return err
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
	if err := ensureSessionExists(session); err != nil {
		return nil, err
	}

	out, err := captureRaw(false, "list-panes", "-a", "-F", "#{session_name}\t#{window_index}\t#{window_name}\t#{window_id}\t#{window_active}\t#{pane_index}\t#{pane_id}\t#{pane_active}\t#{pane_dead}\t#{"+paneNameOption+"}\t#{pane_title}\t#{"+paneSourceOption+"}\t#{"+paneAgentOption+"}\t#{"+paneOnExitOption+"}\t#{pane_current_command}\t#{"+paneStartupOption+"}\t#{pane_dead_status}\t#{pane_pid}\t#{"+paneStartTimeOption+"}")
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(out) == "" {
		return nil, nil
	}

	lines := strings.Split(strings.TrimSuffix(out, "\n"), "\n")
	panes := make([]PaneStatus, 0, len(lines))
	for _, line := range lines {
		fields := strings.Split(line, "\t")
		if len(fields) != 19 {
			return nil, fmt.Errorf("unexpected pane status output %q (fields=%d, want 19)", line, len(fields))
		}
		if fields[0] != session {
			continue
		}

		windowIndex, err := strconv.Atoi(fields[1])
		if err != nil {
			return nil, fmt.Errorf("parse window index %q: %w", fields[1], err)
		}
		paneIndex, err := strconv.Atoi(fields[5])
		if err != nil {
			return nil, fmt.Errorf("parse pane index %q: %w", fields[5], err)
		}
		exitStatus, _ := strconv.Atoi(fields[16])
		pid, _ := strconv.Atoi(fields[17])

		status := PaneStatus{
			WindowIndex:  windowIndex,
			WindowName:   fields[2],
			WindowID:     fields[3],
			WindowActive: fields[4] == "1",
			PaneIndex:    paneIndex,
			PaneID:       fields[6],
			PaneActive:   fields[7] == "1",
			Dead:         fields[8] == "1",
			Name:         unescapeOptionValue(fields[9]),
			Title:        fields[10],
			SourceType:   fields[11],
			AgentName:    fields[12],
			OnExit:       fields[13],
			CurrentCmd:   fields[14],
			StartupCmd:   unescapeOptionValue(fields[15]),
			ExitStatus:   exitStatus,
			Pid:          pid,
			StartTime:    fields[18],
		}

		if !status.Dead && status.Pid > 0 {
			if cpu, mem, err := getProcessMetrics(status.Pid); err == nil {
				status.CPU = cpu
				status.Memory = mem
			}
		}

		panes = append(panes, status)
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

func unescapeOptionValue(v string) string {
	v = strings.ReplaceAll(v, "\\n", "\n")
	v = strings.ReplaceAll(v, "\\t", "\t")
	return v
}

func getProcessMetrics(pid int) (float64, float64, error) {
	cmd := exec.Command("ps", "-o", "%cpu,%mem", "-p", strconv.Itoa(pid), "--no-headers")
	out, err := cmd.Output()
	if err != nil {
		return 0, 0, err
	}

	fields := strings.Fields(string(out))
	if len(fields) != 2 {
		return 0, 0, fmt.Errorf("unexpected ps output: %q", string(out))
	}

	cpu, err := strconv.ParseFloat(fields[0], 64)
	if err != nil {
		return 0, 0, err
	}

	mem, err := strconv.ParseFloat(fields[1], 64)
	if err != nil {
		return 0, 0, err
	}

	return cpu, mem, nil
}

func StatusPaneByName(session string, name string) (PaneStatus, error) {
	return findPaneByName(session, name)
}

func SetSessionConfigPath(session string, path string, verbose bool) error {
	if strings.TrimSpace(path) == "" {
		return nil
	}
	if err := run(verbose, "set-option", "-t", session, sessionConfigPath, path); err != nil {
		return fmt.Errorf("set config path for session %q: %w", session, err)
	}
	return nil
}

func SessionConfigPath(session string, verbose bool) (string, error) {
	if err := ensureSessionExists(session); err != nil {
		return "", err
	}
	value, err := capture(verbose, "show-options", "-qv", "-t", session, sessionConfigPath)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(value), nil
}

func FocusPaneByName(session string, name string, verbose bool) error {
	target, err := findPaneByName(session, name)
	if err != nil {
		return err
	}

	if err := run(verbose, "select-window", "-t", target.WindowID); err != nil {
		return fmt.Errorf("focus window for pane %q: %w", name, err)
	}
	if err := run(verbose, "select-pane", "-t", target.PaneID); err != nil {
		return fmt.Errorf("focus pane %q: %w", name, err)
	}
	return nil
}

func RestartPaneByName(session string, name string, verbose bool) error {
	return respawnPaneByName(session, name, verbose)
}

func RerunPaneByName(session string, name string, verbose bool) error {
	return respawnPaneByName(session, name, verbose)
}

func SendCommandToPaneByName(session string, name string, commandText string, pressEnter bool, verbose bool) error {
	target, err := findPaneByName(session, name)
	if err != nil {
		return err
	}

	if strings.TrimSpace(commandText) == "" {
		return fmt.Errorf("cannot send an empty command to pane %q", name)
	}

	if err := run(verbose, "send-keys", "-t", target.PaneID, "-l", commandText); err != nil {
		return fmt.Errorf("send command to pane %q: %w", name, err)
	}
	if pressEnter {
		if err := run(verbose, "send-keys", "-t", target.PaneID, "Enter"); err != nil {
			return fmt.Errorf("send enter to pane %q: %w", name, err)
		}
	}
	return nil
}

func StopPaneByName(session string, name string, verbose bool) error {
	target, err := findPaneByName(session, name)
	if err != nil {
		return err
	}

	onExit := target.OnExit
	if err := applyPaneLifecycle(target.PaneID, onExit, verbose); err != nil {
		return fmt.Errorf("apply on_exit for pane %q before stop: %w", name, err)
	}
	shell := interactiveShell()
	if err := run(verbose, "respawn-pane", "-k", "-t", target.PaneID, shell); err != nil {
		return fmt.Errorf("stop pane %q: %w", name, err)
	}
	if err := applyPaneMetadata(target.PaneID, paneMetadata{
		Name:           target.Name,
		StartupCommand: target.StartupCmd,
		SourceType:     target.SourceType,
		AgentName:      target.AgentName,
		OnExit:         config.NormalizeOnExit(onExit),
	}, verbose); err != nil {
		return fmt.Errorf("restore metadata for pane %q after stop: %w", name, err)
	}
	return nil
}

func ScaleAgent(session string, agentName string, targetCount int, preferredWindow string, cfg resolve.Config, opts Options) error {
	if targetCount < 0 {
		return fmt.Errorf("target count must be non-negative, got %d", targetCount)
	}

	panes, err := Status(session)
	if err != nil {
		return err
	}

	// 1. Find existing instances of the agent
	var instances []PaneStatus
	for _, p := range panes {
		if p.AgentName == agentName {
			instances = append(instances, p)
		}
	}

	currentCount := len(instances)
	if currentCount == targetCount {
		return nil
	}

	// 2. Identify the target agent and its intended layout
	var targetAgent *resolve.Agent
	if a, ok := cfg.Agents[agentName]; ok {
		targetAgent = &a
	} else {
		return fmt.Errorf("agent %q not found in configuration", agentName)
	}

	// Find the window and its layout
	var targetWindow resolve.Window
	foundWindow := false
	for _, w := range cfg.Windows {
		if preferredWindow != "" && w.Name == preferredWindow {
			targetWindow = w
			foundWindow = true
			break
		}
		// If no preferred window, find the first window that uses this agent
		for _, p := range w.Panes {
			if p.AgentName == agentName {
				targetWindow = w
				foundWindow = true
				break
			}
		}
		if foundWindow {
			break
		}
	}

	if !foundWindow {
		if preferredWindow != "" {
			return fmt.Errorf("window %q not found in configuration", preferredWindow)
		}
		// Fallback to first window
		if len(cfg.Windows) > 0 {
			targetWindow = cfg.Windows[0]
		} else {
			return fmt.Errorf("no windows found in configuration to scale agent %q", agentName)
		}
	}

	// Find the live window ID
	liveWindowID := ""
	if currentCount > 0 {
		liveWindowID = instances[0].WindowID
	} else {
		// Find window by name in the live session
		for _, p := range panes {
			if p.WindowName == targetWindow.Name {
				liveWindowID = p.WindowID
				break
			}
		}
	}

	if liveWindowID == "" {
		return fmt.Errorf("could not find live window for %q in session %q", targetWindow.Name, session)
	}

	// 3. Apply changes
	if targetCount > currentCount {
		// Scale Up
		diff := targetCount - currentCount
		for i := 0; i < diff; i++ {
			instanceNum := currentCount + i + 1
			name := agentName
			if instanceNum > 1 {
				name = fmt.Sprintf("%s-%d", agentName, instanceNum)
			}

			// Build a resolve.Pane from the agent
			pane := resolve.Pane{
				Name:       name,
				AgentName:  agentName,
				SourceType: "agent",
				Command:    targetAgent.Command,
				Cwd:        targetAgent.Cwd,
				Env:        targetAgent.Env,
				Title:      targetAgent.Title,
				OnExit:     targetAgent.OnExit,
				Shell:      targetAgent.Shell,
			}

			// Split window
			newPaneID, err := splitPane(liveWindowID, pane, targetWindow.Root, false, opts.Verbose)
			if err != nil {
				return fmt.Errorf("scale up: split pane: %w", err)
			}

			// Configure new pane
			if err := configurePane(newPaneID, pane, opts.Verbose); err != nil {
				return fmt.Errorf("scale up: configure pane %s: %w", newPaneID, err)
			}
		}
	} else {
		// Scale Down
		diff := currentCount - targetCount
		// Kill the highest IDs/Indices
		sort.Slice(instances, func(i, j int) bool {
			return instances[i].PaneIndex > instances[j].PaneIndex
		})

		for i := 0; i < diff; i++ {
			p := instances[i]
			if err := run(opts.Verbose, "kill-pane", "-t", p.PaneID); err != nil {
				return fmt.Errorf("scale down: kill pane %s: %w", p.PaneID, err)
			}
		}
	}

	// 4. Re-apply layout if specified
	if targetWindow.Layout != "" && targetWindow.Layout != "grid" {
		if err := run(opts.Verbose, "select-layout", "-t", liveWindowID, targetWindow.Layout); err != nil {
			if opts.Verbose {
				fmt.Fprintf(os.Stderr, "warning: scale re-layout failed: %v\n", err)
			}
		}
	}

	return nil
}

func KillPaneByName(session string, name string, verbose bool) error {
	target, err := findPaneByName(session, name)
	if err != nil {
		return err
	}

	if err := run(verbose, "kill-pane", "-t", target.PaneID); err != nil {
		return fmt.Errorf("kill pane %q: %w", name, err)
	}
	return nil
}

func SetKV(session string, key string, value string, verbose bool) error {
	if !kvKeyPattern.MatchString(key) {
		return fmt.Errorf("invalid blackboard key %q; use only alphanumeric characters and underscores", key)
	}
	if err := ensureSessionExists(session); err != nil {
		return err
	}
	if err := run(verbose, "set-option", "-t", session, kvPrefix+key, value); err != nil {
		return fmt.Errorf("set blackboard key %q: %w", key, err)
	}
	return nil
}

func GetKV(session string, key string, verbose bool) (string, error) {
	if !kvKeyPattern.MatchString(key) {
		return "", fmt.Errorf("invalid blackboard key %q", key)
	}
	if err := ensureSessionExists(session); err != nil {
		return "", err
	}
	value, err := capture(verbose, "show-options", "-qv", "-t", session, kvPrefix+key)
	if err != nil {
		return "", fmt.Errorf("get blackboard key %q: %w", key, err)
	}
	return strings.TrimSpace(value), nil
}

func ListKV(session string, verbose bool) (map[string]string, error) {
	if err := ensureSessionExists(session); err != nil {
		return nil, err
	}
	out, err := captureRaw(verbose, "show-options", "-t", session)
	if err != nil {
		return nil, fmt.Errorf("list blackboard: %w", err)
	}

	kv := make(map[string]string)
	lines := strings.Split(strings.TrimSpace(out), "\n")
	for _, line := range lines {
		if strings.HasPrefix(line, kvPrefix) {
			line = strings.TrimPrefix(line, kvPrefix)
			parts := strings.SplitN(line, " ", 2)
			if len(parts) == 2 {
				key := parts[0]
				val := parts[1]
				// Tmux might quote the value if it has spaces
				if len(val) >= 2 && val[0] == '"' && val[len(val)-1] == '"' {
					val = val[1 : len(val)-1]
				}
				kv[key] = val
			}
		}
	}
	return kv, nil
}

func DelKV(session string, key string, verbose bool) error {
	if !kvKeyPattern.MatchString(key) {
		return fmt.Errorf("invalid blackboard key %q", key)
	}
	if err := ensureSessionExists(session); err != nil {
		return err
	}
	if err := run(verbose, "set-option", "-u", "-t", session, kvPrefix+key); err != nil {
		return fmt.Errorf("delete blackboard key %q: %w", key, err)
	}
	return nil
}

func CapturePaneByName(session string, name string, tailLines int, stripEmpty bool, verbose bool) (string, error) {
	target, err := findPaneByName(session, name)
	if err != nil {
		return "", err
	}

	out, err := captureRaw(verbose, "capture-pane", "-J", "-p", "-t", target.PaneID)
	if err != nil {
		return "", fmt.Errorf("capture pane %q: %w", name, err)
	}
	if stripEmpty {
		out = stripEmptyLinesFromCapture(out)
	}
	if tailLines > 0 {
		out = tailLinesFromCapture(out, tailLines)
	}
	return out, nil
}

func WaitForPattern(session string, name string, pattern string, timeout time.Duration, verbose bool) (string, error) {
	re, err := regexp.Compile(pattern)
	if err != nil {
		return "", fmt.Errorf("invalid pattern %q: %w", pattern, err)
	}

	start := time.Now()
	for {
		out, err := CapturePaneByName(session, name, 100, true, verbose)
		if err != nil {
			return "", err
		}

		if match := re.FindString(out); match != "" {
			return match, nil
		}

		if timeout > 0 && time.Since(start) > timeout {
			return "", fmt.Errorf("timeout waiting for pattern %q in pane %q after %v", pattern, name, timeout)
		}

		time.Sleep(500 * time.Millisecond)
	}
}

func stripEmptyLinesFromCapture(content string) string {
	if content == "" {
		return content
	}

	trailingNewline := strings.HasSuffix(content, "\n")
	lines := strings.Split(strings.TrimSuffix(content, "\n"), "\n")
	filtered := make([]string, 0, len(lines))
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		filtered = append(filtered, line)
	}
	if len(filtered) == 0 {
		return ""
	}

	content = strings.Join(filtered, "\n")
	if trailingNewline {
		return content + "\n"
	}
	return content
}

func tailLinesFromCapture(content string, tailLines int) string {
	if tailLines <= 0 || content == "" {
		return content
	}

	trailingNewline := strings.HasSuffix(content, "\n")
	lines := strings.Split(strings.TrimSuffix(content, "\n"), "\n")
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	if len(lines) == 0 {
		return ""
	}
	if tailLines >= len(lines) {
		content = strings.Join(lines, "\n")
		if trailingNewline {
			return content + "\n"
		}
		return content
	}

	tail := strings.Join(lines[len(lines)-tailLines:], "\n")
	if trailingNewline {
		return tail + "\n"
	}
	return tail
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
	args = append(args, paneCreateCommand(firstPane))

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
		args = append(args, paneCreateCommand(pane))

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
	args = append(args, paneCreateCommand(pane))

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
	args = append(args, paneCreateCommand(pane))

	out, err := capture(verbose, args...)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

func configurePane(paneID string, pane resolve.Pane, verbose bool) error {
	startupCommand := buildPaneCommand(pane)
	if err := applyPaneLifecycle(paneID, pane.OnExit, verbose); err != nil {
		return err
	}
	if err := applyPaneMetadata(paneID, paneMetadata{
		Name:           pane.Name,
		StartupCommand: startupCommand,
		SourceType:     pane.SourceType,
		AgentName:      pane.AgentName,
		OnExit:         pane.OnExit,
	}, verbose); err != nil {
		return err
	}
	if needsDeferredStart(pane) && pane.Title != "" {
		if err := run(verbose, "select-pane", "-t", paneID, "-T", pane.Title); err != nil {
			return fmt.Errorf("set title for pane %s: %w", paneID, err)
		}
	}
	if needsDeferredStart(pane) {
		if err := run(verbose, "respawn-pane", "-k", "-t", paneID, startupCommand); err != nil {
			return fmt.Errorf("start pane %s: %w", paneID, err)
		}
		return nil
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
	switch config.NormalizeOnExit(pane.OnExit) {
	case config.OnExitRemain, config.OnExitKill:
		parts = append(parts, command)
		return strings.Join(parts, " && ")
	case config.OnExitRestart:
		script := fmt.Sprintf("while :; do %s; status=$?; printf '\\n[agentmux] command exited with status %%s; restarting in 1s\\n' \"$status\"; sleep 1; done", command)
		command = singleQuote(interactiveShell()) + " -lc " + singleQuote(script)
	}
	parts = append(parts, command)

	return strings.Join(parts, " && ") + "; exec " + singleQuote(interactiveShell())
}

func paneCreateCommand(pane resolve.Pane) string {
	if needsDeferredStart(pane) {
		return "exec sleep 3600"
	}
	return buildPaneCommand(pane)
}

func needsDeferredStart(pane resolve.Pane) bool {
	switch config.NormalizeOnExit(pane.OnExit) {
	case config.OnExitRemain, config.OnExitKill:
		return true
	default:
		return false
	}
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
	if Socket() == "" {
		if _, ok, err := CurrentSession(); err != nil {
			return err
		} else if ok {
			cmd := exec.Command(tmuxBinary(), "switch-client", "-t", name)
			if verbose {
				fmt.Fprintf(os.Stderr, "tmux %s\n", strings.Join(cmd.Args[1:], " "))
			}
			cmd.Stdin = os.Stdin
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr
			return cmd.Run()
		}
	}

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

func captureRaw(verbose bool, args ...string) (string, error) {
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
	return stdout.String(), nil
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

func findPaneByName(session string, name string) (PaneStatus, error) {
	panes, err := Status(session)
	if err != nil {
		return PaneStatus{}, err
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
			return PaneStatus{}, fmt.Errorf("pane %q not found in session %q; session has no named panes. Add `name:` fields in config and reload the session", name, session)
		}
		return PaneStatus{}, fmt.Errorf("pane %q not found in session %q; available named panes: %s. Run `agentmux status %s` to inspect live panes", name, session, strings.Join(available, ", "), session)
	}

	return *target, nil
}

func ensureSessionExists(session string) error {
	exists, err := SessionExists(session)
	if err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("session %q not found. Run `agentmux list` to inspect tmux sessions or `agentmux load <config>` to create it", session)
	}
	return nil
}

func ensureCanReplace(session string) error {
	if socket := Socket(); socket != "" {
		if currentSocket := currentSocketPath(); currentSocket != "" && currentSocket != socket {
			return nil
		}
	}

	current, ok, err := CurrentSession()
	if err != nil {
		return err
	}
	if ok && current == session {
		return fmt.Errorf("cannot replace session %q from inside the same tmux session; run this command from another shell or tmux client", session)
	}
	return nil
}

func currentSocketPath() string {
	tmuxEnv := strings.TrimSpace(os.Getenv("TMUX"))
	if tmuxEnv == "" {
		return ""
	}
	socket, _, _ := strings.Cut(tmuxEnv, ",")
	return strings.TrimSpace(socket)
}

func CurrentSession() (string, bool, error) {
	paneID := strings.TrimSpace(os.Getenv("TMUX_PANE"))
	if paneID == "" && strings.TrimSpace(os.Getenv("TMUX")) == "" {
		return "", false, nil
	}

	args := make([]string, 0, 6)
	if socket := Socket(); socket != "" {
		args = append(args, "-S", socket)
	}
	args = append(args, "display-message", "-p")
	if paneID != "" {
		args = append(args, "-t", paneID)
	}
	args = append(args, "#{session_name}")

	cmd := exec.Command(tmuxBinary(), args...)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		message := strings.TrimSpace(stderr.String())
		if strings.Contains(message, "can't find pane") || strings.Contains(message, "can't find client") {
			return "", false, nil
		}
		if stderr.Len() > 0 {
			return "", false, fmt.Errorf("%s", message)
		}
		return "", false, err
	}

	return strings.TrimSpace(stdout.String()), true, nil
}

func respawnPaneByName(session string, name string, verbose bool) error {
	target, err := findPaneByName(session, name)
	if err != nil {
		return err
	}

	startupCommand := target.StartupCmd
	if strings.TrimSpace(startupCommand) == "" {
		return fmt.Errorf("pane %q in session %q is missing startup command metadata", name, session)
	}
	onExit := target.OnExit
	if err := applyPaneLifecycle(target.PaneID, onExit, verbose); err != nil {
		return fmt.Errorf("apply on_exit for pane %q: %w", name, err)
	}

	if err := run(verbose, "respawn-pane", "-k", "-t", target.PaneID, startupCommand); err != nil {
		return fmt.Errorf("respawn pane %q: %w", name, err)
	}
	if err := applyPaneMetadata(target.PaneID, paneMetadata{
		Name:           target.Name,
		StartupCommand: startupCommand,
		SourceType:     target.SourceType,
		AgentName:      target.AgentName,
		OnExit:         config.NormalizeOnExit(onExit),
	}, verbose); err != nil {
		return fmt.Errorf("restore metadata for pane %q after respawn: %w", name, err)
	}
	return nil
}

func applyPaneLifecycle(paneID string, onExit string, verbose bool) error {
	remain := "off"
	if config.NormalizeOnExit(onExit) == config.OnExitRemain {
		remain = "on"
	}
	if err := run(verbose, "set-option", "-p", "-t", paneID, "remain-on-exit", remain); err != nil {
		return fmt.Errorf("set remain-on-exit for pane %s: %w", paneID, err)
	}
	return nil
}

func applyPaneMetadata(paneID string, metadata paneMetadata, verbose bool) error {
	if metadata.StartTime == "" {
		metadata.StartTime = time.Now().Format(time.RFC3339)
	}

	if strings.TrimSpace(metadata.StartupCommand) != "" {
		sanitized := strings.ReplaceAll(metadata.StartupCommand, "\n", "\\n")
		sanitized = strings.ReplaceAll(sanitized, "\t", "\\t")
		if err := run(verbose, "set-option", "-p", "-t", paneID, paneStartupOption, sanitized); err != nil {
			return fmt.Errorf("set startup command for pane %s: %w", paneID, err)
		}
	}
	if metadata.Name != "" {
		sanitized := strings.ReplaceAll(metadata.Name, "\n", "\\n")
		sanitized = strings.ReplaceAll(sanitized, "\t", "\\t")
		if err := run(verbose, "set-option", "-p", "-t", paneID, paneNameOption, sanitized); err != nil {
			return fmt.Errorf("set name for pane %s: %w", paneID, err)
		}
	}
	if metadata.SourceType != "" {
		if err := run(verbose, "set-option", "-p", "-t", paneID, paneSourceOption, metadata.SourceType); err != nil {
			return fmt.Errorf("set source type for pane %s: %w", paneID, err)
		}
	}
	if metadata.AgentName != "" {
		if err := run(verbose, "set-option", "-p", "-t", paneID, paneAgentOption, metadata.AgentName); err != nil {
			return fmt.Errorf("set agent name for pane %s: %w", paneID, err)
		}
	}
	if metadata.OnExit != "" {
		if err := run(verbose, "set-option", "-p", "-t", paneID, paneOnExitOption, metadata.OnExit); err != nil {
			return fmt.Errorf("set on_exit for pane %s: %w", paneID, err)
		}
	}
	if metadata.StartTime != "" {
		if err := run(verbose, "set-option", "-p", "-t", paneID, paneStartTimeOption, metadata.StartTime); err != nil {
			return fmt.Errorf("set start time for pane %s: %w", paneID, err)
		}
	}
	return nil
}

func showPaneOption(targetPaneID string, option string, verbose bool) (string, error) {
	value, err := capture(verbose, "show-options", "-p", "-v", "-t", targetPaneID, option)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(value), nil
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
