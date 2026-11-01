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
	"agentmux/internal/managedconfig"
	"agentmux/internal/managedlaunch"
	"agentmux/internal/sessioninventory"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type consoleTick time.Time
type consoleData struct {
	taskPage     *consoleTaskPage
	observed     []sessioninventory.Session
	agents       []managed.AgentInfo
	boundaries   managed.Boundaries
	controls     managed.ControlState
	teams        []managed.Team
	autonomy     managed.AutonomyStatus
	conversation managed.ConversationState
	research     *managed.ResearchStatus
	runtimeErr   error
	teamsErr     error
	capacity     managed.Capacity
	budget       managedconfig.AgentBudget
	capacityErr  error
	researchErr  error
	headless     bool
	activity     managedActivitySnapshot
	runtime      managedlaunch.Runtime
	preview      string
	selected     string
	err          error
}
type consoleAction struct {
	message            string
	err                error
	exit               bool
	details            string
	labelID, labelText string
	draft              *consoleDraft
}
type console struct {
	workspaceRequest                       uint64
	workspacePicker                        *consoleWorkspacePicker
	taskQuery, taskAfter, taskCursor       string
	taskBack                               []string
	taskMore                               bool
	state                                  string
	client                                 *managed.Client
	agents                                 []managed.AgentInfo
	selected                               int
	target                                 int
	scope                                  int
	task                                   int
	tab                                    int
	width, height                          int
	data                                   consoleData
	message                                string
	composer                               string
	text                                   []rune
	busy                                   bool
	popup                                  bool
	messageIndex                           int
	boardFilter                            int
	composeTarget, replyID                 string
	composeMode                            string
	textCursor                             int
	textColumn                             int
	textColumnValid                        bool
	memoryNamespace                        string
	memoryEntries                          []managed.Memory
	memoryEntry                            int
	memoryOpen                             bool
	memoryKey                              string
	memoryVersion                          int
	memoryRequest                          uint64
	memoryLoading                          bool
	observedSelected                       int
	boardSelected                          int
	details                                string
	newNative                              consoleNativeDraft
	findIndex                              int
	labelVersion                           int
	roleVersion                            int
	labelDrafts                            map[string]string
	teamIndex, eventIndex, teamMemberIndex int
	teamAction                             *consoleTeamAction
	reader                                 *consoleReader
	failedDraft                            *consoleDraft
	offline                                bool
}
type consoleNativeDraft struct {
	id, title, provider, project, role string
	nativeOnly                         bool
}
type consoleMemory struct {
	agent     string
	request   uint64
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
	if m.tab == 4 {
		return fetchWorkSnapshot(m.state, m.client, m.selectedID(), &consoleTaskPage{Query: m.taskQuery, After: m.taskAfter})
	}
	return fetchWorkSnapshot(m.state, m.client, m.selectedID())
}

