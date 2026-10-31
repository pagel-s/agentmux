package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"agentmux/internal/managed"
	"agentmux/internal/managedlaunch"
	"agentmux/internal/sessioninventory"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type consoleTick time.Time
type consoleData struct {
	observed   []sessioninventory.Session
	agents     []managed.AgentInfo
	boundaries managed.Boundaries
	controls   managed.ControlState
	activity   managedActivitySnapshot
	runtime    managedlaunch.Runtime
	preview    string
	selected   string
	err        error
}
type consoleAction struct {
	message string
	err     error
	exit    bool
	details string
}
type console struct {
	state                  string
	client                 *managed.Client
	agents                 []managed.AgentInfo
	selected               int
	target                 int
	scope                  int
	task                   int
	tab                    int
	width, height          int
	data                   consoleData
	message                string
	composer               string
	text                   []rune
	busy                   bool
	popup                  bool
	messageIndex           int
	boardFilter            int
	composeTarget, replyID string
	textCursor             int
	memoryNamespace        string
	memoryEntries          []managed.Memory
	memoryEntry            int
	memoryOpen             bool
	memoryKey              string
	memoryVersion          int
	observedSelected       int
	boardSelected          int
	details                string
	newNative              consoleNativeDraft
	findIndex              int
	labelVersion           int
}
type consoleNativeDraft struct{ id, title, provider, project, role string }
type consoleMemory struct {
	namespace string
	entries   []managed.Memory
	err       error
}

// Keep the raw observations for native-terminal routing and reconnect checks;
// only the human inventory omits processes already represented by a live bridge.
func unconnectedObservations(agents []managed.AgentInfo, observations []sessioninventory.Session) []sessioninventory.Session {
	registered := map[string]bool{}
	for _, a := range agents {
		if a.External && a.State == "active" && a.ExternalRef != "" {
			registered[a.ExternalRef] = true
		}
	}
	visible := make([]sessioninventory.Session, 0, len(observations))
	for _, v := range observations {
		if !registered[v.ID] {
			visible = append(visible, v)
		}
	}
	return visible
}
func (m console) observedSessions() []sessioninventory.Session {
	return unconnectedObservations(m.agents, m.data.observed)
}
func observationStatus(reference string, observations []sessioninventory.Session) string {
	for _, v := range observations {
		if reference != "" && v.ID == reference {
			return "native observed"
		}
	}
	return "native not currently observed"
}

func consoleTimer() tea.Cmd {
	return tea.Tick(time.Second, func(t time.Time) tea.Msg { return consoleTick(t) })
}
func (m console) selectedID() string {
	if len(m.agents) == 0 {
		return ""
	}
	return m.agents[m.selected%len(m.agents)].ID
}
func (m console) Init() tea.Cmd { return tea.Batch(m.fetch, consoleTimer()) }

func (m console) fetch() tea.Msg {
	return fetchWorkSnapshot(m.state, m.client, m.selectedID())
}

func fetchWorkSnapshot(state string, client *managed.Client, selected string) consoleData {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	d := consoleData{selected: selected}
	raw, err := workCall(ctx, client, "agent.list", map[string]any{"limit": 200})
	if err != nil {
		d.err = err
		return d
	}
	var registry struct {
		Agents []managed.AgentInfo `json:"agents"`
	}
	if err = json.Unmarshal(raw, &registry); err != nil {
		d.err = err
		return d
	}
	d.agents = registry.Agents
	raw, err = workCall(ctx, client, "control.get", map[string]any{})
	if err != nil {
		d.err = err
		return d
	}
	if err = json.Unmarshal(raw, &d.controls); err != nil {
		d.err = err
		return d
	}
	if d.selected == "" && len(d.agents) > 0 {
		d.selected = d.agents[0].ID
	}
	active := false
	for _, agent := range d.agents {
		if agent.ID == d.selected {
			active = agent.State == "active"
		}
	}
	if d.selected != "" && active {
		raw, err = workCall(ctx, client, "boundaries.get", map[string]string{"agent": d.selected})
		if err != nil {
			d.err = err
			return d
		}
		if err = json.Unmarshal(raw, &d.boundaries); err != nil {
			d.err = err
			return d
		}
	}
	if !active {
		d.boundaries = managed.Boundaries{Version: d.controls.Version, Agent: d.selected}
	}
	raw, err = workCall(ctx, client, "activity", map[string]any{"limit": 40})
	if err != nil {
		d.err = err
		return d
	}
	if err = json.Unmarshal(raw, &d.activity); err != nil {
		d.err = err
		return d
	}
	d.runtime, _ = managedlaunch.Snapshot(state)
	d.observed = discoverWorkSessions(ctx, state)
	if pane, ok := d.runtime.Agents[d.selected]; ok {
		// Capture is owner-only. Rendered content is escaped and clipped so
		// native output cannot inject terminal controls into the dashboard.
		cmd := exec.CommandContext(ctx, "tmux", "-S", managedlaunch.SocketPath(state), "capture-pane", "-p", "-t", pane.PaneID, "-S", "-10")
		if data, e := cmd.Output(); e == nil {
			d.preview = string(data)
		}
	}
	return d
}

