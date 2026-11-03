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
	case "start", "attach", "dashboard", "sessions", "workspaces", "context", "task", "tasks", "add", "collaborate", "disconnect", "isolate", "label", "delivery", "role", "web", "send", "reply", "inbox", "board", "down", "remove", "retire", "connect", "relaunch", "allow", "deny", "split", "unsplit":
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
			if command == "delivery" || command == "workspaces" || command == "context" || command == "task" || command == "tasks" {
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
	case "--session", "--state", "--config", "--project", "--sandbox", "--view", "--group", "--board", "--legacy-policy", "--mode", "--to", "--resume", "--native-launcher", "--harness", "--role", "--title", "--model", "--auth-file", "--auth-provider", "--name", "--approve", "--max-turns", "--max-hops", "--cooldown", "--port", "--register", "--limit", "--after", "--query", "--status":
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
			fmt.Fprintf(out, "Current workspace: %s\nState: %s\nOpen: %s\n", consoleContent(current.session, 100), consoleContent(current.state, 1000), displayWorkCommand(workHomeCommand(operation, current.state)))
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
		writeWorkHomeInventory(out, rows, operation)
		return exitSuccess
	}
	model, err := tea.NewProgram(workHome{rows: live, operation: operation, height: 24, width: 80}, tea.WithInput(in), tea.WithOutput(out), tea.WithAltScreen()).Run()
	if err != nil {
		fmt.Fprintln(diagnostics, "error:", err)
		return exitRuntime
	}
	if chosen := model.(workHome).chosen; chosen != "" {
		return runWorkSession(operation, []string{"--state", chosen}, in, out, diagnostics)
	}
	return exitSuccess
}

func workHomeCommand(operation, state string) string {
	command := "ui"
	if operation == "attach" {
		command = "a"
	}
	return managedlaunch.ShellCommand([]string{"amx", command, "--state", state})
}

// Never label escaped or truncated argv as a copyable command. JSON views
// retain exact values when a path or filter contains terminal display controls.
func displayWorkCommand(command string) string {
	if consoleContent(command, len(command)) != command || strings.ContainsAny(command, "\n\r\t") {
		return "copyable command omitted because an argument contains display controls; use JSON for exact values."
	}
	return command
}

// Keep the landing view useful after a long history of stopped workspaces.
// The complete inventory remains available through amx ws.
func writeWorkHomeInventory(out io.Writer, rows []workspaceListing, operation string) {
	var active, saved []workspaceListing
	for _, row := range rows {
		if row.Status == "running" || row.Status == "attention" {
			active = append(active, row)
		} else {
			saved = append(saved, row)
		}
	}
	fmt.Fprintln(out, "amx · workspaces")
	writeRow := func(row workspaceListing) {
		fmt.Fprintf(out, "  %s · %s · %d sessions\n    %s\n    %s\n", consoleContent(row.Session, 100), consoleContent(row.Status, 100), row.Agents, consoleContent(row.State, 1000), displayWorkCommand(workHomeCommand(operation, row.State)))
		if row.Problem != "" {
			fmt.Fprintln(out, "    "+consoleContent(row.Problem, 300))
		}
	}
	if len(active) > 0 {
		fmt.Fprintf(out, "Active (%d):\n", len(active))
		for _, row := range active {
			writeRow(row)
		}
	} else {
		fmt.Fprintln(out, "No running workspaces.")
	}
	if len(saved) > 0 {
		count := min(3, len(saved))
		fmt.Fprintf(out, "Saved (%d of %d):\n", count, len(saved))
		for _, row := range saved[:count] {
			writeRow(row)
		}
	}
	fmt.Fprintln(out, "All workspaces: amx ws\nTarget details: amx context\nNew workspace: amx up -s NAME")
}

type workHome struct {
	rows          []workspaceListing
	selected      int
	chosen        string
	operation     string
	query         string
	filtering     bool
	filterFocus   string
	height, width int
}

func (m workHome) Init() tea.Cmd { return nil }