func fetchWorkSnapshot(state string, client *managed.Client, selected string, tasks ...*consoleTaskPage) consoleData {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	d := consoleData{selected: selected}
	agents, err := workRegistry(ctx, client)
	if err != nil {
		status, problem := client.LocalResearchStatus(ctx, state)
		d.research = status
		if problem != nil {
			d.researchErr = problem
		}
		d.err = err
		return d
	}
	d.agents = agents
	var raw json.RawMessage
	cursor := ""
	for {
		raw, err = workCall(ctx, client, "team.list", map[string]any{"limit": 200, "after": cursor})
		if err != nil {
			d.teamsErr = err
			break
		}
		var page struct {
			Teams  []managed.Team `json:"teams"`
			Cursor string         `json:"cursor"`
		}
		if err = json.Unmarshal(raw, &page); err != nil {
			d.teamsErr = err
			break
		}
		d.teams = append(d.teams, page.Teams...)
		if len(page.Teams) == 0 || page.Cursor == "" || page.Cursor == cursor {
			break
		}
		cursor = page.Cursor
	}
	if raw, err = workCall(ctx, client, "research.status", map[string]any{}); err != nil {
		d.researchErr = err
	} else {
		var status struct {
			Research *managed.ResearchStatus `json:"research"`
		}
		if err = json.Unmarshal(raw, &status); err != nil {
			d.researchErr = err
		} else {
			d.research = status.Research
		}
	}
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
		if raw, err = workCall(ctx, client, "capacity.get", map[string]string{"agent": d.selected}); err != nil {
			d.capacityErr = err
		} else if err = json.Unmarshal(raw, &d.capacity); err != nil {
			d.capacityErr = err
		}
		if raw, err = workCall(ctx, client, "budget.status", map[string]string{"agent": d.selected}); err != nil {
			d.capacityErr = err
		} else {
			var budget struct {
				Limit managedconfig.AgentBudget `json:"limit"`
			}
			if err = json.Unmarshal(raw, &budget); err != nil {
				d.capacityErr = err
			} else {
				d.budget = budget.Limit
			}
		}
		raw, err = workCall(ctx, client, "boundaries.get", map[string]string{"agent": d.selected})
		if err != nil {
			d.err = err
			return d
		}
		if err = json.Unmarshal(raw, &d.boundaries); err != nil {
			d.err = err
			return d
		}
		if raw, err = workCall(ctx, client, "autonomy.status", map[string]string{"agent": d.selected}); err != nil {
			d.runtimeErr = err
		} else {
			var status struct {
				Status managed.AutonomyStatus `json:"status"`
			}
			if err = json.Unmarshal(raw, &status); err != nil {
				d.runtimeErr = err
			} else {
				d.autonomy = status.Status
			}
		}
		for _, info := range d.agents {
			if info.ID == d.selected && info.Config.Conversation == "persistent" {
				if raw, err = workCall(ctx, client, "conversation.status", map[string]string{"agent": d.selected}); err != nil {
					d.runtimeErr = err
				} else {
					var status struct {
						Conversation managed.ConversationState `json:"conversation"`
					}
					if err = json.Unmarshal(raw, &status); err != nil {
						d.runtimeErr = err
					} else {
						d.conversation = status.Conversation
					}
				}
			}
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
	if len(tasks) > 0 && tasks[0] != nil {
		page := *tasks[0]
		raw, err = workCall(ctx, client, "task.list", map[string]any{"limit": 40, "order": "desc", "query": page.Query, "after": page.After})
		if err == nil {
			var result struct {
				Tasks  []managed.Task `json:"tasks"`
				Cursor string         `json:"cursor"`
				More   bool           `json:"has_more"`
			}
			err = json.Unmarshal(raw, &result)
			if err == nil {
				d.activity.Tasks = result.Tasks
				page.Cursor = result.Cursor
				page.More = result.More
				d.taskPage = &page
			}
		}
		if err != nil {
			d.err = err
			return d
		}
	}
	d.runtime, _ = managedlaunch.Snapshot(state)
	for _, info := range d.agents {
		if info.ID == d.selected && !info.External && d.runtime.Agents[d.selected].PaneID == "" {
			d.headless = d.research != nil || d.runtime.AttemptID == "" && info.Config.Interaction != "interactive"
		}
	}
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
		if m.offline {
			return consoleAction{err: errors.New("broker offline; refresh before making changes")}
		}
		_, err := workCall(context.Background(), m.client, op, args)
		return consoleAction{message: message, err: err}
	}
}
func (m console) attach() tea.Cmd {
	state, id, runtime, popup := m.state, m.selectedID(), m.data.runtime, m.popup
	if m.data.headless || m.data.researchErr != nil || m.data.research != nil && runtime.Agents[id].PaneID == "" {
		return func() tea.Msg {
			if m.data.research == nil {
				return consoleAction{message: "Headless managed worker: inspect Tasks and Events. Cancel accepted work from Tasks; there is no native tmux pane to attach."}
			}
			return consoleAction{message: "Research workers run headlessly. Inspect Tasks and Events; pause at a phase boundary to steer. Native terminal typing is unavailable for this worker."}
		}
	}
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
	case consoleWorkspaceList:
		if msg.request != m.workspaceRequest || m.composer != "" || m.reader != nil || m.teamAction != nil {
			return m, nil
		}
		if msg.err != nil {
			m.message = "Workspace inventory unavailable: " + activityText(msg.err.Error(), 160)
		} else {
			m.workspacePicker = &consoleWorkspacePicker{rows: msg.rows}
		}
	case consoleMemoryVersionReview:
		m.receiveMemoryReview(msg)
	case consoleRoleReview:
		m.receiveRoleReview(msg)
	case consoleMemory:
		if msg.request != m.memoryRequest || msg.agent != m.selectedID() || m.tab != 3 || !m.memoryLoading {
			return m, nil
		}
		m.memoryLoading = false
		m.busy = false
		if msg.err != nil {
			m.message = "Memory unavailable: " + activityText(msg.err.Error(), 120)
		} else {
			key := ""
			if m.memoryOpen && m.memoryNamespace == msg.namespace && len(m.memoryEntries) > 0 {
				key = m.memoryEntries[m.memoryEntry%len(m.memoryEntries)].Key
			}
			m.memoryNamespace = msg.namespace
			m.memoryEntries = msg.entries
			m.memoryEntry = 0
			for i, entry := range msg.entries {
				if entry.Key == key {
					m.memoryEntry = i
					break
				}
			}
			m.memoryOpen = true
		}
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
	case consoleTick:
		return m, tea.Batch(m.fetch, consoleTimer())
	case consoleData:
		if msg.err != nil {
			m.offline = true
			if msg.selected == m.selectedID() && msg.research != nil {
				m.data.research = msg.research
				m.data.researchErr = nil
			}
			m.message = "Refresh failed: " + activityText(msg.err.Error(), 160)
			if msg.research != nil {
				m.message += ". Saved research state: " + msg.research.Progress.State + "; writes disabled."
			}
			return m, nil
		}
		m.offline = false
		observedID := ""
		teamTargetID := ""
		recipientID := ""
		if len(m.agents) > 0 {
			teamTargetID = m.agents[m.teamMemberIndex%len(m.agents)].ID
			recipientID = m.agents[m.target%len(m.agents)].ID
		}
		if observations := m.observedSessions(); len(observations) > 0 {
			observedID = observations[m.observedSelected%len(observations)].ID
		}
		oldID := m.selectedID()
		m.agents = msg.agents
		m.teamMemberIndex = 0
		m.target = 0
		for i, a := range m.agents {
			if a.ID == recipientID {
				m.target = i
			}
			if a.ID == teamTargetID {
				m.teamMemberIndex = i
			}
			if a.ID == oldID {
				m.selected = i
			}
		}
		if len(m.agents) > 0 && m.selected >= len(m.agents) {
			m.selected = len(m.agents) - 1
		}
		if oldID != m.selectedID() {
			m.closeMemory()
		}
		if msg.err != nil {
			m.message = "Refresh failed: " + activityText(msg.err.Error(), 160)
		}
		// Ignore a stale selection result. Its terminal or ACLs must never be
		// shown as belonging to a different agent during rapid navigation.
		if msg.selected == m.selectedID() {
			var messageID, taskID, teamID, eventID string
			if len(m.data.teams) > 0 {
				teamID = m.data.teams[m.teamIndex%len(m.data.teams)].ID
			}
			if m.eventIndex > 0 && len(m.data.activity.Events) > 0 {
				eventID = m.data.activity.Events[m.eventIndex%len(m.data.activity.Events)].ID
			}
			oldMessages := m.boardMessages()
			if m.messageIndex > 0 && m.messageIndex < len(oldMessages) {
				messageID = oldMessages[m.messageIndex].ID
			}
			if m.task < len(m.data.activity.Tasks) {
				taskID = m.data.activity.Tasks[m.task].ID
			}
			if m.tab == 4 {
				if msg.taskPage == nil || msg.taskPage.Query != m.taskQuery || msg.taskPage.After != m.taskAfter {
					msg.activity.Tasks = m.data.activity.Tasks
				} else {
					m.taskCursor = msg.taskPage.Cursor
					m.taskMore = msg.taskPage.More
				}
			}
			m.data = msg
			m.teamIndex = 0
			for i, team := range msg.teams {
				if team.ID == teamID {
					m.teamIndex = i
					break
				}
			}
			m.eventIndex = 0
			for i, event := range msg.activity.Events {
				if event.ID == eventID {
					m.eventIndex = i
					break
				}
			}
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
		if msg.labelID != "" {
			if msg.err != nil {
				if m.labelDrafts == nil {
					m.labelDrafts = map[string]string{}
				}
				m.labelDrafts[msg.labelID] = msg.labelText
			} else {
				delete(m.labelDrafts, msg.labelID)
			}
		}
		if msg.err != nil {
			advice := "Refresh before retrying; delivery may be uncertain."
			var rejected *managed.Fault
			if errors.As(msg.err, &rejected) {
				switch rejected.Code {
				case "conflict", "invalid", "forbidden", "not_found", "limit", "expired":
					advice = "Request rejected. Refresh current state before trying again."
				}
			}
			m.message = "Action failed: " + activityText(msg.err.Error(), 200) + ". " + advice
			if msg.draft != nil {
				m.failedDraft = msg.draft
				m.message = "Draft kept for " + msg.draft.target + "; Ctrl-R restores after reviewing delivery. " + m.message
			}
			if msg.labelID != "" {
				m.message = "Title draft kept for " + msg.labelID + "; press L to review after refresh. " + m.message
			}
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
	if s.Source == "team" {
		m.message = "Consented team access follows membership. Open Teams to invite, remove or dissolve; invitations require participant acceptance."
		return nil
	}
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
	views := map[string]int{"": 0, "board": 0, "agents": 1, "connections": 2, "memory": 3, "tasks": 4, "sessions": 5, "teams": 6, "events": 7, "research": 7}
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
		if draft.nativeOnly {
			args = append(args, "--native-only")
		}
		if draft.id != "" {
			args = append([]string{draft.id}, args...)
		}
		if code := runWorkAdd(args, &output, &diagnostics); code != exitSuccess {
			return consoleAction{err: fmt.Errorf("%s", strings.TrimSpace(diagnostics.String()+output.String())), draft: &consoleDraft{kind: "new-title", target: "What are you working on? (optional)", text: draft.title, native: &draft}}
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
