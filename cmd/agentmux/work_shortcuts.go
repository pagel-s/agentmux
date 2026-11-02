package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"agentmux/internal/managedlaunch"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/term"
)

var workAliases = map[string]string{
	"up": "start", "a": "attach", "open": "attach", "ui": "dashboard",
	"ls": "sessions", "ws": "workspaces", "new": "add", "link": "collaborate",
	"unlink": "disconnect", "name": "label", "mode": "delivery",
	"archive": "retire",
}

func workCommand(command string) bool {
	switch command {
	case "start", "attach", "dashboard", "sessions", "workspaces", "add", "collaborate", "disconnect", "isolate", "label", "delivery", "role", "web", "send", "reply", "inbox", "board", "down", "remove", "retire", "connect", "relaunch", "allow", "deny", "split", "unsplit":
		return true
	}
	return false
}

// Normalize only everyday commands. Legacy raw terminal input and everything
// after -- remain data. Explicit selectors are never silently overwritten.
func normalizeWorkCLI(input []string) ([]string, error) {
	args := append([]string(nil), input...)
	var leading []string
	i := 0
	for i < len(args) {
		name, value, inline := strings.Cut(args[i], "=")
		if name != "-s" && name != "--session" && name != "--state" {
			break
		}
		if name == "-s" {
			name = "--session"
		}
		if !inline {
			i++
			if i >= len(args) {
				return nil, fmt.Errorf("%s requires a value", name)
			}
			value = args[i]
		}
		leading = append(leading, name, value)
		i++
	}
	if i == len(args) {
		if len(leading) == 0 {
			return args, nil
		}
		return checkedWorkSelectors(append([]string{"dashboard"}, leading...))
	}
	command := args[i]
	if canonical, ok := workAliases[command]; ok {
		command = canonical
	}
	if !workCommand(command) {
		if len(leading) > 0 {
			return nil, errors.New("workspace selectors apply to everyday commands; use amx ui, a, ls, link or mode")
		}
		return args, nil
	}
	tail := args[i+1:]
	if command == "send" && len(leading) == 0 && len(tail) == 2 {
		// Preserve the existing two-argument message form, even when its
		// literal message happens to be a newly introduced short option.
		return append([]string{command}, tail...), nil
	}
	if command == "send" && len(leading) == 0 && !workSendRequested(tail) {
		selector := false
		for j, arg := range tail {
			if arg == "--" {
				break
			}
			if strings.HasPrefix(arg, "-s=") || arg == "-s" && j+1 < len(tail) {
				selector = true
			}
		}
		if len(tail) == 3 && !selector {
			return args, nil
		}
		// Three leading positionals identify the original SESSION PANE COMMAND
		// interface. Never reinterpret flags inside its shell command.
		positionals := 0
		for _, arg := range tail {
			if strings.HasPrefix(arg, "-") {
				break
			}
			positionals++
		}
		if positionals >= 3 {
			return args, nil
		}
	}
	result := []string{command}
	for n := 0; n < len(tail); n++ {
		arg := tail[n]
		if arg == "--" {
			result = append(result, leading...)
			leading = nil
			result = append(result, tail[n:]...)
			break
		}
		name, value, inline := strings.Cut(arg, "=")
		switch name {
		case "-s":
			name = "--session"
		case "-p":
			if command == "start" || command == "add" {
				name = "--project"
			}
		case "-d":
			if command == "start" {
				name = "--detach"
			}
		case "-j":
			if command == "delivery" || command == "workspaces" {
				name = "--json"
			}
		case "-o":
			if command == "web" {
				name = "--open"
			}
		}
		if inline {
			result = append(result, name+"="+value)
		} else {
			result = append(result, name)
			if workOptionValue(name) && n+1 < len(tail) {
				n++
				result = append(result, tail[n])
			}
		}
	}
	result = append(result, leading...)
	switch command {
	case "add", "delivery", "connect":
		result = interspersedWorkOptions(result)
	}
	return checkedWorkSelectors(result)
}

