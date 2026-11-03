package main

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strconv"
	"strings"

	"agentmux/internal/managed"
	"agentmux/internal/nativebridge"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

// Owner reviews freeze the recipient, policy revision and candidate contents.
// Polls and registry reordering cannot redirect an approval or widen its scope.
type consoleNativeAction struct {
	agent, label                    string
	policy                          managed.NativePolicy
	pending                         []nativebridge.Delivery
	field, selected, offset         int
	approve, confirm, replaceDigits bool
	pendingMore                     bool
	capabilities                    nativebridge.Capabilities
	latest                          *managed.NativeStatus
	needsRefresh                    bool
	adapter, reason                 string
}

var nativeFields = []string{"Delivery", "Maximum turns", "Maximum hops", "Cooldown seconds", "Inbox notes", "Pings", "Board updates", "Memory updates", "Returned results"}
var nativeModes = []string{"manual", "review", "auto"}

type consoleNativeVersionReview struct {
	state, agent string
	version      int
	current      managed.NativeStatus
	err          error
}

func (m console) fetchNativeVersion() tea.Cmd {
	action := *m.nativeAction
	review := consoleNativeVersionReview{state: m.state, agent: action.agent, version: action.policy.Version}
	return func() tea.Msg {
		raw, err := workCall(context.Background(), m.client, "native.status", map[string]any{"agent": action.agent})
		review.err = err
		if err == nil {
			var out struct {
				Status managed.NativeStatus `json:"status"`
			}
			review.err = json.Unmarshal(raw, &out)
			review.current = out.Status
		}
		return review
	}
}

func (m *console) receiveNativeVersionReview(review consoleNativeVersionReview) {
	m.busy = false
	if m.nativeAction == nil || review.state != m.state || review.agent != m.nativeAction.agent || review.version != m.nativeAction.policy.Version {
		return
	}
	if review.err != nil || review.current.Agent != review.agent || review.current.Policy.Version < 1 {
		m.message = "Current native policy review unavailable; your draft is kept."
		return
	}
	current := review.current
	current.Pending = append([]nativebridge.Delivery(nil), current.Pending...)
	m.nativeAction.latest = &current
	m.nativeAction.offset = 0
}

func (m *console) reviewNativeDelivery(approve bool) {
	if m.busy {
		return
	}
	if m.offline || m.data.nativeErr != nil || m.data.native.Agent != m.selectedID() || m.data.native.Policy.Version < 1 {
		m.message = "Current native delivery state unavailable. Refresh before editing or approving."
		return
	}
	s := m.data.native
	if approve && (s.Policy.Mode != "review" || len(s.Pending) == 0) {
		m.message = "Review mode and a pending request are required. Shift+D configures delivery."
		return
	}
	m.nativeAction = &consoleNativeAction{agent: s.Agent, label: m.agentLabel(s.Agent), policy: s.Policy,
		pending: append([]nativebridge.Delivery(nil), s.Pending...), pendingMore: s.PendingMore, capabilities: s.Capabilities, approve: approve, replaceDigits: true, adapter: s.Adapter, reason: s.Reason}
}

func (a consoleNativeAction) args() (string, map[string]any) {
	if a.approve {
		return "native.approve", map[string]any{"agent": a.agent, "version": a.policy.Version, "candidate": a.pending[a.selected].ID}
	}
	p := a.policy
	return "native.configure", map[string]any{"agent": a.agent, "version": p.Version, "mode": p.Mode,
		"max_turns": p.MaxTurns, "max_hops": p.MaxHops, "cooldown_seconds": p.CooldownSeconds,
		"notes": p.Notes, "pings": p.Pings, "boards": p.Boards, "memory_updates": p.MemoryUpdates, "results": p.Results}
}

func nativeEnabled(on bool) string {
	if on {
		return "on"
	}
	return "off"
}

func nativeFieldValues(p managed.NativePolicy) []string {
	return []string{p.Mode, strconv.Itoa(p.MaxTurns), strconv.Itoa(p.MaxHops), strconv.Itoa(p.CooldownSeconds), nativeEnabled(p.Notes), nativeEnabled(p.Pings), nativeEnabled(p.Boards), nativeEnabled(p.MemoryUpdates), nativeEnabled(p.Results)}
}

func (a *consoleNativeAction) number() *int {
	switch a.field {
	case 1:
		return &a.policy.MaxTurns
	case 2:
		return &a.policy.MaxHops
	case 3:
		return &a.policy.CooldownSeconds
	}
	return nil
}

