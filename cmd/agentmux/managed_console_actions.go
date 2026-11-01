package main

import (
	"fmt"
	"sort"
	"strings"

	"agentmux/internal/managed"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

// Frozen owner reviews never derive a target again when Enter is pressed.
type consoleOwnerReview struct {
	operation, title, body, message string
	args                            map[string]any
	offset                          int
}

func (m console) ownerReviewKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.ownerReview = nil
	case "ctrl+c":
		return m, m.close()
	case "down", "j":
		m.ownerReview.offset++
	case "up", "k":
		m.ownerReview.offset = max(0, m.ownerReview.offset-1)
	case "pgdown":
		m.ownerReview.offset += max(1, m.height/2)
	case "pgup":
		m.ownerReview.offset = max(0, m.ownerReview.offset-max(1, m.height/2))
	case "enter":
		if m.busy || m.offline {
			m.message = "Review kept; reconnect and wait for pending changes before applying."
			return m, nil
		}
		a := *m.ownerReview
		m.ownerReview = nil
		m.busy = true
		if a.operation == "launch-native" {
			return m, m.addNative(m.newNative)
		}
		return m, m.mutate(a.operation, a.args, a.message)
	}
	return m, nil
}
func (m console) ownerReviewPanel(width, height int) string {
	a := m.ownerReview
	lines := strings.Split(ansi.Wrap(consoleContent(a.body, 1<<20), max(1, width), ""), "\n")
	first := min(a.offset, max(0, len(lines)-max(1, height-4)))
	return consoleClip(consoleAccent.Render(a.title)+"\nEnter applies exact reviewed action · Esc cancels · ↑/↓ scroll\n\n"+strings.Join(lines[first:], "\n"), width, height)
}