// These commands use Go FlagSet after their positional operands. Let owners
// place options anywhere without moving option values or literal -- payloads.
func interspersedWorkOptions(args []string) []string {
	positionals, options := []string{args[0]}, []string{}
	var literal []string
	for i := 1; i < len(args); i++ {
		if args[i] == "--" {
			literal = args[i:]
			break
		}
		name, _, inline := strings.Cut(args[i], "=")
		if strings.HasPrefix(name, "-") {
			options = append(options, args[i])
			if !inline && workOptionValue(name) && i+1 < len(args) {
				i++
				options = append(options, args[i])
			}
		} else {
			positionals = append(positionals, args[i])
		}
	}
	return append(append(positionals, options...), literal...)
}

func checkedWorkSelectors(args []string) ([]string, error) {
	seen := map[string]bool{}
	for i := 1; i < len(args); i++ {
		if args[i] == "--" {
			break
		}
		name, _, inline := strings.Cut(args[i], "=")
		if name == "--session" || name == "--state" {
			if seen[name] {
				return nil, fmt.Errorf("duplicate workspace selector %s; no action was taken", name)
			}
			seen[name] = true
		}
		if !inline && workOptionValue(name) {
			i++
		}
	}
	return args, nil
}

func workOptionValue(name string) bool {
	switch name {
	case "--session", "--state", "--config", "--project", "--sandbox", "--view", "--group", "--board", "--legacy-policy", "--mode", "--to", "--resume", "--native-launcher", "--harness", "--role", "--title", "--model", "--auth-file", "--auth-provider", "--name", "--approve", "--max-turns", "--max-hops", "--cooldown", "--port", "--register":
		return true
	}
	return false
}

func contextTMux(ctx context.Context, socket string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "tmux", append([]string{"-S", socket}, args...)...)
	cmd.WaitDelay = 100 * time.Millisecond
	data, err := cmd.Output()
	return strings.TrimSpace(string(data)), err
}