func (a *consoleNativeAction) toggle(step int) {
	switch a.field {
	case 0:
		for i, mode := range nativeModes {
			if a.policy.Mode == mode {
				a.policy.Mode = nativeModes[(i+step+len(nativeModes))%len(nativeModes)]
				break
			}
		}
	case 4:
		a.policy.Notes = !a.policy.Notes
	case 5:
		a.policy.Pings = !a.policy.Pings
	case 6:
		a.policy.Boards = !a.policy.Boards
	case 7:
		a.policy.MemoryUpdates = !a.policy.MemoryUpdates
	case 8:
		a.policy.Results = !a.policy.Results
	default:
		if n := a.number(); n != nil {
			*n = max(0, min(86400, *n+step))
		}
	}
	a.replaceDigits = true
}

func (m console) nativeDeliveryKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	a := *m.nativeAction
	key := msg.String()
	if key == "ctrl+c" {
		return m, m.close()
	}
	if key == "esc" || key == "q" {
		if a.latest != nil {
			a.latest = nil
			a.offset = 0
			m.nativeAction = &a
			return m, nil
		}
		if a.confirm {
			a.confirm, a.offset = false, 0
			m.nativeAction = &a
		} else {
			m.nativeAction = nil
		}
		return m, nil
	}
	if key == "ctrl+r" {
		if m.busy {
			return m, nil
		}
		if m.offline {
			m.message = "Broker offline. Native delivery draft kept."
			return m, nil
		}
		m.busy = true
		return m, m.fetchNativeVersion()
	}
	if a.latest != nil {
		switch key {
		case "up", "k":
			a.offset = max(0, a.offset-1)
		case "down", "j":
			a.offset++
		case "pgup":
			a.offset = max(0, a.offset-8)
		case "pgdown":
			a.offset += 8
		case "home":
			a.offset = 0
		case "enter":
			if a.approve {
				unchanged := false
				for _, candidate := range a.latest.Pending {
					if reflect.DeepEqual(candidate, a.pending[a.selected]) {
						unchanged = true
						break
					}
				}
				if !unchanged || a.latest.Policy.Mode != "review" {
					m.message = "That exact request is no longer eligible under the current review policy. Inspect its delivery before choosing another request."
					return m, nil
				}
			}
			a.policy.Version = a.latest.Policy.Version
			a.latest = nil
			a.needsRefresh = false
			a.confirm = true
			a.offset = 0
			m.message = "Reviewed policy version adopted. Your retained choices are unchanged; Enter applies only after this final review."
		}
		m.nativeAction = &a
		return m, nil
	}
	if a.confirm {
		switch key {
		case "up", "k":
			a.offset = max(0, a.offset-1)
		case "down", "j":
			a.offset++
		case "pgup":
			a.offset = max(0, a.offset-8)
		case "pgdown":
			a.offset += 8
		case "home":
			a.offset = 0
		case "enter":
			if m.busy {
				return m, nil
			}
			if m.offline {
				m.message = "Broker offline. Exact review kept; refresh before applying."
				return m, nil
			}
			if a.needsRefresh {
				m.message = "Draft kept. Ctrl-R reviews the current policy before another submission."
				return m, nil
			}
			op, args := a.args()
			m.nativeAction, m.busy = nil, true
			label := "Native delivery policy saved for " + a.agent + ". Broker permissions still govern every turn."
			if a.approve {
				label = "Approved only " + a.pending[a.selected].ID + " for " + a.agent + "; native delivery waits for a safe boundary."
			}
			command := m.mutate(op, args, label)
			return m, func() tea.Msg {
				result := command().(consoleAction)
				if result.err != nil {
					result.nativeDraft = &a
				}
				return result
			}
		}
		m.nativeAction = &a
		return m, nil
	}
	if a.approve {
		switch key {
		case "up", "k":
			a.selected = max(0, a.selected-1)
		case "down", "j":
			a.selected = min(len(a.pending)-1, a.selected+1)
		case "enter":
			a.confirm, a.offset = true, 0
		}
		m.nativeAction = &a
		return m, nil
	}
	switch key {
	case "up", "k", "shift+tab":
		a.field = (a.field + len(nativeFields) - 1) % len(nativeFields)
		a.replaceDigits = true
	case "down", "j", "tab":
		a.field = (a.field + 1) % len(nativeFields)
		a.replaceDigits = true
	case "left", "h":
		a.toggle(-1)
	case "right", "l", " ":
		a.toggle(1)
	case "backspace", "ctrl+h":
		if n := a.number(); n != nil {
			*n /= 10
			a.replaceDigits = false
		}
	case "ctrl+u":
		if n := a.number(); n != nil {
			*n = 0
			a.replaceDigits = false
		}
	case "enter":
		if a.policy.MaxTurns < 1 || a.policy.MaxTurns > 10000 || a.policy.MaxHops < 1 || a.policy.MaxHops > 64 || a.policy.CooldownSeconds < 0 || a.policy.CooldownSeconds > 86400 {
			m.message = "Use 1–10,000 turns, 1–64 hops and 0–86,400 cooldown seconds."
			return m, nil
		}
		a.confirm, a.offset = true, 0
	default:
		if n := a.number(); n != nil && msg.Type == tea.KeyRunes {
			for _, r := range msg.Runes {
				if r >= '0' && r <= '9' {
					v := *n
					if a.replaceDigits {
						v = 0
					}
					if v*10+int(r-'0') <= 86400 {
						*n = v*10 + int(r-'0')
						a.replaceDigits = false
					}
				}
			}
		}
	}
	m.nativeAction = &a
	return m, nil
}