func (m console) stopPopulation(id string) []string {
	chosen := map[string]bool{id: true}
	for change := true; change; {
		change = false
		for _, a := range m.agents {
			if a.State == "active" && !chosen[a.ID] && chosen[a.Parent] {
				chosen[a.ID] = true
				change = true
			}
		}
	}
	ids := []string{}
	for _, a := range m.agents {
		if chosen[a.ID] && a.State == "active" {
			ids = append(ids, a.ID)
		}
	}
	sort.Strings(ids)
	return ids
}
func (m *console) reviewStop() {
	if m.tab == 5 {
		m.message = "Observed sessions are read-only. Select a registered bridge in Sessions to disconnect it."
		return
	}
	if m.tab != 0 && m.tab != 1 {
		m.message = "Revoke is available in Board and Agents. Teams actions use the explicitly labeled team target."
		return
	}
	a := m.selectedAgent()
	if a == nil || a.State != "active" || m.busy {
		return
	}
	ids := m.stopPopulation(a.ID)
	body := "Root stable ID: " + a.ID + "\nTitle: " + sessionTitle(*a) + "\n\nExact active subtree (" + fmt.Sprint(len(ids)) + "):\n" + strings.Join(ids, "\n") + "\n\nOwned native processes receive a shutdown request. External native terminals stay open; their bridges disconnect. Accepted tasks and previously read information remain. If the subtree changes, this review is rejected."
	m.ownerReview = &consoleOwnerReview{operation: "agent.stop", title: "Review session revocation", body: body, args: map[string]any{"id": a.ID, "expected_agents": ids}, message: "Revocation requested for exact reviewed subtree. Check process state before retiring records."}
}
func (m *console) reviewIsolation() {
	a := m.selectedAgent()
	if a == nil || m.busy {
		return
	}
	groups := []string{}
	for _, g := range m.data.controls.Groups {
		if teamContains(g.Members, a.ID) {
			groups = append(groups, g.ID+" · "+strings.Join(g.Members, ", ")+" · board "+g.Board+" · memory "+g.Memory)
		}
	}
	teams := []string{}
	for _, t := range m.data.teams {
		if teamContains(t.Members, a.ID) {
			teams = append(teams, t.ID+" · "+t.Name)
		}
	}
	body := fmt.Sprintf("Stable ID: %s\nPermission version: %d\n\nIsolation suspends peer sends/receives and shared board/memory access across ALL scopes. Saved owner grants are retained. Native interaction, private memory, accepted tasks and previously read knowledge remain.\n\nWorkspace groups:\n%s\n\nConsented teams:\n%s\n\nUse Disable group for one exact contribution instead.", a.ID, m.data.controls.Version, strings.Join(groups, "\n"), strings.Join(teams, "\n"))
	m.ownerReview = &consoleOwnerReview{operation: "control.update", title: "Review global session isolation", body: body, args: map[string]any{"version": m.data.controls.Version, "kind": "independent", "agents": []string{a.ID}}, message: "Session isolated across shared scopes; saved settings retained."}
}
func (m *console) reviewGroupDisable(id string) {
	for _, g := range m.data.controls.Groups {
		if g.ID != id {
			continue
		}
		if g.ContributionsVersion < 1 {
			m.message = "Legacy group: reconnect with c to review migration before disabling its contribution."
			return
		}
		m.ownerReview = &consoleOwnerReview{operation: "control.update", title: "Review exact group disable", body: fmt.Sprintf("Group ID: %s\nOriginal members: %s\nCurrent members: %s\nBoard: %s\nMemory: %s\nPermission version: %d\n\nRemove only this group's contribution. Other groups and saved overrides remain. Accepted tasks continue; previously read data remains.", g.ID, strings.Join(g.Agents, ", "), strings.Join(g.Members, ", "), g.Board, g.Memory, m.data.controls.Version), args: map[string]any{"version": m.data.controls.Version, "kind": "collaboration", "agents": append([]string{}, g.Agents...), "enabled": false}, message: "Exact group disabled. Other group contributions and saved settings retained."}
		return
	}
}
func (m *console) reviewTaskResolution(resolution string) {
	if len(m.data.activity.Tasks) == 0 {
		return
	}
	t := m.data.activity.Tasks[m.task%len(m.data.activity.Tasks)]
	if t.Delivery != "uncertain" && t.Delivery != "cancel_uncertain" {
		m.message = "Only an uncertain delivery needs owner resolution."
		return
	}
	if resolution == "retry" && t.Delivery == "cancel_uncertain" {
		m.message = "Cancellation uncertainty supports fail resolution after verifying the process stopped."
		return
	}
	m.ownerReview = &consoleOwnerReview{operation: "task.resolve", title: "Review uncertain task: " + resolution, body: fmt.Sprintf("Task: %s\nVersion: %d\nRecipient: %s\nState: %s / %s\nInstruction:\n%s\n\nInspect side effects and stop the old process before resolving. Fail records a failed task (cancel_uncertain acknowledges stopped cancellation). Retry requeues this exact task and may repeat effects; a new attempt is charged again. Neither choice proves what previously executed.", t.ID, t.Version, t.To, t.State, t.Delivery, t.Text), args: map[string]any{"id": t.ID, "version": t.Version, "resolution": resolution}, message: "Exact task resolution saved; inspect its new state and version."}
}
func (m *console) reviewRetire() {
	a := m.selectedAgent()
	if a == nil {
		return
	}
	if a.State != "revoked" {
		m.message = "Revoke and verify process shutdown before retiring the session record."
		return
	}
	m.ownerReview = &consoleOwnerReview{operation: "agent.retire", title: "Review session retirement", body: "Stable ID: " + a.ID + "\nTitle: " + sessionTitle(*a) + "\n\nArchive this revoked registry row from ordinary session lists. History, tasks and audit records remain. Retirement does not stop a process; verify shutdown separately. The stable ID remains reserved.", args: map[string]any{"agent": a.ID}, message: "Session record retired; history retained."}
}

type consoleMenuAction struct {
	label, key, target, reason, kind string
	recipient, teamID, taskID        string
	version                          int
	tab                              int
}