// Context is attachment-local, not a global last-used workspace. Check both
// the dedicated server and this exact core/view session's attempt tag.
func ownedWorkContext(value string, query func(context.Context, string, ...string) (string, error)) (workOptions, bool, error) {
	last := strings.LastIndexByte(value, ',')
	if last < 0 {
		return workOptions{}, false, nil
	}
	previous := strings.LastIndexByte(value[:last], ',')
	if previous < 0 {
		return workOptions{}, false, nil
	}
	socket, id := value[:previous], value[last+1:]
	if !filepath.IsAbs(socket) || filepath.Base(socket) != "tmux.sock" {
		return workOptions{}, false, nil
	}
	state := filepath.Dir(socket)
	runtime, err := managedlaunch.Snapshot(state)
	failure := errors.New("cannot verify the current Agentmux workspace; select it explicitly with -s NAME or --state DIR")
	if os.IsNotExist(err) {
		if _, problem := os.Stat(filepath.Join(state, "state.sqlite")); problem == nil {
			return workOptions{}, true, failure
		}
		return workOptions{}, false, nil
	}
	if err != nil {
		return workOptions{}, true, failure
	}
	if _, err := strconv.ParseUint(id, 10, 64); err != nil {
		return workOptions{}, true, failure
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	name, err := query(ctx, socket, "display-message", "-p", "-t", "$"+id, "#{session_name}")
	if err != nil || name == "" {
		return workOptions{}, true, failure
	}
	option := "@agentmux_view"
	if name == runtime.Session {
		option = "@agentmux_runtime"
	}
	tag, err := query(ctx, socket, "show-options", "-v", "-t", name, option)
	if err != nil || tag != runtime.AttemptID {
		return workOptions{}, true, failure
	}
	return workOptions{state: state, session: runtime.Session}, true, nil
}

func runWorkHome(in io.Reader, out, diagnostics io.Writer) int {
	return runWorkHomeOperation("dashboard", in, out, diagnostics)
}

func runWorkHomeOperation(operation string, in io.Reader, out, diagnostics io.Writer) int {
	input, terminal := in.(interface{ Fd() uintptr })
	terminal = terminal && term.IsTerminal(input.Fd())
	output, outputTerminal := out.(interface{ Fd() uintptr })
	terminal = terminal && outputTerminal && term.IsTerminal(output.Fd())
	current, found, err := ownedWorkContext(os.Getenv("TMUX"), contextTMux)
	if err != nil {
		fmt.Fprintln(diagnostics, "error:", err)
		return exitValidation
	}
	if found {
		if !terminal {
			fmt.Fprintf(out, "Current workspace: %s\nOpen: amx ui --state %s\n", consoleContent(current.session, 100), consoleContent(current.state, 500))
			return exitSuccess
		}
		return runWorkSession(operation, []string{"--state", current.state}, in, out, diagnostics)
	}
	rows, err := workspaceInventory("")
	if err != nil {
		fmt.Fprintln(diagnostics, "error:", err)
		return exitRuntime
	}
	live := []workspaceListing{}
	for _, row := range rows {
		if row.Status == "running" || row.Status == "attention" {
			live = append(live, row)
		}
	}
	if len(live) == 1 && terminal {
		return runWorkSession(operation, []string{"--state", live[0].State}, in, out, diagnostics)
	}
	if len(live) == 0 || !terminal {
		fmt.Fprintln(out, "amx · workspaces")
		for _, row := range rows {
			fmt.Fprintf(out, "  %s · %s · %d sessions\n", consoleContent(row.Session, 100), row.Status, row.Agents)
		}
		fmt.Fprintln(out, "Open: amx ui -s NAME\nNew workspace: amx up -s NAME")
		return exitSuccess
	}
	model, err := tea.NewProgram(workHome{rows: live, height: 24, width: 80}, tea.WithInput(in), tea.WithOutput(out), tea.WithAltScreen()).Run()
	if err != nil {
		fmt.Fprintln(diagnostics, "error:", err)
		return exitRuntime
	}
	if chosen := model.(workHome).chosen; chosen != "" {
		return runWorkSession(operation, []string{"--state", chosen}, in, out, diagnostics)
	}
	return exitSuccess
}

type workHome struct {
	rows          []workspaceListing
	selected      int
	chosen        string
	height, width int
}

func (m workHome) Init() tea.Cmd { return nil }
func (m workHome) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if size, ok := msg.(tea.WindowSizeMsg); ok {
		m.height, m.width = size.Height, size.Width
	}
	if key, ok := msg.(tea.KeyMsg); ok {
		switch key.String() {
		case "up", "k":
			m.selected = max(0, m.selected-1)
		case "down", "j":
			m.selected = max(0, min(len(m.rows)-1, m.selected+1))
		case "esc", "q", "ctrl+c":
			return m, tea.Quit
		case "enter":
			if len(m.rows) > 0 {
				m.chosen = m.rows[m.selected].State
				return m, tea.Quit
			}
		}
	}
	return m, nil
}
func (m workHome) View() string {
	var b strings.Builder
	b.WriteString(consoleAccent.Render("amx · choose workspace") + "\n\n")
	count := max(1, (m.height-5)/3)
	first := max(0, m.selected-count+1)
	for i := first; i < len(m.rows) && i < first+count; i++ {
		row := m.rows[i]
		text := fmt.Sprintf("  %s · %d sessions", consoleContent(row.Session, 100), row.Agents)
		if i == m.selected {
			text = consoleSelected.Render("› " + strings.TrimSpace(text))
		}
		fmt.Fprintf(&b, "%s\n  %s\n\n", text, consoleMuted.Render(consoleContent(row.State, 200)))
	}
	b.WriteString("↑/↓ choose · Enter opens workbench · Esc cancels\n")
	return consoleClip(b.String(), max(30, m.width), max(8, m.height))
}