func (m console) nativeDeliveryPanel(width, height int) string {
	a := m.nativeAction
	if a == nil {
		return ""
	}
	header := fmt.Sprintf("%s\n%s · policy v%d\n\n", consoleAccent.Render("Native delivery · "+consoleContent(a.label, 150)), consoleContent(a.agent, 100), a.policy.Version)
	if a.latest != nil {
		body := fmt.Sprintf("Current saved policy · version %d\n", a.latest.Policy.Version)
		for i, value := range nativeFieldValues(a.latest.Policy) {
			body += fmt.Sprintf("%-20s %s\n", nativeFields[i], value)
		}
		body += fmt.Sprintf("\nYour retained draft · original version %d\n", a.policy.Version)
		if a.approve {
			body += "Approval of exact candidate " + consoleContent(a.pending[a.selected].ID, 180) + "\n"
		} else {
			for i, value := range nativeFieldValues(a.policy) {
				body += fmt.Sprintf("%-20s %s\n", nativeFields[i], value)
			}
		}
		body += "\nEnter adopts only the displayed current version. Your choices stay unchanged. No save or approval occurs until the following final review."
		lines := strings.Split(ansi.Wrap(body, max(1, width), ""), "\n")
		rows := max(1, height-5)
		offset := min(a.offset, max(0, len(lines)-rows))
		return consoleClip(header+strings.Join(lines[offset:min(len(lines), offset+rows)], "\n"), width, height)
	}
	if a.confirm {
		var body string
		if a.approve {
			d := a.pending[a.selected]
			body = fmt.Sprintf("Approve this exact %s from %s\nCandidate: %s\nTask: %s\n\n%s\n\nApproval grants this candidate one native turn under policy v%d. It does not grant further commands or change peer permissions.", consoleContent(d.Kind, 40), consoleContent(d.From, 100), consoleContent(d.ID, 160), consoleContent(d.TaskID, 160), consoleContent(d.Text, len([]rune(d.Text))), a.policy.Version)
		} else {
			body = "Review delivery policy\n\n"
			for i, value := range nativeFieldValues(a.policy) {
				body += fmt.Sprintf("%-20s %s\n", nativeFields[i], value)
			}
			body += "\nInstructions still require an allowed Commands edge. Notes, results and shared updates remain agent-authored observations.\nNative tools, approvals and human interaction stay available."
			if a.policy.Mode == "manual" {
				body += "\nManual stops future turns and requests safe cancellation of an active peer turn."
			}
			if a.adapter == "" {
				body += "\nDelivery waits for a supported connected adapter: " + consoleContent(a.reason, 600)
			}
		}
		if a.adapter != "" && !a.capabilities.PreservesDraft {
			body += "\nDraft preservation is not verified for this adapter. Check the native terminal before enabling delivery."
		}
		if a.adapter != "" && !a.capabilities.Cancellation {
			body += "\nQueued native turns cannot be retracted automatically; cancellation can remain uncertain."
		}
		if a.capabilities.CompletionEvidence == "receiver-ack" {
			body += "\nCompletion is a receiver acknowledgment, not an independently observed native completion event."
		}
		lines := strings.Split(ansi.Wrap(body, max(1, width), ""), "\n")
		rows := max(1, height-5)
		offset := min(a.offset, max(0, len(lines)-rows))
		return consoleClip(header+strings.Join(lines[offset:min(len(lines), offset+rows)], "\n"), width, height)
	}
	if a.approve {
		body := header + consoleMuted.Render("Choose a request; Enter opens its full exact-content review.") + "\n\n"
		rows := max(1, height-6)
		if a.pendingMore {
			body += consoleMuted.Render("More queued items follow as the queue advances.") + "\n"
			rows = max(1, rows-1)
		}
		start := max(0, a.selected-rows+1)
		for i := start; i < len(a.pending) && i < start+rows; i++ {
			d := a.pending[i]
			line := fmt.Sprintf("%s → %s · %s", consoleContent(d.From, 80), a.agent, consoleContent(d.Kind, 30))
			if i == a.selected {
				line = consoleSelected.Render("› " + line)
			} else {
				line = "  " + line
			}
			body += consoleClip(line, width, 1) + "\n"
		}
		return consoleClip(body, width, height)
	}
	body := header
	values := nativeFieldValues(a.policy)
	rows := max(1, height-7)
	start := max(0, a.field-rows+1)
	for i := start; i < len(nativeFields) && i < start+rows; i++ {
		line := fmt.Sprintf("  %-20s %s", nativeFields[i], values[i])
		if i == a.field {
			line = consoleSelected.Render("› " + strings.TrimPrefix(line, "  "))
		}
		body += consoleClip(line, width, 1) + "\n"
	}
	body += fmt.Sprintf("\n%s\n%s", consoleMuted.Render("Manual: inbox only · Review: approve each · Auto: safe boundary"), consoleMuted.Render("Field "+strconv.Itoa(a.field+1)+"/"+strconv.Itoa(len(nativeFields))+" · Enter reviews before applying"))
	return consoleClip(body, width, height)
}

