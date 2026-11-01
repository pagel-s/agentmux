package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"agentmux/internal/managed"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// The reader freezes owner-visible content and its identity. Polling, registry
// changes and key presses cannot retarget it or issue a broker mutation.
type consoleReader struct {
	title, body  string
	offset       int
	help         bool
	memoryReview *consoleMemoryVersionReview
	roleReview   *consoleRoleReview
}

const consoleReaderHelp = "↑/↓ or j/k scroll · PgUp/PgDn page · Home/End · Esc/q back"

func (m console) readerHelp() string {
	if m.reader != nil && m.reader.roleReview != nil {
		return "↑/↓ scroll · Enter adopts reviewed role version (no save)\nEsc/q keeps original version and draft"
	}
	if m.reader != nil && m.reader.memoryReview != nil {
		return "↑/↓ scroll · PgUp/PgDn · Home/End · Enter adopts reviewed version (no save)\nEsc/q keeps original version and draft"
	}
	return consoleReaderHelp
}

type consoleMemoryVersionReview struct {
	namespace, key, draft string
	oldVersion, version   int
	value, state          string
	err                   error
}

func (m console) reviewMemoryVersion() tea.Cmd {
	review := consoleMemoryVersionReview{namespace: m.memoryNamespace, key: m.memoryKey, draft: string(m.text), oldVersion: m.memoryVersion}
	return func() tea.Msg {
		args := map[string]string{"namespace": review.namespace, "key": review.key}
		raw, err := workCall(context.Background(), m.client, "memory.head", args)
		review.err = err
		if err != nil {
			return review
		}
		var head struct {
			Version int    `json:"version"`
			State   string `json:"state"`
		}
		if review.err = json.Unmarshal(raw, &head); review.err != nil {
			return review
		}
		review.version, review.state = head.Version, head.State
		if head.State != "live" {
			switch head.State {
			case "missing", "deleted", "expired":
			default:
				review.err = fmt.Errorf("unknown memory state; review latest again")
			}
			return review
		}
		raw, review.err = workCall(context.Background(), m.client, "memory.get", args)
		if review.err != nil {
			return review
		}
		var result struct {
			Entry managed.Memory `json:"entry"`
		}
		if review.err = json.Unmarshal(raw, &result); review.err == nil {
			if result.Entry.Version != head.Version {
				review.err = fmt.Errorf("memory changed during review; review latest again")
			} else {
				review.value = result.Entry.Value
			}
		}
		return review
	}
}

func (m *console) receiveMemoryReview(review consoleMemoryVersionReview) {
	m.busy = false
	if m.composer != "memory-value" || m.memoryNamespace != review.namespace || m.memoryKey != review.key || string(m.text) != review.draft || m.memoryVersion != review.oldVersion {
		m.message = "Memory review discarded because the draft changed. Ctrl-R reviews the current draft."
		return
	}
	if review.err != nil {
		m.message = "Memory review unavailable; draft kept: " + activityText(review.err.Error(), 150)
		return
	}
	latest := review.value
	if review.state != "" && review.state != "live" {
		latest = "No live value (" + review.state + ")."
	}
	m.reader = &consoleReader{title: consoleContent("Review memory "+review.namespace+" / "+review.key, 300), body: consoleContent(fmt.Sprintf("Latest saved value · version %d\n%s\n\nYour retained draft · original version %d\n%s\n\nEnter adopts only the displayed version for this draft. No save occurs until you submit from the editor; a newer concurrent write still conflicts.", review.version, latest, review.oldVersion, review.draft), 1<<20), memoryReview: &review}
}

func (m console) readerPageRows() int {
	width, height := m.width, m.height
	if width <= 0 {
		width = 100
	}
	if height <= 0 {
		height = 30
	}
	footer := ""
	if m.busy {
		footer = "Applying…\n"
	} else if m.message != "" && !m.reader.help {
		footer = consoleClip(consoleContent(m.message, 500), width, 2) + "\n"
	}
	footer += m.readerHelp()
	return max(1, height-lipgloss.Height(consoleClip(footer, width, height/2))-7)
}

type consoleDraft struct {
	kind, target, reference, text, namespace, key, mode string
	version                                             int
	roleVersion                                         int
	native                                              *consoleNativeDraft
}