func (m *console) openActions() {
	m.actionRows = nil
	m.actionIndex = 0
	id := m.selectedID()
	target := id
	if m.tab == 6 {
		if a := m.teamTarget(); a != nil {
			target = a.ID
		}
	}
	add := func(label, key string, tab int, reason string) {
		m.actionRows = append(m.actionRows, consoleMenuAction{label: label, key: key, tab: tab, target: target, reason: reason})
	}
	need := ""
	if target == "" {
		need = "Select a session"
	}
	add("New session", "N", m.tab, "")
	add("Advanced new session", "new-advanced", m.tab, "")
	add("Open native terminal", "enter", 1, need)
	add("Session details", "o", 1, need)
	add("Knowledge exposure ledger", "exposure", 1, need)
	add("Rename session title", "L", 1, need)
	add("Edit coordinated role", "V", 1, need)
	add("Send note or instruction", "s", 1, need)
	add("Configure native delivery", "D", 1, need)
	add("Review native inbox", "A", 1, need)
	add("Review relaunch", "U", 1, need)
	add("Admission budget", "B", 1, need)
	add("Revoke session and descendants", "x", 1, need)
	add("Retire session record", "retire", 1, need)
	add("Connect selected sender and recipient", "c", 2, need)
	add("Isolate selected session across groups", "d", 2, need)
	for _, g := range m.data.controls.Groups {
		if g.State == "active" && teamContains(g.Agents, id) {
			m.actionRows = append(m.actionRows, consoleMenuAction{label: "Disable exact group " + g.ID, target: g.ID, kind: "disable-group", tab: 2})
		}
	}
	if m.tab == 6 {
		for _, a := range []struct{ label, key, op string }{{"Invite team target", "i", "team.invite"}, {"Remove team target", "r", "team.remove"}, {"Propose team owner", "t", "team.transfer"}, {"Edit team", "e", "team.update"}, {"Dissolve team", "d", "team.dissolve"}} {
			add(a.label, a.key, 6, m.teamActionReason(a.op))
		}
	}
	if m.tab == 4 {
		add("Resolve uncertain task as failed", "resolve-fail", 4, "")
		add("Retry uncertain task", "resolve-retry", 4, "")
	}
	add("Attention queue", "attention", m.tab, "")
	add("Search task history", "f", 4, "")
	add("Research details", "research-details", 7, "")
	add("Whole study overview", "study-details", 7, "")
	add("Toggle whole-study follow", "study-follow", 7, "")
	add("Open current study trial", "study-current", 7, "")
	for i := range m.actionRows {
		a := &m.actionRows[i]
		if a.tab == 6 {
			if t := m.selectedTeam(); t != nil {
				a.teamID = t.ID
				a.version = t.Version
			}
		}
		if a.key == "c" && len(m.agents) > 0 {
			a.recipient = m.agents[m.target%len(m.agents)].ID
		}
		if strings.HasPrefix(a.key, "resolve-") && len(m.data.activity.Tasks) > 0 {
			t := m.data.activity.Tasks[m.task%len(m.data.activity.Tasks)]
			a.taskID = t.ID
			a.version = t.Version
			a.target = t.To
		}
		if a.reason == "" {
			a.reason = m.menuActionReason(*a)
		}
	}
	m.compose("actions", "Search executable actions", "", "")
}
func (m console) menuMatches() []consoleMenuAction {
	out := []consoleMenuAction{}
	q := strings.ToLower(string(m.text))
	for _, a := range m.actionRows {
		if strings.Contains(strings.ToLower(a.label+" "+a.target+" "+a.key), q) {
			out = append(out, a)
		}
	}
	return out
}
func (m console) invokeMenuAction() (tea.Model, tea.Cmd) {
	rows := m.menuMatches()
	if len(rows) == 0 {
		return m, nil
	}
	a := rows[m.actionIndex%len(rows)]
	if a.reason != "" {
		m.message = a.reason
		return m, nil
	}
	if reason := m.menuActionReason(a); reason != "" {
		m.message = reason
		return m, nil
	}
	if a.teamID != "" {
		found := false
		for i, t := range m.data.teams {
			if t.ID == a.teamID && t.Version == a.version {
				m.teamIndex = i
				found = true
				break
			}
		}
		if !found {
			m.message = "Team changed; reopen Actions to review its current identity/version."
			return m, nil
		}
	}
	if a.taskID != "" {
		found := false
		for i, t := range m.data.activity.Tasks {
			if t.ID == a.taskID && t.Version == a.version {
				m.task = i
				found = true
				break
			}
		}
		if !found {
			m.message = "Task changed; reopen Actions to review its exact current version."
			return m, nil
		}
	}
	if a.recipient != "" {
		found := false
		for i, v := range m.agents {
			if v.ID == a.recipient {
				m.target = i
				found = true
				break
			}
		}
		if !found {
			m.message = "Recipient disappeared; reopen Actions."
			return m, nil
		}
	}
	m.composer = ""
	m.text = nil
	if a.kind == "disable-group" {
		m.reviewGroupDisable(a.target)
		return m, nil
	}
	if a.target != "" {
		found := false
		for i, v := range m.agents {
			if v.ID == a.target {
				m.explicitSessionChoice(i)
				if a.tab == 6 {
					m.teamMemberIndex = i
				}
				found = true
				break
			}
		}
		if !found {
			m.message = "Action target disappeared; reopen Actions."
			return m, nil
		}
	}
	m.tab = a.tab
	m.closeMemory()
	if selected := m.selectedAgent(); selected != nil {
		m.data.headless = !selected.External && m.data.runtime.Agents[selected.ID].PaneID == "" && (m.data.research != nil || m.data.runtime.AttemptID == "" && selected.Config.Interaction != "interactive")
	}
	switch a.key {
	case "retire":
		m.reviewRetire()
	case "exposure":
		cmd := m.readExposureLedger()
		return m, cmd
	case "resolve-fail":
		m.reviewTaskResolution("fail")
	case "resolve-retry":
		m.reviewTaskResolution("retry")
	case "attention":
		m.attentionOnly = !m.attentionOnly
		m.openSessionSwitcher()
		return m, m.fetch
	case "new-advanced":
		m.startNewSession(true)
	case "research-details":
		m.openResearchDetails()
	case "study-details":
		m.openStudyDetails()
	case "study-follow":
		m.studyFollow = !m.studyFollow
		return m, m.fetch
	case "study-current":
		return m.openCurrentStudyTrial()
	default:
		if a.key == "enter" {
			return m.key(tea.KeyMsg{Type: tea.KeyEnter})
		}
		return m.key(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(a.key)})
	}
	return m, nil
}
func (m console) actionsPanel(width, height int) string {
	rows := m.menuMatches()
	var out strings.Builder
	out.WriteString(consoleAccent.Render("Actions · exact targets") + "\nSearch: " + consoleEditor(m.text, m.textCursor, max(1, width-9), 1) + "\n\n")
	start := max(0, m.actionIndex-max(1, (height-4)/3)+1)
	for i := start; i < len(rows); i++ {
		a := rows[i]
		prefix := "  "
		if i == m.actionIndex%max(1, len(rows)) {
			prefix = "› "
		}
		out.WriteString(consoleMiddle(prefix+a.label, width) + "\n")
		shortcut := a.key
		if len(a.key) == 1 && a.key >= "A" && a.key <= "Z" {
			shortcut = "Shift+" + a.key
		}
		detail := a.target + " · " + shortcut
		if a.recipient != "" {
			detail = a.target + " → " + a.recipient + " · " + shortcut
		}
		if a.teamID != "" {
			detail = a.teamID + " → " + a.target + " · " + shortcut
		}
		if a.taskID != "" {
			detail = a.taskID + fmt.Sprintf(" v%d · ", a.version) + a.target
		}
		if a.reason != "" {
			detail = "Unavailable: " + a.reason
		}
		out.WriteString(consoleMuted.Render(consoleMiddle(detail, width)) + "\n\n")
	}
	return consoleClip(out.String(), width, height)
}
func consoleMiddle(text string, width int) string {
	text = consoleContent(text, 1<<20)
	if ansi.StringWidth(text) <= width {
		return text
	}
	if width < 3 {
		return ansi.Truncate(text, max(1, width), "")
	}
	left := (width - 1) / 2
	right := width - 1 - left
	runes := []rune(text)
	tail := ""
	for i := len(runes) - 1; i >= 0; i-- {
		if ansi.StringWidth(string(runes[i])+tail) > right {
			break
		}
		tail = string(runes[i]) + tail
	}
	return ansi.Truncate(text, left, "") + "…" + tail
}
func (m *console) startNewSession(advanced bool) {
	provider := "claude"
	nativeOnly := false
	if a := m.selectedAgent(); a != nil {
		switch a.Config.Harness {
		case "claude", "codex", "pi", "antigravity":
			provider = a.Config.Harness
		}
		nativeOnly = a.Config.NativeOnly
	}
	m.newNative = consoleNativeDraft{provider: provider, project: m.selectedProject(), nativeOnly: nativeOnly || provider == "antigravity", advanced: advanced}
	m.compose("new-title", "Optional title · Enter reviews inherited provider/project · Ctrl+O advanced", "", "")
}
func (m *console) openResearchDetails() {
	if m.data.research == nil {
		m.message = "No research trial is attached to this workspace."
		return
	}
	p := m.data.research.Progress
	var out strings.Builder
	fmt.Fprintf(&out, "Trial: %s\nCondition: %s\nAttempt: %d\nState directory: %s\nDeadline: %s\nAdmitted population (%d):\n%s\n\nTask references:\n", p.Trial, p.Condition, p.Attempt, p.StateDir, p.Deadline, len(p.Population), strings.Join(p.Population, "\n"))
	keys := []string{}
	for k := range p.Tasks {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Fprintf(&out, "%s · %s · task %s\n", k, p.Tasks[k], p.TaskIDs[k])
	}
	m.reader = &consoleReader{title: "Research trial details", body: out.String()}
}