func (m console) nativeDeliverySummary(width int) string {
	if m.offline {
		return consoleMuted.Render("Native delivery: saved state; current delivery unknown\n")
	}
	if m.data.nativeErr != nil {
		return "Native delivery unavailable: " + consoleContent(m.data.nativeErr.Error(), 180) + "\n"
	}
	s := m.data.native
	if s.Agent != m.selectedID() {
		return "Native delivery: loading; command permission alone does not start a turn.\n"
	}
	var out strings.Builder
	fmt.Fprintf(&out, "Native delivery: %s · %s\n", s.Policy.Mode, consoleContent(s.State, 80))
	fmt.Fprintf(&out, "%s\n", consoleContent(s.Reason, 250))
	fmt.Fprintln(&out, nativeObservationSummary(s))
	fmt.Fprintf(&out, "Turns %d/%d · hop cap %d · pending %d\n", s.Turns, s.Policy.MaxTurns, s.Policy.MaxHops, max(s.PendingCount, len(s.Pending)))
	if s.PendingMore {
		out.WriteString("More queued items follow as the queue advances.\n")
	}
	if s.Active != nil {
		fmt.Fprintf(&out, "Active: %s · %s\n", consoleContent(s.Active.ID, 100), consoleContent(s.Active.Phase, 40))
		fmt.Fprintf(&out, "Last receipt: %s\n", consoleContent(s.Active.UpdatedAt, 100))
	}
	adapter := s.Adapter
	if adapter == "" {
		adapter = "not connected"
	}
	evidence := ""
	if s.Capabilities.CompletionEvidence == "receiver-ack" {
		evidence = " · completion: receiver acknowledgment"
	} else if s.Capabilities.CompletionEvidence == "native-events" {
		evidence = " · native completion events"
	}
	fmt.Fprintf(&out, "Adapter: %s · instructions %s · replies %s%s\n", consoleContent(adapter, 50), nativeEnabled(s.Capabilities.Instructions), nativeEnabled(s.Capabilities.Completion), evidence)
	if s.Adapter != "" {
		draft, cancel := "unverified", "unavailable"
		if s.Capabilities.PreservesDraft {
			draft = "verified"
		}
		if s.Capabilities.Cancellation {
			cancel = "supported"
		}
		fmt.Fprintf(&out, "Draft preservation %s · cancellation %s\n", draft, cancel)
	}
	out.WriteString("Shift+D delivery settings")
	if s.Policy.Mode == "review" && len(s.Pending) > 0 {
		out.WriteString(" · Shift+A review inbox")
	}
	out.WriteString("\n")
	return consoleClip(out.String(), width, 12) + "\n"
}