func (m *console) restoreDraft() {
	if m.failedDraft == nil {
		m.message = "No failed draft to restore."
		return
	}
	d := m.failedDraft
	if d.native != nil {
		m.newNative = *d.native
	}
	m.memoryNamespace, m.memoryKey, m.memoryVersion = d.namespace, d.key, d.version
	if d.kind == "role" {
		m.roleVersion = d.roleVersion
	}
	m.compose(d.kind, d.target, d.reference, d.text)
	if d.mode != "" {
		m.composeMode = d.mode
	}
	m.failedDraft = nil
	m.message = "Draft restored for its original target. Review delivery before submitting again."
}

func (m *console) openReader() {
	var title, body string
	switch m.tab {
	case 0:
		messages := m.boardMessages()
		if len(messages) == 0 {
			break
		}
		v := messages[m.messageIndex%len(messages)]
		title = "Message " + v.ID
		body = v.Text + fmt.Sprintf("\n\nFrom: %s\nTo: %s\nBoard: %s\nReply to: %s\nThread: %s", v.From, strings.Join(v.To, ", "), v.Board, v.ReplyTo, v.ThreadID)
	case 1:
		if len(m.agents) == 0 {
			break
		}
		a := m.agents[m.selected%len(m.agents)]
		title = "Session " + a.ID
		data, _ := json.MarshalIndent(a, "", "  ")
		body = string(data)
		if a.External {
			body += "\n\nBridge setup (read and paste yourself; readiness unconfirmed)\n" + bridgeInstructions(m.state, a.ID)
		} else if m.data.selected == a.ID && m.data.preview != "" {
			body += "\n\nObserved terminal preview\n" + m.data.preview
		}
	case 2:
		title = "Effective boundaries and stored owner intent"
		data, _ := json.MarshalIndent(map[string]any{"effective": m.data.boundaries, "stored_controls": m.data.controls}, "", "  ")
		body = "Effective permissions include membership, isolation and lineage. Stored allow values are intent, not proof of current access.\n\n" + string(data)
	case 3:
		if m.memoryOpen && len(m.memoryEntries) > 0 {
			v := m.memoryEntries[m.memoryEntry%len(m.memoryEntries)]
			title = fmt.Sprintf("Memory %s / %s · v%d", m.memoryNamespace, v.Key, v.Version)
			body = v.Value
			v.Value, v.Embedding = "", nil
			metadata, _ := json.MarshalIndent(v, "", "  ")
			body += "\n\nMemory metadata / asserted source claims\n" + string(metadata)
		} else if m.data.boundaries.Agent == m.selectedID() {
			scopes := append(append([]managed.BoundaryScope{}, m.data.boundaries.Boards...), m.data.boundaries.Memory...)
			if len(scopes) > 0 {
				v := scopes[m.scope%len(scopes)]
				title = "Boundary " + v.Name
				data, _ := json.MarshalIndent(v, "", "  ")
				body = string(data)
			}
		}
	case 4:
		if len(m.data.activity.Tasks) == 0 {
			break
		}
		v := m.data.activity.Tasks[m.task%len(m.data.activity.Tasks)]
		title = "Task " + v.ID
		body = "Result\n" + v.Result + "\n\nInstruction\n" + v.Text
		v.Text, v.Result = "", ""
		data, _ := json.MarshalIndent(v, "", "  ")
		body += "\n\nTask provenance / reported trace\n" + string(data)
	case 5:
		title, body = "Existing terminal / bridge setup", m.details
	case 6:
		if v := m.selectedTeam(); v != nil {
			title = "Team " + v.ID
			data, _ := json.MarshalIndent(v, "", "  ")
			body = string(data)
		}
	case 7:
		if len(m.data.activity.Events) > 0 {
			v := m.data.activity.Events[m.eventIndex%len(m.data.activity.Events)]
			title = "Event " + v.ID
			data, _ := json.MarshalIndent(v, "", "  ")
			body = string(data)
		}
	}
	if body == "" {
		m.message = "No selected content to read. Choose a message, task, memory entry, team or event."
		return
	}
	m.reader = &consoleReader{title: consoleContent(title, 300), body: consoleContent(body, 1<<20)}
}