func (m workHome) visibleRows() []workspaceListing {
	words := strings.Fields(strings.ToLower(m.query))
	if len(words) == 0 {
		return m.rows
	}
	var rows []workspaceListing
	for _, row := range m.rows {
		text := strings.ToLower(row.Session + " " + row.State + " " + row.Status)
		match := true
		for _, word := range words {
			if !strings.Contains(text, word) {
				match = false
				break
			}
		}
		if match {
			rows = append(rows, row)
		}
	}
	return rows
}

func (m workHome) selectedState() string {
	rows := m.visibleRows()
	if m.selected < 0 || m.selected >= len(rows) {
		return ""
	}
	return rows[m.selected].State
}

func (m *workHome) setQuery(query string) {
	focus := m.selectedState()
	if strings.TrimSpace(m.query) == "" && strings.TrimSpace(query) != "" {
		m.filterFocus = focus
	}
	m.query = query
	if strings.TrimSpace(query) == "" && m.filterFocus != "" {
		focus = m.filterFocus
	}
	m.selected = 0
	for i, row := range m.visibleRows() {
		if row.State == focus {
			m.selected = i
			break
		}
	}
}

func (m workHome) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if size, ok := msg.(tea.WindowSizeMsg); ok {
		m.height, m.width = size.Height, size.Width
	}
	if key, ok := msg.(tea.KeyMsg); ok {
		rows := m.visibleRows()
		switch key.String() {
		case "esc", "ctrl+c":
			return m, tea.Quit
		case "enter":
			if chosen := m.selectedState(); chosen != "" {
				m.chosen = chosen
				return m, tea.Quit
			}
		case "up", "ctrl+p":
			m.selected = max(0, m.selected-1)
		case "down", "ctrl+n":
			m.selected = max(0, min(len(rows)-1, m.selected+1))
		default:
			if m.filtering {
				switch key.Type {
				case tea.KeyRunes:
					m.setQuery(m.query + string(key.Runes))
				case tea.KeySpace:
					m.setQuery(m.query + " ")
				case tea.KeyBackspace, tea.KeyCtrlH:
					runes := []rune(m.query)
					if len(runes) > 0 {
						m.setQuery(string(runes[:len(runes)-1]))
					}
				case tea.KeyCtrlU:
					m.setQuery("")
				}
				return m, nil
			}
			switch key.String() {
			case "/", "ctrl+f":
				m.filterFocus = m.selectedState()
				m.filtering = true
			case "k":
				m.selected = max(0, m.selected-1)
			case "j":
				m.selected = max(0, min(len(rows)-1, m.selected+1))
			case "q":
				return m, tea.Quit
			}
		}
	}
	return m, nil
}
func (m workHome) View() string {
	var b strings.Builder
	rows := m.visibleRows()
	b.WriteString(consoleAccent.Render("amx · choose workspace") + "\n")
	if m.filtering {
		fmt.Fprintf(&b, "Filter: %s▏ · %d/%d\n\n", consoleContent(m.query, 200), len(rows), len(m.rows))
	} else {
		fmt.Fprintf(&b, "%d active workspaces · / filters name, state or status\n\n", len(rows))
	}
	count := max(1, (m.height-5)/3)
	first := max(0, m.selected-count+1)
	for i := first; i < len(rows) && i < first+count; i++ {
		row := rows[i]
		text := fmt.Sprintf("  %s · %s · %d sessions", consoleContent(row.Session, 100), consoleContent(row.Status, 100), row.Agents)
		if i == m.selected {
			text = consoleSelected.Render("› " + strings.TrimSpace(text))
		}
		fmt.Fprintf(&b, "%s\n  %s\n\n", text, consoleMuted.Render(consoleMiddle(row.State, max(28, m.width-2))))
	}
	if len(rows) == 0 {
		b.WriteString("No matching workspaces. Ctrl+U clears the filter.\n\n")
	}
	open := "workbench"
	if m.operation == "attach" {
		open = "terminal"
	}
	if m.filtering {
		fmt.Fprintf(&b, "↑/↓ choose · Enter opens %s · Ctrl+U clears · Esc cancels\n", open)
	} else {
		fmt.Fprintf(&b, "↑/↓ or j/k choose · Enter opens %s · / filters · Esc cancels\n", open)
	}
	return consoleClip(b.String(), max(30, m.width), max(8, m.height))
}