func (m console) mutate(op string, args any, message string) tea.Cmd {
	return func() tea.Msg {
		_, err := workCall(context.Background(), m.client, op, args)
		return consoleAction{message: message, err: err}
	}
}
func (m console) attach() tea.Cmd {
	state, id, runtime, popup := m.state, m.selectedID(), m.data.runtime, m.popup
	for _, a := range m.agents {
		if a.ID == id && a.External {
			return func() tea.Msg {
				return consoleAction{message: "Use the original native terminal. Its bridge needs the setup pasted by you; readiness is not confirmed.", details: bridgeInstructions(state, id)}
			}
		}
	}
	return func() tea.Msg {
		exe := runtime.Controller
		if exe == "" {
			var err error
			exe, err = os.Executable()
			if err != nil {
				return consoleAction{err: err}
			}
		}
		if !strings.HasPrefix(os.Getenv("TMUX"), managedlaunch.SocketPath(state)+",") {
			return consoleAttach{state: state, agent: id, controller: exe}
		}
		launcher, err := managedlaunch.New(managedlaunch.Options{StateDir: state, Executable: exe})
		if err != nil {
			return consoleAction{err: err}
		}
		if err = launcher.Focus(context.Background(), runtime, id); err != nil {
			return consoleAction{err: err}
		}
		return consoleAction{message: "Native terminal selected. F1 returns to the workbench.", exit: popup}
	}
}

type consoleAttach struct{ state, agent, controller string }

func (m console) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case consoleMemory:
		m.busy = false
		if msg.err != nil {
			m.message = "Memory unavailable: " + activityText(msg.err.Error(), 120)
		} else {
			m.memoryNamespace = msg.namespace
			m.memoryEntries = msg.entries
			m.memoryEntry = 0
			m.memoryOpen = true
		}
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
	case consoleTick:
		return m, tea.Batch(m.fetch, consoleTimer())
	case consoleData:
		if msg.err != nil {
			m.message = "Refresh failed: " + activityText(msg.err.Error(), 160)
			return m, nil
		}
		observedID := ""
		if observations := m.observedSessions(); len(observations) > 0 {
			observedID = observations[m.observedSelected%len(observations)].ID
		}
		oldID := m.selectedID()
		m.agents = msg.agents
		for i, a := range m.agents {
			if a.ID == oldID {
				m.selected = i
				break
			}
		}
		if len(m.agents) > 0 && m.selected >= len(m.agents) {
			m.selected = len(m.agents) - 1
		}
		if msg.err != nil {
			m.message = "Refresh failed: " + activityText(msg.err.Error(), 160)
		}
		// Ignore a stale selection result. Its terminal or ACLs must never be
		// shown as belonging to a different agent during rapid navigation.
		if msg.selected == m.selectedID() {
			var messageID, taskID string
			oldMessages := m.boardMessages()
			if m.messageIndex > 0 && m.messageIndex < len(oldMessages) {
				messageID = oldMessages[m.messageIndex].ID
			}
			if m.task < len(m.data.activity.Tasks) {
				taskID = m.data.activity.Tasks[m.task].ID
			}
			m.data = msg
			if observations := m.observedSessions(); len(observations) > 0 {
				m.observedSelected %= len(observations)
				for i, v := range observations {
					if v.ID == observedID {
						m.observedSelected = i
						break
					}
				}
			} else {
				m.observedSelected = 0
			}
			if messageID != "" {
				for i, v := range m.boardMessages() {
					if v.ID == messageID {
						m.messageIndex = i
						break
					}
				}
			}
			if taskID != "" {
				for i, v := range m.data.activity.Tasks {
					if v.ID == taskID {
						m.task = i
						break
					}
				}
			}
		}
	case consoleAction:
		if msg.exit && msg.err == nil {
			return m, tea.Quit
		}
		m.busy = false
		if msg.err != nil {
			m.message = "Action failed: " + activityText(msg.err.Error(), 200) + ". Refresh before retrying; delivery may be uncertain."
		} else {
			m.message = msg.message
			if msg.details != "" {
				m.details = msg.details
			}
		}
		return m, m.fetch
	case consoleAttach:
		cmd := exec.Command(msg.controller, "attach", msg.agent, "--state", msg.state)
		for _, e := range os.Environ() {
			if !strings.HasPrefix(e, "TMUX=") && !strings.HasPrefix(e, "TMUX_PANE=") {
				cmd.Env = append(cmd.Env, e)
			}
		}
		return m, tea.ExecProcess(cmd, func(err error) tea.Msg {
			return consoleAction{message: "Detached from native terminals; team remains running.", err: err}
		})
	case tea.KeyMsg:
		return m.key(msg)
	}
	return m, nil
}

