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
func (m console) loadMemory(namespace string) tea.Cmd {
	return func() tea.Msg {
		raw, err := workCall(context.Background(), m.client, "memory.list", map[string]any{"namespace": namespace, "limit": 100})
		if err != nil {
			return consoleMemory{err: err}
		}
		var result struct {
			Entries []managed.Memory `json:"entries"`
		}
		err = json.Unmarshal(raw, &result)
		return consoleMemory{namespace: namespace, entries: result.Entries, err: err}
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
}
func (m console) key(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	if m.composer != "" {
		switch key {
		case "esc":
			m.composer = ""
			m.text = nil
		case "ctrl+c":
			return m, m.close()
		case "ctrl+u":
			m.text = nil
			m.textCursor = 0
		case "left":
			if m.textCursor > 0 {
				m.textCursor--
			}
		case "right":
			if m.textCursor < len(m.text) {
				m.textCursor++
			}
		case "home", "ctrl+a":
			m.textCursor = 0
		case "end", "ctrl+e":
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
		case "enter":
			if m.busy || strings.TrimSpace(string(m.text)) == "" && m.composer != "new-role" {
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
			if reference != "" {
				text = "Follow-up to " + reference + ":\n" + text
			}
			namespace, memoryKey, version := m.memoryNamespace, m.memoryKey, m.memoryVersion
			m.composer = ""
			m.text = nil
			m.textCursor = 0
			m.busy = true
			return m, func() tea.Msg {
				if kind == "memory-value" {
					_, err := workCall(context.Background(), m.client, "memory.put", map[string]any{"namespace": namespace, "key": memoryKey, "value": text, "version": version})
					return consoleAction{message: "Memory saved with version checking. Press v to refresh.", err: err}
				}
				if kind == "board" {
					_, err := workCall(context.Background(), m.client, "board.post", map[string]string{"board": target, "text": text})
					return consoleAction{message: "Posted to #" + target + ".", err: err}
				}
				id, label, err := workSend(context.Background(), m.client, m.state, target, text)
				return consoleAction{message: label + " queued for " + target + ": " + id, err: err}
			}
		default:
			runes := msg.Runes
			if key == "alt+enter" {
				runes = []rune{'\n'}
			} else if msg.Type != tea.KeyRunes && key != " " {
				return m, nil
			}
			if len(m.text)+len(runes) <= 16384 {
				m.text = append(m.text[:m.textCursor], append(append([]rune{}, runes...), m.text[m.textCursor:]...)...)
				m.textCursor += len(runes)
			}
		}
		return m, nil
	}
	switch key {
	case "q", "ctrl+c":
		return m, m.close()
	case "esc":
		m.memoryOpen = false
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
			m.memoryOpen = false
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
			m.memoryOpen = false
			return m, m.fetch
		}
	case "tab":
		m.tab = (m.tab + 1) % 6
	case "shift+tab":
		m.tab = (m.tab + 5) % 6
	case "1", "2", "3", "4", "5", "6":
		m.tab = int(key[0] - '1')
	case "enter":
		if m.tab == 5 {
			if observations := m.observedSessions(); len(observations) > 0 {
				v := observations[m.observedSelected%len(observations)]
				m.details = "Observed ID: " + v.ID + "\nOriginal terminal: " + v.TTY + "\n" + v.AttachCommand + "\nConnect bridge: press c, then copy the displayed setup into the existing native conversation. No terminal input is injected."
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
			m.compose("new-id", "unique session ID", "", "")
		}
	case "x":
		if m.tab == 5 {
			m.message = "Observed sessions are read-only. Select a registered bridge in Agents to disconnect it; its native terminal keeps running."
			return m, nil
		}
		if !m.busy && len(m.agents) > 0 {
			a := m.agents[m.selected%len(m.agents)]
			m.busy = true
			label := "Closing " + a.ID + "; other native terminals remain running."
			if a.External {
				label = "Disconnected bridge " + a.ID + ". Its original native terminal keeps running."
			}
			return m, m.mutate("agent.stop", map[string]string{"id": a.ID}, label)
		}
	case "f":
		if m.tab == 0 {
			m.boardFilter = (m.boardFilter + 1) % 3
			m.messageIndex = 0
		}
	case "]":
		m.scope++
		m.task++
		m.messageIndex++
		m.memoryEntry++
	case "[":
		if m.scope > 0 {
			m.scope--
		}
		if m.task > 0 {
			m.task--
		}
		if m.messageIndex > 0 {
			m.messageIndex--
		}
		if m.memoryEntry > 0 {
			m.memoryEntry--
		}
	case "right", "l":
		if len(m.agents) > 0 {
			m.target = (m.target + 1) % len(m.agents)
		}
	case "left", "h":
		if len(m.agents) > 0 {
			m.target = (m.target + len(m.agents) - 1) % len(m.agents)
		}
	case "r":
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
		if m.tab == 3 && !m.busy {
			if m.memoryOpen {
				m.busy = true
				return m, m.loadMemory(m.memoryNamespace)
			}
			scopes := append(append([]managed.BoundaryScope{}, m.data.boundaries.Boards...), m.data.boundaries.Memory...)
			if len(scopes) > 0 {
				index := m.scope % len(scopes)
				if index >= len(m.data.boundaries.Boards) {
					m.busy = true
					return m, m.loadMemory(scopes[index].Name)
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
		if m.tab == 2 && !m.busy && len(m.agents) > 0 && m.data.boundaries.Agent == m.selectedID() {
			action := map[string]string{"m": "message.send", "p": "ping.send", "i": "instruct"}[key]
			target := m.agents[m.target%len(m.agents)].ID
			allow := !boundaryAllows(m.data.boundaries, target, action)
			m.busy = true
			return m, m.mutate("control.update", map[string]any{"version": m.data.boundaries.Version, "kind": "edge", "from": m.selectedID(), "to": target, "action": action, "allow": allow}, "Permissions saved and enforced immediately.")
		}
	case "c":
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
		if m.tab == 2 && !m.busy && m.selectedID() != "" {
			id, client := m.selectedID(), m.client
			m.busy = true
			return m, func() tea.Msg {
				_, err := workCollaboration(context.Background(), client, []string{id}, true)
				return consoleAction{message: id + " disconnected from peer coordination and shared memory. Native interaction continues; previously read data and accepted tasks remain.", err: err}
			}
		}
	case "a":
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
				if result.Status.Mode == "paused" {
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
		m.compose("new-role", "optional role (Enter to leave empty)", "", "")
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
