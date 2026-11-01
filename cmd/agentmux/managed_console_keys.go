package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"agentmux/internal/managed"
	"agentmux/internal/managedconfig"
	"agentmux/internal/managedlaunch"
	tea "github.com/charmbracelet/bubbletea"
)

func (m console) boardMessages() []managed.Message {
	out := []managed.Message{}
	for _, v := range m.data.activity.Messages {
		if m.boardFilter == 1 && (m.selectedBoard() == "" || v.Board != m.selectedBoard()) {
			continue
		}
		if m.boardFilter == 2 {
			visible := v.From == m.selectedID()
			for _, to := range v.To {
				visible = visible || to == m.selectedID()
			}
			if !visible || v.Board != "" {
				continue
			}
		}
		out = append(out, v)
	}
	return out
}
func (m *console) closeMemory() {
	m.memoryRequest++
	m.memoryOpen = false
	m.memoryEntries = nil
	m.memoryNamespace = ""
	if m.memoryLoading {
		m.memoryLoading = false
		m.busy = false
	}
}
func (m *console) loadMemory(namespace string) tea.Cmd {
	m.memoryRequest++
	m.memoryLoading, m.busy = true, true
	agent, request, client := m.selectedID(), m.memoryRequest, m.client
	return func() tea.Msg {
		entries := []managed.Memory{}
		cursor := ""
		seen := map[string]bool{}
		for {
			raw, err := workCall(context.Background(), client, "memory.list", map[string]any{"namespace": namespace, "limit": 100, "after": cursor})
			if err != nil {
				return consoleMemory{agent: agent, request: request, namespace: namespace, err: err}
			}
			var result struct {
				Entries []managed.Memory `json:"entries"`
				Cursor  string           `json:"cursor"`
			}
			if err = json.Unmarshal(raw, &result); err != nil {
				return consoleMemory{agent: agent, request: request, namespace: namespace, err: err}
			}
			for _, entry := range result.Entries {
				if !seen[entry.Key] {
					entries = append(entries, entry)
					seen[entry.Key] = true
				}
			}
			if len(result.Entries) == 0 || result.Cursor == "" || result.Cursor == cursor {
				break
			}
			cursor = result.Cursor
		}
		return consoleMemory{agent: agent, request: request, namespace: namespace, entries: entries}
	}
}
func (m console) close() tea.Cmd {
	if !m.popup && strings.HasPrefix(os.Getenv("TMUX"), managedlaunch.SocketPath(m.state)+",") {
		return func() tea.Msg {
			data, err := exec.Command("tmux", "-S", managedlaunch.SocketPath(m.state), "list-clients", "-F", "#{client_name}\t#{pane_id}\t#{client_activity}").Output()
			if err != nil {
				return consoleAction{err: err}
			}
			client := consoleDetachClient(string(data), os.Getenv("TMUX_PANE"), os.Getenv("AGENTMUX_CLIENT"))
			if client == "" {
				return consoleAction{message: "No client is viewing this workbench; native terminals remain running."}
			}
			err = exec.Command("tmux", "-S", managedlaunch.SocketPath(m.state), "detach-client", "-t", client).Run()
			return consoleAction{message: "Workbench detached; agents remain running.", err: err}
		}
	}
	return tea.Quit
}
func (m *console) compose(kind, target, reference, text string) {
	m.composer = kind
	m.composeTarget = target
	m.replyID = reference
	m.text = []rune(text)
	m.textCursor = len(m.text)
	m.textColumnValid = false
}
func (m console) key(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if msg.Paste && m.composer == "" && (m.teamAction == nil || m.teamAction.researchAction == "" || m.teamAction.reasonReady) {
		// Even a one-character clipboard paste is text, never a control key.
		return m, nil
	}
	// A terminal read may coalesce quick keys (jjj or /query) into one rune
	// message. Reduce actual keystrokes in order, then insert remaining editor
	// text as one batch. Bracketed pastes outside an editor remain inert.
	if m.composer == "" && msg.Type == tea.KeyRunes && !msg.Alt && !msg.Paste && len(msg.Runes) > 1 {
		var commands []tea.Cmd
		for i, r := range msg.Runes {
			next, cmd := m.key(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
			m = next.(console)
			if cmd != nil {
				commands = append(commands, cmd)
			}
			if m.composer != "" && i+1 < len(msg.Runes) {
				next, cmd = m.key(tea.KeyMsg{Type: tea.KeyRunes, Runes: msg.Runes[i+1:]})
				m = next.(console)
				if cmd != nil {
					commands = append(commands, cmd)
				}
				break
			}
		}
		return m, tea.Batch(commands...)
	}
	key := msg.String()
	if m.reader != nil {
		return m.readerKey(msg)
	}
	if m.teamAction != nil {
		return m.teamReviewKey(msg)
	}
	if m.composer != "" {
		if key != "up" && key != "down" {
			m.textColumnValid = false
		}
		switch key {
		case "esc":
			m.composer = ""
			m.text = nil
		case "ctrl+c":
			return m, m.close()
		case "ctrl+u":
			m.text = nil
			m.textCursor = 0
			m.findIndex = 0
		case "up", "down":
			if m.composer == "find" {
				if n := len(m.sessionMatches()); n > 0 {
					step := 1
					if key == "up" {
						step = n - 1
					}
					m.findIndex = (m.findIndex + step) % n
				}
			} else {
				width := m.width
				if width <= 0 {
					width = 100
				}
				step := 1
				if key == "up" {
					step = -1
				}
				m.textCursor, m.textColumn = consoleEditorMove(m.text, m.textCursor, width, step, m.textColumn, m.textColumnValid)
				m.textColumnValid = true
			}
		case "left":
			if m.textCursor > 0 {
				m.textCursor--
			}
		case "right":
			if m.textCursor < len(m.text) {
				m.textCursor++
			}
		case "home", "end":
			m.textCursor = consoleEditorLineEdge(m.text, m.textCursor, key == "end")
		case "ctrl+a", "ctrl+home":
			m.textCursor = 0
		case "ctrl+e", "ctrl+end":
			m.textCursor = len(m.text)
		case "backspace", "ctrl+h":
			if m.textCursor > 0 {
				m.text = append(m.text[:m.textCursor-1], m.text[m.textCursor:]...)
				m.textCursor--
			}
		case "delete":
			if m.textCursor < len(m.text) {
				m.text = append(m.text[:m.textCursor], m.text[m.textCursor+1:]...)
			}
		case "ctrl+r":
			if m.composer == "memory-value" && !m.busy {
				if m.offline {
					m.message = "Broker offline; draft kept. Review the latest memory version after reconnecting."
					return m, nil
				}
				m.busy = true
				return m, m.reviewMemoryVersion()
			}
		case "enter":
			if m.composer == "find" {
				matches := m.sessionMatches()
				if len(matches) == 0 {
					return m, nil
				}
				match := matches[m.findIndex%len(matches)]
				m.composer, m.details = "", ""
				m.text = nil
				if match.agent >= 0 {
					m.selected, m.tab = match.agent, 1
					m.scope = 0
					m.closeMemory()
					return m, m.fetch
				}
				m.observedSelected, m.tab = match.observation, 5
				return m, nil
			}
			if m.busy || strings.TrimSpace(string(m.text)) == "" && m.composer != "new-role" && m.composer != "new-title" && m.composer != "label" {
				return m, nil
			}
			if m.offline {
				m.message = "Broker offline. Draft kept; refresh before submitting."
				return m, nil
			}
			if strings.HasPrefix(m.composer, "new-") {
				return m.nativeDraftNext()
			}
			if m.composer == "memory-key" {
				m.memoryKey = string(m.text)
				m.compose("memory-value", m.memoryNamespace+"/"+m.memoryKey, "", "")
				return m, nil
			}
			text, target, kind, reference := string(m.text), m.composeTarget, m.composer, m.replyID
			draft := &consoleDraft{kind: kind, target: target, reference: reference, text: text, namespace: m.memoryNamespace, key: m.memoryKey, version: m.memoryVersion}
			if reference != "" {
				text = "Follow-up to " + reference + ":\n" + text
			}
			namespace, memoryKey, version := m.memoryNamespace, m.memoryKey, m.memoryVersion
			labelVersion := m.labelVersion
			m.composer = ""
			m.text = nil
			m.textCursor = 0
			m.busy = true
			return m, func() tea.Msg {
				if kind == "label" {
					_, err := workCall(context.Background(), m.client, "agent.label", map[string]any{"id": target, "title": text, "version": labelVersion})
					return consoleAction{message: "Session title saved; permanent ID and native conversation unchanged.", labelID: target, labelText: text, err: err}
				}
				if kind == "memory-value" {
					_, err := workCall(context.Background(), m.client, "memory.put", map[string]any{"namespace": namespace, "key": memoryKey, "value": text, "version": version})
					return consoleAction{message: "Memory saved with version checking. Press v to refresh.", err: err, draft: draft}
				}
				if kind == "board" {
					_, err := workCall(context.Background(), m.client, "board.post", map[string]string{"board": target, "text": text})
					return consoleAction{message: "Posted to #" + target + ".", err: err, draft: draft}
				}
				id, label, err := workSend(context.Background(), m.client, m.state, target, text)
				return consoleAction{message: label + " queued for " + target + ": " + id, err: err, draft: draft}
			}
		default:
			runes := msg.Runes
			if key == " " {
				runes = []rune{' '}
			} else if key == "alt+enter" {
				runes = []rune{'\n'}
			} else if msg.Type != tea.KeyRunes && key != " " {
				return m, nil
			}
			if len(m.text)+len(runes) <= 16384 {
				m.findIndex = 0
				m.text = append(m.text[:m.textCursor], append(append([]rune{}, runes...), m.text[m.textCursor:]...)...)
				m.textCursor += len(runes)
			} else {
				m.message = fmt.Sprintf("Input limit: 16,384 characters (draft has %d). Paste was not inserted; your draft is kept. Shorten it before adding more text.", len(m.text))
			}
		}
		return m, nil
	}
	switch key {
	case "ctrl+r":
		m.restoreDraft()
	case "o":
		m.openReader()
	case "P", "R", "S", "T":
		if m.tab == 7 {
			action := map[string]string{"P": "pause", "R": "resume", "S": "step", "T": "stop"}[key]
			m.reviewResearchAction(action)
			return m, nil
		}
	case "/":
		m.findIndex = 0
		m.compose("find", "task / project / provider / ID", "", "")
	case "L":
		if !m.busy && m.selectedID() != "" && m.tab != 5 {
			a := m.agents[m.selected%len(m.agents)]
			m.labelVersion = a.TitleVersion
			title := a.Title
			if draft, ok := m.labelDrafts[a.ID]; ok {
				title = draft
			}
			m.compose("label", a.ID+" (empty clears title)", "", title)
			// The submitted target must be the ID alone, not its display hint.
			m.composeTarget = a.ID
		}
	case "q", "ctrl+c":
		return m, m.close()
	case "esc":
		m.closeMemory()
	case "home":
		if m.tab == 7 {
			m.eventIndex = 0
		}
	case "up", "k":
		if m.tab == 5 {
			if m.observedSelected > 0 {
				m.observedSelected--
				m.details = ""
			}
			return m, nil
		}
		if m.selected > 0 {
			m.selected--
			m.scope = 0
			m.closeMemory()
			return m, m.fetch
		}
	case "down", "j":
		if m.tab == 5 {
			if m.observedSelected+1 < len(m.observedSessions()) {
				m.observedSelected++
				m.details = ""
			}
			return m, nil
		}
		if m.selected+1 < len(m.agents) {
			m.selected++
			m.scope = 0
			m.closeMemory()
			return m, m.fetch
		}
	case "tab":
		m.closeMemory()
		m.tab = (m.tab + 1) % 8
	case "shift+tab":
		m.closeMemory()
		m.tab = (m.tab + 7) % 8
	case "1", "2", "3", "4", "5", "6", "7", "8":
		if m.tab != int(key[0]-'1') {
			m.closeMemory()
		}
		m.tab = int(key[0] - '1')
	case "enter":
		if m.tab == 6 && len(m.agents) > 0 {
			m.selected = m.teamMemberIndex % len(m.agents)
			m.closeMemory()
			a := m.agents[m.selected]
			m.data.headless = !a.External && m.data.runtime.Agents[a.ID].PaneID == "" && (m.data.research != nil || m.data.runtime.AttemptID == "" && a.Config.Interaction != "interactive")
			return m, m.attach()
		}
		if m.tab == 5 {
			if observations := m.observedSessions(); len(observations) > 0 {
				v := observations[m.observedSelected%len(observations)]
				m.details = observedTitle(v) + "\nObserved ID: " + v.ID + "\nProject: " + v.Project + "\nStarted: " + sessionAdded(v.StartedAt) + "\nOriginal terminal: " + v.TTY + "\nTmux: " + v.TmuxSession + "\n" + v.AttachCommand + "\nConnect bridge: press c, then copy the displayed setup into the existing native conversation. No terminal input is injected."
			}
			return m, nil
		}
		if m.selectedID() != "" {
			return m, m.attach()
		}
	case "s":
		if m.selectedID() != "" {
			m.compose("send", m.selectedID(), "", "")
		}
	case "b":
		if m.tab == 6 {
			if team := m.selectedTeam(); team != nil && team.State == "active" {
				m.compose("board", team.Board, "", "")
			}
			return m, nil
		}
		if board := m.selectedBoard(); board != "" {
			m.compose("board", board, "", "")
		} else {
			m.message = "No board is shared with this agent. Connections → c enables a scoped collaboration group."
		}
	case "+", "=", "-":
		boards := m.availableBoards()
		if len(boards) > 0 {
			step := 1
			if key == "-" {
				step = len(boards) - 1
			}
			m.boardSelected = (m.boardSelected + step) % len(boards)
			m.message = "Board: #" + m.selectedBoard()
			m.messageIndex = 0
		}
	case "N":
		if !m.busy {
			m.newNative = consoleNativeDraft{provider: "claude", project: m.selectedProject()}
			m.compose("new-title", "What are you working on? (optional)", "", "")
		}
	case "x":
		if m.tab == 5 {
			m.message = "Observed sessions are read-only. Select a registered bridge in Agents to disconnect it; its native terminal keeps running."
			return m, nil
		}
		if !m.busy && len(m.agents) > 0 {
			a := m.agents[m.selected%len(m.agents)]
			m.busy = true
			return m, func() tea.Msg {
				if m.offline {
					return consoleAction{err: errors.New("broker offline; refresh before making changes")}
				}
				raw, err := workCall(context.Background(), m.client, "agent.stop", map[string]string{"id": a.ID})
				if err != nil {
					return consoleAction{err: err}
				}
				var result struct {
					RevokedAgents int `json:"revoked_agents"`
				}
				if err := json.Unmarshal(raw, &result); err != nil {
					return consoleAction{err: err}
				}
				return consoleAction{message: consoleRevocationMessage(a.ID, result.RevokedAgents)}
			}
		}
	case "f":
		if m.tab == 0 {
			m.boardFilter = (m.boardFilter + 1) % 3
			m.messageIndex = 0
		}
	case "]":
		m.stepContent(1)
	case "[":
		m.stepContent(-1)
	case "right", "l":
		if m.tab == 6 && len(m.agents) > 0 {
			m.teamMemberIndex = (m.teamMemberIndex + 1) % len(m.agents)
			return m, nil
		}
		if len(m.agents) > 0 {
			m.target = (m.target + 1) % len(m.agents)
		}
	case "left", "h":
		if m.tab == 6 && len(m.agents) > 0 {
			m.teamMemberIndex = (m.teamMemberIndex + len(m.agents) - 1) % len(m.agents)
			return m, nil
		}
		if len(m.agents) > 0 {
			m.target = (m.target + len(m.agents) - 1) % len(m.agents)
		}
	case "r":
		if m.tab == 6 {
			m.reviewTeamAction("team.remove")
			return m, nil
		}
		if m.tab == 4 && len(m.data.activity.Tasks) > 0 {
			v := m.data.activity.Tasks[m.task%len(m.data.activity.Tasks)]
			m.compose("reply", v.To, v.ID, "")
		}
		if m.tab == 0 && len(m.boardMessages()) > 0 {
			v := m.boardMessages()[m.messageIndex%len(m.boardMessages())]
			if v.Board != "" {
				m.compose("board", v.Board, v.ID, "")
			} else if v.From != "$owner" {
				m.compose("reply", v.From, v.ID, "")
			} else if len(v.To) == 1 {
				m.compose("reply", v.To[0], v.ID, "")
			}
		}
		if m.tab == 3 && !m.memoryOpen {
			cmd := m.toggleScope(false)
			return m, cmd
		}
	case "w":
		if m.tab == 3 && !m.memoryOpen {
			cmd := m.toggleScope(true)
			return m, cmd
		}
	case "v":
		if m.tab == 6 && !m.busy {
			if team := m.selectedTeam(); team != nil {
				m.tab = 3
				return m, m.loadMemory(team.Memory)
			}
		}
		if m.tab == 3 && !m.busy {
			if m.data.boundaries.Agent != m.selectedID() {
				m.message = "Loading the selected session's current memory boundaries."
				return m, nil
			}
			if m.memoryOpen {
				cmd := m.loadMemory(m.memoryNamespace)
				return m, cmd
			}
			scopes := append(append([]managed.BoundaryScope{}, m.data.boundaries.Boards...), m.data.boundaries.Memory...)
			if len(scopes) > 0 {
				index := m.scope % len(scopes)
				if index >= len(m.data.boundaries.Boards) {
					cmd := m.loadMemory(scopes[index].Name)
					return m, cmd
				}
				m.message = "Select a memory namespace, then v. Board notes are in Board."
			}
		}
	case "e":
		if m.tab == 3 && m.memoryOpen && len(m.memoryEntries) > 0 {
			v := m.memoryEntries[m.memoryEntry%len(m.memoryEntries)]
			m.memoryKey = v.Key
			m.memoryVersion = v.Version
			m.compose("memory-value", m.memoryNamespace+"/"+v.Key, "", v.Value)
		}
	case "n":
		if m.tab == 3 && m.memoryOpen {
			m.memoryVersion = 0
			m.compose("memory-key", m.memoryNamespace, "", "")
		}
	case "m", "p", "i":
		if m.tab == 6 && key == "i" {
			m.reviewTeamAction("team.invite")
			return m, nil
		}
		if m.tab == 2 && !m.busy && len(m.agents) > 0 && m.data.boundaries.Agent == m.selectedID() {
			action := map[string]string{"m": "message.send", "p": "ping.send", "i": "instruct"}[key]
			target := m.agents[m.target%len(m.agents)].ID
			allow := !boundaryAllows(m.data.boundaries, target, action)
			m.busy = true
			return m, m.mutate("control.update", map[string]any{"version": m.data.boundaries.Version, "kind": "edge", "from": m.selectedID(), "to": target, "action": action, "allow": allow}, "Permissions saved and enforced immediately.")
		}
	case "c":
		if m.offline {
			m.message = "Broker offline; refresh before making changes."
			return m, nil
		}
		if observations := m.observedSessions(); m.tab == 5 && !m.busy && len(observations) > 0 {
			v := observations[m.observedSelected%len(observations)]
			state := m.state
			id := fmt.Sprintf("%s_%d", v.Harness, v.PID)
			m.busy = true
			return m, func() tea.Msg {
				setup, err := connectObservedSession(context.Background(), state, v.ID, id)
				details := "Print setup in a shell:\n" + managedlaunch.ShellCommand([]string{"agentmux", "connect", v.ID, "--name", id, "--state", state}) + "\n\n" + setup
				return consoleAction{message: "Bridge prepared as " + id + ". Paste the setup in its original native session; readiness is not confirmed.", details: details, err: err}
			}
		}
		if m.tab == 2 && !m.busy && len(m.agents) > 0 {
			from, to := m.selectedID(), m.agents[m.target%len(m.agents)].ID
			if from == to {
				m.message = "Choose another recipient with ←/→ before connecting."
				return m, nil
			}
			client := m.client
			m.busy = true
			return m, func() tea.Msg {
				raw, err := workCollaboration(context.Background(), client, []string{from, to}, false)
				var c managed.ControlState
				if err == nil {
					err = json.Unmarshal(raw, &c)
				}
				label := "Scoped collaboration enabled for " + from + " and " + to
				if c.Group != nil {
					label += ". Board #" + c.Group.Board + " · memory " + c.Group.Memory
				}
				return consoleAction{message: label, err: err}
			}
		}
		if m.tab == 4 && !m.busy && len(m.data.activity.Tasks) > 0 {
			v := m.data.activity.Tasks[m.task%len(m.data.activity.Tasks)]
			m.busy = true
			return m, m.mutate("task.cancel", map[string]any{"id": v.ID, "version": v.Version}, "Cancellation requested. Check delivery acknowledgement.")
		}
	case "d":
		if m.offline {
			m.message = "Broker offline; refresh before making changes."
			return m, nil
		}
		if m.tab == 6 {
			m.reviewTeamAction("team.dissolve")
			return m, nil
		}
		if m.tab == 2 && !m.busy && m.selectedID() != "" {
			id, client := m.selectedID(), m.client
			m.busy = true
			return m, func() tea.Msg {
				_, err := workCollaboration(context.Background(), client, []string{id}, true)
				return consoleAction{message: id + " disconnected from peer coordination and shared memory. Native interaction continues; previously read data and accepted tasks remain.", err: err}
			}
		}
	case "a":
		if m.offline {
			m.message = "Broker offline; refresh before making changes."
			return m, nil
		}
		if (m.tab == 0 || m.tab == 1) && !m.busy && m.selectedID() != "" {
			if !m.agents[m.selected%len(m.agents)].Config.Autonomy.Enabled {
				m.message = "Automatic wakeups are disabled. Native interaction stays in the agent terminal."
				return m, nil
			}
			id := m.selectedID()
			m.busy = true
			return m, func() tea.Msg {
				raw, err := workCall(context.Background(), m.client, "autonomy.status", map[string]string{"agent": id})
				if err != nil {
					return consoleAction{err: err}
				}
				var result struct {
					Status managed.AutonomyStatus `json:"status"`
				}
				if err = json.Unmarshal(raw, &result); err != nil {
					return consoleAction{err: err}
				}
				mode := "paused"
				if result.Status.Mode != "running" {
					mode = "running"
				}
				_, err = workCall(context.Background(), m.client, "autonomy.control", map[string]any{"agent": id, "version": result.Status.Version, "mode": mode})
				return consoleAction{message: "Managed autonomy: " + mode + ". In-flight work uses task cancellation.", err: err}
			}
		}
	}
	return m, nil
}

func consoleDetachClient(data, pane, explicit string) string {
	if pane == "" {
		return ""
	}
	chosen := ""
	var latest int64 = -1
	for _, line := range strings.Split(data, "\n") {
		f := strings.Split(line, "\t")
		if len(f) != 3 || f[1] != pane || explicit != "" && f[0] != explicit {
			continue
		}
		stamp, err := strconv.ParseInt(f[2], 10, 64)
		if err == nil && stamp >= latest {
			chosen, latest = f[0], stamp
		}
	}
	return chosen
}
func (m console) availableBoards() []string {
	out := []string{}
	if m.data.boundaries.Agent != m.selectedID() {
		return out
	}
	for _, v := range m.data.boundaries.Boards {
		if v.Read || v.Write {
			out = append(out, v.Name)
		}
	}
	return out
}
func (m console) selectedBoard() string {
	boards := m.availableBoards()
	if len(boards) == 0 {
		return ""
	}
	return boards[m.boardSelected%len(boards)]
}

func (m console) nativeDraftNext() (tea.Model, tea.Cmd) {
	text := strings.TrimSpace(string(m.text))
	switch m.composer {
	case "new-title":
		if err := managed.ValidateSessionTitle(text); err != nil {
			m.message = err.Error()
			return m, nil
		}
		m.newNative.title = text
		m.compose("new-provider", "claude / codex / pi", "", m.newNative.provider)
	case "new-id":
		if !managedconfig.ValidID(text) {
			m.message = "Use an ID starting with a letter, followed by letters, digits, - or _."
			return m, nil
		}
		for _, a := range m.agents {
			if a.ID == text {
				m.message = "Session ID already exists. Choose a new stable ID."
				return m, nil
			}
		}
		m.newNative.id = text
		m.compose("new-provider", "claude / codex / pi", "", m.newNative.provider)
	case "new-provider":
		if text != "claude" && text != "codex" && text != "pi" {
			m.message = "Choose claude, codex or pi."
			return m, nil
		}
		m.newNative.provider = text
		m.compose("new-project", "project directory", "", m.newNative.project)
	case "new-project":
		m.newNative.project = text
		m.compose("new-role", "optional role (Enter to leave empty)", "", m.newNative.role)
	case "new-role":
		m.newNative.role = string(m.text)
		draft := m.newNative
		m.composer = ""
		m.text = nil
		m.busy = true
		return m, m.addNative(draft)
	default:
		return m, func() tea.Msg { return consoleAction{err: errors.New("unknown native session setup step")} }
	}
	return m, nil
}

func consoleRevocationMessage(id string, count int) string {
	if count == 0 {
		return id + " is already revoked. Process termination is not confirmed."
	}
	return fmt.Sprintf("Revoked %s and active descendants (%d sessions). Owned shutdown requested; external native terminals stay open. Process termination is not confirmed.", id, count)
}