func (m console) menuActionReason(action consoleMenuAction) string {
	read := action.key == "exposure" || action.key == "o" || action.key == "f" || action.key == "attention" || strings.HasPrefix(action.key, "study-") || action.key == "research-details"
	if !read && (m.offline || m.busy) {
		return "Reconnect and wait for pending changes"
	}
	var target *managed.AgentInfo
	for _, a := range m.agents {
		if a.ID == action.target {
			copy := a
			target = &copy
			break
		}
	}
	switch action.key {
	case "N", "new-advanced", "attention", "f", "study-follow":
		return ""
	case "research-details":
		if m.data.research == nil {
			return "No research trial in this workspace"
		}
	case "study-details", "study-current":
		if m.data.study == nil {
			return "No validated study output"
		}
		if action.key == "study-current" && !m.data.study.Live {
			return "Waiting for a live trial"
		}
	case "retire":
		if target == nil || target.State != "revoked" {
			return "Revoke and verify process shutdown first"
		}
	case "D", "A":
		if target == nil || target.State != "active" || target.Config.Interaction != "interactive" || target.External {
			return "Requires an active owned native session"
		}
		if action.key == "A" {
			n, ok := m.data.nativeStates[target.ID]
			if !ok || n.Policy.Mode != "review" || len(n.Pending) == 0 {
				return "No pending native request in Review mode"
			}
		}
	case "V":
		if target != nil && target.Config.NativeOnly {
			return "Native-only session has no injected role"
		}
	case "U":
		if target == nil || target.External || target.Config.Interaction != "interactive" {
			return "Requires an owned native session"
		}
		if m.data.runtime.Agents[target.ID].State == "running" {
			return "Exit the native CLI before relaunch"
		}
	case "x":
		if target == nil || target.State != "active" {
			return "Requires an active session"
		}
	case "c":
		if action.recipient == action.target {
			return "Select a distinct recipient in Connections"
		}
		for _, a := range m.agents {
			if a.ID == action.recipient && a.State != "active" {
				return "Recipient is not active"
			}
		}
		if target != nil && target.State != "active" {
			return "Sender is not active"
		}
	case "resolve-fail", "resolve-retry":
		if action.taskID == "" {
			return "Select an uncertain task"
		}
		for _, t := range m.data.activity.Tasks {
			if t.ID == action.taskID {
				if t.Delivery != "uncertain" && t.Delivery != "cancel_uncertain" {
					return "Task delivery is not uncertain"
				}
				if action.key == "resolve-retry" && t.Delivery == "cancel_uncertain" {
					return "Cancellation uncertainty supports fail resolution"
				}
			}
		}
	}
	return ""
}