func (m console) readerKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	page := m.readerPageRows()
	lines := strings.Split(ansi.Wrap(m.reader.body, max(1, m.width), ""), "\n")
	limit := max(0, len(lines)-page)
	m.reader = &consoleReader{title: m.reader.title, body: m.reader.body, offset: min(m.reader.offset, limit), help: m.reader.help, memoryReview: m.reader.memoryReview, roleReview: m.reader.roleReview}
	switch msg.String() {
	case "enter":
		if review := m.reader.roleReview; review != nil {
			if m.composer == "role" && m.composeTarget == review.id && string(m.text) == review.draft && m.roleVersion == review.oldVersion {
				m.roleVersion = review.version
				m.message = "Reviewed role version adopted; draft retained. Enter in the editor saves with CAS checking."
			} else {
				m.message = "Role draft changed; review the current draft again."
			}
			m.reader = nil
			return m, nil
		}
		if review := m.reader.memoryReview; review != nil {
			if m.composer == "memory-value" && m.memoryNamespace == review.namespace && m.memoryKey == review.key && string(m.text) == review.draft && m.memoryVersion == review.oldVersion {
				m.memoryVersion = review.version
				m.message = fmt.Sprintf("Draft kept; reviewed version %d adopted. Enter in the editor saves with version checking.", review.version)
			} else {
				m.message = "Draft changed; review its latest version again before saving."
			}
			m.reader = nil
		}
	case "esc", "q", "o":
		m.reader = nil
	case "ctrl+c":
		return m, m.close()
	case "down", "j":
		m.reader.offset = min(limit, m.reader.offset+1)
	case "up", "k":
		m.reader.offset = max(0, m.reader.offset-1)
	case "pgdown", "ctrl+f", " ":
		m.reader.offset = min(limit, m.reader.offset+page)
	case "pgup", "ctrl+b":
		m.reader.offset = max(0, m.reader.offset-page)
	case "home":
		m.reader.offset = 0
	case "end":
		m.reader.offset = limit
	}
	return m, nil
}

func (m console) readerPanel(width, height int) string {
	lines := strings.Split(ansi.Wrap(m.reader.body, max(1, width), ""), "\n")
	rows := max(1, height-3)
	first := min(m.reader.offset, max(0, len(lines)-rows))
	last := min(len(lines), first+rows)
	label := "Saved selection"
	if m.reader.help {
		label = "Help"
	}
	header := consoleAccent.Render(consoleClip(m.reader.title, width, 1)) + "\n" + consoleMuted.Render(consoleClip(fmt.Sprintf("%s · lines %d–%d / %d · PgUp/PgDn · Esc back", label, first+1, last, len(lines)), width, 1)) + "\n\n"
	return consoleClip(header+strings.Join(lines[first:last], "\n"), width, height)
}

func consoleEditor(text []rune, cursor, width, rows int) string {
	width = max(1, width)
	cursor = max(0, min(cursor, len(text)))
	before := consoleContent(string(text[:cursor]), len(text)*8+1)
	after := consoleContent(string(text[cursor:]), len(text)*8+1)
	prefix := strings.Split(ansi.Hardwrap(before, width, true), "\n")
	line := len(prefix) - 1
	if ansi.StringWidth(prefix[line]) >= width {
		line++
	}
	lines := strings.Split(ansi.Hardwrap(before+consoleAccent.Render("▏")+after, width, true), "\n")
	first := max(0, line-max(1, rows)+1)
	first = min(first, max(0, len(lines)-rows))
	return strings.Join(lines[first:min(len(lines), first+max(1, rows))], "\n")
}

func consoleStep(index, count, delta int) int {
	if count == 0 {
		return 0
	}
	return max(0, index%count+delta) % count
}

func (m *console) stepContent(delta int) {
	switch m.tab {
	case 0:
		m.messageIndex = consoleStep(m.messageIndex, len(m.boardMessages()), delta)
	case 3:
		if m.memoryOpen {
			m.memoryEntry = consoleStep(m.memoryEntry, len(m.memoryEntries), delta)
		} else {
			m.scope = consoleStep(m.scope, len(m.data.boundaries.Boards)+len(m.data.boundaries.Memory), delta)
		}
	case 4:
		m.task = consoleStep(m.task, len(m.data.activity.Tasks), delta)
	case 6:
		m.teamIndex = consoleStep(m.teamIndex, len(m.data.teams), delta)
	case 7:
		m.eventIndex = consoleStep(m.eventIndex, len(m.data.activity.Events), delta)
	}
}