func boundaryAllows(b managed.Boundaries, target, action string) bool {
	for _, t := range b.Targets {
		if t.To == target {
			for _, a := range t.Actions {
				if a == action {
					return true
				}
			}
		}
	}
	return false
}
func (m *console) toggleScope(write bool) tea.Cmd {
	if m.busy || m.data.boundaries.Agent != m.selectedID() {
		return nil
	}
	scopes := append(append([]managed.BoundaryScope{}, m.data.boundaries.Boards...), m.data.boundaries.Memory...)
	if len(scopes) == 0 {
		return nil
	}
	index := m.scope % len(scopes)
	s := scopes[index]
	kind := "board"
	if index >= len(m.data.boundaries.Boards) {
		kind = "memory"
	}
	if write {
		s.Write = !s.Write
	} else {
		s.Read = !s.Read
	}
	m.busy = true
	return m.mutate("control.update", map[string]any{"version": m.data.boundaries.Version, "kind": kind, "agent": m.selectedID(), "namespace": s.Name, "read": s.Read, "write": s.Write}, "Shared scope updated; future broker reads/writes use the new permissions. Previously read data cannot be recalled.")
}

var consoleAccent = lipgloss.NewStyle().Foreground(lipgloss.Color("#A78BFA")).Bold(true)
var consoleMuted = lipgloss.NewStyle().Foreground(lipgloss.Color("#94A3B8"))
var consoleSelected = lipgloss.NewStyle().Foreground(lipgloss.Color("#FFFFFF")).Background(lipgloss.Color("#4C1D95")).Bold(true)

func accessLabel(v bool) string {
	if v {
		return "allowed"
	}
	return "denied"
}
func runManagedConsole(state string, in io.Reader, out, diagnostics io.Writer) int {
	return runManagedConsoleView(state, "board", false, in, out, diagnostics)
}
func runManagedConsoleView(state, view string, popup bool, in io.Reader, out, diagnostics io.Writer) int {
	views := map[string]int{"": 0, "board": 0, "agents": 1, "connections": 2, "memory": 3, "tasks": 4, "sessions": 5}
	index, ok := views[view]
	if !ok {
		fmt.Fprintln(diagnostics, "error: unknown dashboard view")
		return exitValidation
	}
	credential, err := managed.LoadCredential(managed.CredentialPath(state, ""))
	if err != nil {
		fmt.Fprintln(diagnostics, "error: work session not found; use agentmux start or select --state / --session")
		return exitRuntime
	}
	if credential.AgentID != "$owner" {
		fmt.Fprintln(diagnostics, "error: dashboard requires the owner credential")
		return exitValidation
	}
	model := console{state: state, client: managed.NewClient(credential), tab: index, popup: popup}
	_, err = tea.NewProgram(model, tea.WithInput(in), tea.WithOutput(out), tea.WithAltScreen()).Run()
	if err != nil && !errors.Is(err, context.Canceled) {
		fmt.Fprintln(diagnostics, "error: dashboard:", err)
		return exitRuntime
	}
	return exitSuccess
}

func (m console) addNative(draft consoleNativeDraft) tea.Cmd {
	state := m.state
	return func() tea.Msg {
		var output, diagnostics bytes.Buffer
		args := []string{"--title", draft.title, "--harness", draft.provider, "--project", draft.project, "--role", draft.role, "--sandbox", "local", "--state", state}
		if draft.id != "" {
			args = append([]string{draft.id}, args...)
		}
		if code := runWorkAdd(args, &output, &diagnostics); code != exitSuccess {
			return consoleAction{err: fmt.Errorf("%s", strings.TrimSpace(diagnostics.String()+output.String()))}
		}
		return consoleAction{message: strings.TrimSpace(output.String())}
	}
}

func (m console) selectedProject() string {
	for _, a := range m.agents {
		if a.ID == m.selectedID() && a.Config.Workdir != "" {
			return a.Config.Workdir
		}
	}
	dir, _ := os.Getwd()
	return dir
}

func (m console) bridgeHelper(id string) string { return filepath.Join(m.state+".bridge", id) }
