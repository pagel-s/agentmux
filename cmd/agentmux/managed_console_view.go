package main

import (
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode"

	"agentmux/internal/managed"
	"github.com/charmbracelet/lipgloss"
)

func consoleContent(value string, limit int) string {
	var out strings.Builder
	for i, r := range []rune(value) {
		if i >= limit {
			out.WriteString("…")
			break
		}
		if r == '\n' {
			out.WriteRune(r)
		} else if r == '\t' {
			out.WriteString("  ")
		} else if !unicode.IsPrint(r) || r == 0x061c || r == 0x200e || r == 0x200f || (r >= 0x202a && r <= 0x202e) || (r >= 0x2066 && r <= 0x2069) {
			quoted := strconv.QuoteRuneToASCII(r)
			out.WriteString(quoted[1 : len(quoted)-1])
		} else {
			out.WriteRune(r)
		}
	}
	return out.String()
}
func consoleTime(value string) string {
	t, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return ""
	}
	return t.Local().Format("15:04:05")
}
func consoleClip(value string, width, height int) string {
	if width < 1 {
		width = 1
	}
	if height < 1 {
		height = 1
	}
	return lipgloss.NewStyle().Width(width).MaxWidth(width).MaxHeight(height).Render(value)
}
func (m console) roster(width, height int) string {
	var out strings.Builder
	out.WriteString(consoleMuted.Render("AGENTS") + "\n\n")
	start := 0
	if m.selected > height/3-2 {
		start = m.selected - height/3 + 2
	}
	if start < 0 {
		start = 0
	}
	for i := start; i < len(m.agents); i++ {
		a := m.agents[i]
		mode := a.Config.Interaction
		if a.External {
			mode = "external bridge"
		}
		if mode == "" {
			mode = "managed"
		}
		status := a.State
		if a.External && a.State == "active" {
			status = observationStatus(a.ExternalRef, m.data.observed)
		}
		if pane, ok := m.data.runtime.Agents[a.ID]; ok {
			status = pane.State
		}
		name := "  " + a.ID
		if i == m.selected {
			name = consoleSelected.Render("› " + a.ID)
		}
		out.WriteString(consoleClip(name, width, 1) + "\n" + consoleMuted.Render(consoleClip("  "+mode+" · "+status, width, 1)) + "\n\n")
		if lipgloss.Height(out.String()) >= height-2 {
			break
		}
	}
	out.WriteString(consoleMuted.Render("↑/↓ select\nEnter open native CLI"))
	return consoleClip(out.String(), width, height)
}
func (m console) panel(width, height int) string {
	var out strings.Builder
	switch m.tab {
	case 0:
		board := m.selectedBoard()
		boardLabel := "No shared board"
		if board != "" {
			boardLabel = "#" + board
		}
		filters := []string{"All coordination", boardLabel, m.selectedID() + " inbox"}
		messages := m.boardMessages()
		fmt.Fprintf(&out, "%s  %s\n\n", consoleAccent.Render(filters[m.boardFilter%3]), consoleMuted.Render(fmt.Sprintf("%d recent · f filter · [/] select · r reply", len(messages))))
		if len(messages) == 0 {
			out.WriteString(consoleMuted.Render("No messages here yet.\n\nPress b to post to a shared board; +/- chooses the board.\nPress s to send to the selected agent.\nEnter opens its full native terminal.\nConnections → c enables a scoped collaboration group."))
		}
		if len(messages) > 0 {
			start := m.messageIndex % len(messages)
			for i := start; i < len(messages); i++ {
				v := messages[i]
				to := strings.Join(v.To, ", ")
				if v.Board != "" {
					to = "#" + m.scopeLabel(v.Board)
				}
				prefix := "  "
				if i == start {
					prefix = consoleAccent.Render("› ")
				}
				fmt.Fprintf(&out, "%s%s %s %s\n", prefix, consoleAccent.Render(v.From), consoleMuted.Render("→ "+to), consoleMuted.Render(consoleTime(v.CreatedAt)))
				body := consoleClip(consoleContent(v.Text, 1500), width-3, 6)
				out.WriteString(lipgloss.NewStyle().PaddingLeft(2).Render(body) + "\n\n")
				if lipgloss.Height(out.String()) > height-4 {
					break
				}
			}
		}
	case 1:
		fmt.Fprintf(&out, "%s  %s\n\n", consoleAccent.Render(m.selectedID()), consoleMuted.Render("Enter opens the native terminal"))
		if len(m.agents) > 0 {
			a := m.agents[m.selected%len(m.agents)]
			fmt.Fprintf(&out, "%s\n%s\n%s\n\n", consoleContent(a.Config.Role, 300), consoleMuted.Render(consoleContent(a.Config.Workdir, 200)), consoleMuted.Render(a.Config.Harness+" · "+a.Config.Sandbox+" · network "+a.Config.Network))
			if a.External {
				fmt.Fprintf(&out, "%s  %s\n\n%s\n\n%s\n", consoleAccent.Render("EXTERNAL NATIVE SESSION"), consoleMuted.Render(observationStatus(a.ExternalRef, m.data.observed)), consoleContent("Use its original terminal. Helper: "+m.bridgeHelper(a.ID), 500), consoleMuted.Render("Registration prepares a bridge; it does not prove the native agent read the setup.\nCopy the setup below into its existing conversation yourself."))
				out.WriteString("\n" + consoleContent(bridgeInstructions(m.state, a.ID), 6000))
				break
			}
		}
		out.WriteString(consoleMuted.Render("LIVE TERMINAL PREVIEW\n"))
		preview := strings.TrimSpace(m.data.preview)
		if preview == "" {
			preview = "Waiting for terminal output…"
		}
		out.WriteString(consoleContent(preview, 6000))
	case 2:
		out.WriteString(consoleAccent.Render("Connections") + consoleMuted.Render(fmt.Sprintf("  revision %d\n\n", m.data.boundaries.Version)))
		if len(m.agents) > 0 {
			target := m.agents[m.target%len(m.agents)].ID
			fmt.Fprintf(&out, "%s  →  %s\n%s\n\n", consoleAccent.Render(m.selectedID()), consoleAccent.Render(target), consoleMuted.Render("←/→ recipient · ↑/↓ sender"))
			for _, row := range []struct{ key, action, label string }{{"m", "message.send", "Messages"}, {"p", "ping.send", "Pings"}, {"i", "instruct", "Commands"}} {
				enabled := boundaryAllows(m.data.boundaries, target, row.action)
				value := "○  denied"
				if enabled {
					value = consoleAccent.Render("●  allowed")
				}
				fmt.Fprintf(&out, "  [%s]  %-13s %s\n", row.key, row.label, value)
			}
			out.WriteString("\n" + consoleMuted.Render("c connect these two with scoped board/memory · d disconnect sender\nKeys toggle live permissions. Commands create tasks.\nExisting tasks stay accepted; cancel them in Tasks.\nAgent context: managed call boundaries.get"))
			out.WriteString("\n\n" + consoleAccent.Render("Workspace groups") + "\n")
			found := false
			for _, g := range m.data.controls.Groups {
				member := false
				for _, id := range g.Agents {
					member = member || id == m.selectedID()
				}
				if !member {
					continue
				}
				found = true
				fmt.Fprintf(&out, "%s · %s\n  %s\n  board #%s\n  memory %s\n", consoleContent(g.ID, 64), g.State, strings.Join(g.Members, ", "), g.Board, g.Memory)
			}
			if !found {
				out.WriteString(consoleMuted.Render("No group yet. Choose a recipient, then c."))
			}
		}
	case 3:
		if m.memoryOpen {
			fmt.Fprintf(&out, "%s  %s\n\n", consoleAccent.Render("Memory / "+m.scopeLabel(m.memoryNamespace)), consoleMuted.Render("owner view · [/] entry · e edit · n new · v refresh · Esc back"))
			if len(m.memoryEntries) == 0 {
				out.WriteString(consoleMuted.Render("No entries. Press n to create a shared note."))
			}
			first := max(0, m.memoryEntry-8)
			for i := first; i < len(m.memoryEntries); i++ {
				v := m.memoryEntries[i]
				line := fmt.Sprintf("  %-24s v%d", consoleContent(v.Key, 24), v.Version)
				if i == m.memoryEntry%len(m.memoryEntries) {
					line = consoleSelected.Render(line)
				}
				out.WriteString(line + "\n")
				if i-first >= 8 {
					break
				}
			}
			if len(m.memoryEntries) > 0 {
				v := m.memoryEntries[m.memoryEntry%len(m.memoryEntries)]
				out.WriteString("\n" + consoleContent(v.Value, 4000))
			}
		} else {
			out.WriteString(consoleAccent.Render("Shared boundaries") + consoleMuted.Render("  [/] select · r read · w write · v inspect memory\n\n"))
			scopes := append(append([]managed.BoundaryScope{}, m.data.boundaries.Boards...), m.data.boundaries.Memory...)
			for i, v := range scopes {
				kind := "board"
				if i >= len(m.data.boundaries.Boards) {
					kind = "memory"
				}
				line := fmt.Sprintf("  %-7s %-18s read %-7s write %s", kind, m.scopeLabel(v.Name), accessLabel(v.Read), accessLabel(v.Write))
				if i == m.scope%len(scopes) {
					line = consoleSelected.Render(line)
				}
				out.WriteString(line + "\n")
			}
			out.WriteString("\n" + consoleMuted.Render("Private memory is separate for every agent.\nRevocation prevents future reads; past knowledge remains."))
		}
	case 4:
		out.WriteString(consoleAccent.Render("Tasks & results") + consoleMuted.Render("  [/] select · r follow up · c cancel\n\n"))
		first := max(0, m.task-5)
		for i := first; i < len(m.data.activity.Tasks); i++ {
			v := m.data.activity.Tasks[i]
			line := fmt.Sprintf("  %-16s %-12s %s", v.To, v.State, v.Delivery)
			if i == m.task%len(m.data.activity.Tasks) {
				line = consoleSelected.Render(line)
			}
			out.WriteString(line + "\n")
			if i-first >= 5 {
				break
			}
		}
		if len(m.data.activity.Tasks) > 0 {
			v := m.data.activity.Tasks[m.task%len(m.data.activity.Tasks)]
			out.WriteString("\n" + consoleMuted.Render(v.ID) + "\n" + consoleContent(v.Text, 1000) + "\n\n" + consoleContent(v.Result, 4000))
			if len(v.Usage) > 0 {
				out.WriteString("\n\n" + consoleMuted.Render("Provider-reported usage: ") + consoleContent(string(v.Usage), 500))
			}
		} else {
			out.WriteString(consoleMuted.Render("No managed tasks yet.\nHuman-led native sessions use their own conversation UI."))
		}
	case 5:
		observations := m.observedSessions()
		out.WriteString(consoleAccent.Render("Existing sessions") + consoleMuted.Render("  ↑/↓ select · Enter details · c prepare bridge\n\n"))
		out.WriteString(consoleMuted.Render("Observed live terminals keep their native conversation and tools.\nObservation grants no communication rights and sends no input.\n\n"))
		if len(observations) == 0 {
			out.WriteString(consoleMuted.Render("No unregistered live TTY sessions found.\nRegistered bridges appear in Agents under their assigned names.\nN creates a native session. Generic interpreter wrappers may not be identifiable.\nDiscovery is local and best effort; custom tmux servers may be absent."))
		}
		first, count := max(0, m.observedSelected-4), 5
		if m.details != "" {
			first, count = m.observedSelected, 1
		}
		for i := first; i < len(observations) && i < first+count; i++ {
			v := observations[i]
			line := fmt.Sprintf("  %s · %s · %s", v.Harness, v.Source, v.TTY)
			if i == m.observedSelected {
				line = consoleSelected.Render(line)
			}
			fmt.Fprintf(&out, "%s\n  %s\n  %s\n", line, consoleContent(v.ID, 100), consoleMuted.Render(consoleContent(v.Project, 200)))
		}
		if m.details != "" {
			out.WriteString("\n" + consoleAccent.Render("Manual setup / terminal details") + "\n" + consoleContent(m.details, 7000))
		}
	}
	return consoleClip(out.String(), width, height)
}
func (m console) View() string {
	width, height := m.width, m.height
	if width <= 0 {
		width = 100
	}
	if height <= 0 {
		height = 30
	}
	header := consoleAccent.Render(" AGENTMUX ") + " " + consoleContent(m.data.runtime.Session, 60) + "  " + consoleMuted.Render(m.data.runtime.State)
	tabs := []string{"1 Board", "2 Agents", "3 Connections", "4 Memory", "5 Tasks", "6 Sessions"}
	var navigation strings.Builder
	for i, label := range tabs {
		if i == m.tab {
			navigation.WriteString(consoleSelected.Render(" " + label + " "))
		} else {
			navigation.WriteString(consoleMuted.Render(" " + label + " "))
		}
	}
	var footer strings.Builder
	if m.composer != "" {
		label := m.composer + " → " + m.composeTarget
		if m.replyID != "" {
			label += " · reply " + m.replyID
		}
		cursor := m.textCursor
		if cursor < 0 || cursor > len(m.text) {
			cursor = len(m.text)
		}
		text := consoleContent(string(m.text[:cursor]), 3000) + consoleAccent.Render("▏") + consoleContent(string(m.text[cursor:]), 3000)
		footer.WriteString(consoleAccent.Render(label) + "\n" + consoleClip(text, width, 4) + "\n" + consoleMuted.Render("Enter submits · Alt-Enter newline · Esc cancels · Ctrl-U clears") + "\n")
	}
	if m.busy {
		footer.WriteString(consoleMuted.Render("Applying…") + "\n")
	} else if m.message != "" {
		footer.WriteString(consoleClip(consoleContent(m.message, 500), width, 2) + "\n")
	}
	footer.WriteString(consoleMuted.Render("↑/↓ select · Enter native CLI · Tab views · F1 workbench\nN new · x close/disconnect · s send · b board · +/- board · q detach/close"))
	bottom := consoleClip(footer.String(), width, height/2)
	bodyHeight := height - lipgloss.Height(bottom) - 4
	if bodyHeight < 1 {
		bodyHeight = 1
	}
	var compact string
	if width < 85 && len(m.agents) > 0 {
		first := max(0, m.selected-1)
		for i := first; i < len(m.agents) && i < first+3; i++ {
			label := " " + consoleContent(m.agents[i].ID, 16) + " "
			if i == m.selected {
				compact += consoleSelected.Render(label)
			} else {
				compact += consoleMuted.Render(label)
			}
		}
		compact = consoleClip(compact, width, 1) + "\n"
		bodyHeight = max(1, bodyHeight-1)
	}
	var body string
	if width >= 85 {
		sideWidth := 23
		separator := consoleMuted.Render("│")
		bar := strings.TrimSuffix(strings.Repeat(separator+"\n", bodyHeight), "\n")
		right := lipgloss.NewStyle().PaddingLeft(2).Render(m.panel(width-sideWidth-4, bodyHeight))
		body = lipgloss.JoinHorizontal(lipgloss.Top, m.roster(sideWidth, bodyHeight), " "+bar, right)
	} else {
		body = m.panel(width, bodyHeight)
	}
	return consoleClip(header, width, 1) + "\n" + consoleClip(navigation.String(), width, 1) + "\n\n" + compact + consoleClip(body, width, bodyHeight) + "\n" + bottom
}

func (m console) scopeLabel(name string) string {
	for _, g := range m.data.controls.Groups {
		if g.Board == name || g.Memory == name {
			return strings.Join(g.Agents, " + ")
		}
	}
	return name
}
