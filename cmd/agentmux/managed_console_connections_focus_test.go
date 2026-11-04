package main

import (
	"errors"
	"strings"
	"testing"

	"agentmux/internal/managed"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

func TestConsoleConnectionsInitialRecipientIsFirstOtherActivePeer(t *testing.T) {
	for _, test := range []struct {
		name, focus, want string
		agents            []managed.AgentInfo
	}{
		{name: "two active", agents: []managed.AgentInfo{{ID: "alpha", State: "active"}, {ID: "beta", State: "active"}}, want: "beta"},
		{name: "skip inactive", agents: []managed.AgentInfo{{ID: "alpha", State: "active"}, {ID: "retired", State: "revoked"}, {ID: "beta", State: "active"}}, want: "beta"},
		{name: "initial sender focus", focus: "beta", agents: []managed.AgentInfo{{ID: "alpha", State: "active"}, {ID: "beta", State: "active"}}, want: "alpha"},
		{name: "only sender", agents: []managed.AgentInfo{{ID: "alpha", State: "active"}}, want: "alpha"},
	} {
		t.Run(test.name, func(t *testing.T) {
			m := console{tab: 2}
			// Failed and empty initial refreshes must not consume the first roster.
			for _, data := range []consoleData{{err: errors.New("offline"), agents: test.agents}, {}, {agents: test.agents, focusID: test.focus}} {
				next, _ := m.Update(data)
				m = next.(console)
			}
			if !m.recipientInitialized || m.nativeDeliveryRecipient() != test.want || m.nativeAction != nil || m.busy {
				t.Fatalf("initial recipient=%q, want %q; initialized=%v", m.nativeDeliveryRecipient(), test.want, m.recipientInitialized)
			}
		})
	}
}

func TestConsoleConnectionsInitialDefaultNeverReplacesExistingContext(t *testing.T) {
	agents := []managed.AgentInfo{{ID: "alpha", State: "active"}, {ID: "beta", State: "active"}}
	for _, test := range []struct {
		name string
		edit func(*console)
	}{
		{"prior roster and self recipient", func(m *console) { m.agents = agents }},
		{"restored initialization", func(m *console) { m.recipientInitialized = true }},
		{"message draft", func(m *console) { m.composer, m.composeTarget, m.text = "send", "alpha", []rune("unsent") }},
		{"failed draft", func(m *console) { m.failedDraft = &consoleDraft{kind: "send", target: "alpha", text: "unsent"} }},
		{"frozen native approval", func(m *console) { m.nativeAction = &consoleNativeAction{agent: "alpha", approve: true} }},
		{"frozen team action", func(m *console) { m.teamAction = &consoleTeamAction{} }},
		{"frozen owner review", func(m *console) { m.ownerReview = &consoleOwnerReview{} }},
	} {
		t.Run(test.name, func(t *testing.T) {
			m := console{tab: 2}
			test.edit(&m)
			native, team, owner, draft, text := m.nativeAction, m.teamAction, m.ownerReview, m.failedDraft, string(m.text)
			next, _ := m.Update(consoleData{agents: agents})
			m = next.(console)
			if m.target != 0 || !m.recipientInitialized || m.nativeAction != native || m.teamAction != team || m.ownerReview != owner || m.failedDraft != draft || string(m.text) != text {
				t.Fatalf("first populated refresh replaced existing selection, draft or frozen action: %+v", m)
			}
			// Clearing a modal or restoring this model must not defer the default
			// until a subsequent poll.
			m.composer, m.composeTarget, m.text = "", "", nil
			m.nativeAction, m.teamAction, m.ownerReview, m.failedDraft = nil, nil, nil, nil
			next, _ = m.Update(consoleData{agents: agents})
			if next.(console).target != 0 {
				t.Fatal("a later refresh applied the initial recipient default")
			}
		})
	}
}

func TestConsoleConnectionsInitialRecipientStaysIndependentOnLaterRefreshes(t *testing.T) {
	agents := []managed.AgentInfo{{ID: "alpha", State: "active"}, {ID: "beta", State: "active"}}
	next, _ := (console{tab: 2}).Update(consoleData{agents: agents})
	m := next.(console)
	// Select the recipient as sender too. Self-pairs remain a valid deliberate
	// selection; the initial default must never follow the sender.
	m.selected = 1
	m.nativeAction = &consoleNativeAction{agent: "beta", approve: true}
	for _, roster := range [][]managed.AgentInfo{{agents[1], agents[0]}, agents, {agents[1], agents[0]}} {
		next, _ = m.Update(consoleData{agents: roster})
		m = next.(console)
		if m.selectedID() != "beta" || m.nativeDeliveryRecipient() != "beta" || m.nativeAction.agent != "beta" || !m.nativeAction.approve {
			t.Fatal("later roster refresh retargeted the independent recipient or frozen approval")
		}
	}
	// A single-session first roster also consumes initialization. A later peer
	// appearing must not silently change the existing self recipient.
	next, _ = (console{tab: 2}).Update(consoleData{agents: agents[:1]})
	next, _ = next.(console).Update(consoleData{agents: agents})
	if next.(console).nativeDeliveryRecipient() != "alpha" {
		t.Fatal("a later join retargeted an already initialized recipient")
	}
}

func TestConsoleConnectionsDirectFocusPreservesIdentitiesAndPermissions(t *testing.T) {
	for _, focused := range []bool{false, true} {
		for _, key := range []rune{'[', ']'} {
			m := sessionSwitcherFixture()
			m.contentFocus, m.selected, m.target = focused, 1, 2
			m.message = "stale hint"
			m.data.boundaries.Agent, m.data.boundaries.Version = "bob", 17
			beforeSender, beforeRecipient := m.selectedID(), m.agents[m.target].ID
			for repeat := 0; repeat < 2; repeat++ {
				next, cmd := consolePress(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{key}})
				if cmd != nil || next.contentFocus != (key == ']') || next.tab != 2 || next.selectedID() != beforeSender || next.agents[next.target].ID != beforeRecipient || next.data.boundaries.Version != 17 || next.composer != "" || next.busy || next.message != "" {
					t.Fatalf("%c changed identities, permissions or view instead of explicitly focusing: %+v", key, next)
				}
				m = next
			}
			next, cmd := consolePress(m, tea.KeyMsg{Type: tea.KeyUp})
			if key == '[' {
				if next.selectedID() != "alice" || next.target != 2 || cmd == nil {
					t.Fatal("Up after sender focus failed to select only sender and refresh its boundaries")
				}
			} else if next.selectedID() != "bob" || next.target != 1 || cmd != nil {
				t.Fatal("Up after recipient focus changed sender or issued a command")
			}
		}
	}
}

func TestConsoleConnectionsLabelsFollowActualFocusAtSplitWidths(t *testing.T) {
	for _, width := range []int{50, 64, 80, 110} {
		for _, recipient := range []bool{false, true} {
			m := sessionSwitcherFixture()
			m.width, m.height, m.contentFocus = width, 32, recipient
			sender := ansi.Strip(m.connectionFocusLabel(false))
			target := ansi.Strip(m.connectionFocusLabel(true))
			if strings.Contains(sender, "↑/↓") == recipient || strings.Contains(target, "↑/↓") != recipient {
				t.Fatalf("both identities or the wrong identity claim Up/Down at width %d: %q / %q", width, sender, target)
			}
			view := ansi.Strip(m.View())
			for _, hint := range []string{"SENDER", "RECIPIENT", "[ sender", "] recipient", "Tab switch"} {
				if !strings.Contains(view, hint) {
					t.Fatalf("width %d hides explicit focus hint %q:\n%s", width, hint, view)
				}
			}
		}
	}
}

func TestConsoleConnectionsFocusKeysStayInEditorsAndLeaveHistoryKeysIntact(t *testing.T) {
	m := sessionSwitcherFixture()
	m.contentFocus = true
	m.compose("send", "bob", "", "draft")
	for _, key := range []rune{'[', ']'} {
		next, cmd := consolePress(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{key}})
		if cmd != nil || next.composer != "send" || next.composeTarget != "bob" || !next.contentFocus {
			t.Fatal("focus shortcut escaped or retargeted an existing draft")
		}
		m = next
	}
	if string(m.text) != "draft[]" {
		t.Fatal("focus shortcuts stopped being literal editor text")
	}
	m = sessionSwitcherFixture()
	m.tab = 0
	m.data.activity.Messages = []managed.Message{{ID: "first"}, {ID: "second"}}
	next, cmd := consolePress(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{']'}})
	if cmd != nil || next.messageIndex != 1 || !next.contentFocus || next.tab != 0 {
		t.Fatal("Connections shortcuts changed Board history navigation")
	}
}
