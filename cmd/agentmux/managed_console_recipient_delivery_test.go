package main

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"agentmux/internal/managed"
	"agentmux/internal/managedconfig"
	"agentmux/internal/nativebridge"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

func recipientDeliveryFixture() console {
	m := nativeConsoleFixture()
	m.tab, m.target = 2, 1
	m.agents[1].Config = managedconfig.Agent{Interaction: "interactive", Harness: "codex"}
	m.data.boundaries.Agent = "pi"
	m.data.nativeStates = map[string]managed.NativeStatus{"codex": {
		Agent: "codex", Policy: managed.NativePolicy{Version: 12, NativeDelivery: managedconfig.NativeDelivery{Mode: "review", MaxTurns: 19, MaxHops: 3, CooldownSeconds: 2, Results: true}},
		PendingKnown: true, Coverage: "Known pending queue",
		PendingCount: 1, Pending: []nativebridge.Delivery{{ID: "recipient-candidate", Kind: "instruction", From: "pi", Text: "PRIVATE_PENDING_BODY"}},
	}}
	s := m.data.nativeStates["codex"]
	s.Reason = "Awaiting exact owner approval"
	m.data.nativeStates["codex"] = s
	return m
}

func TestConsoleConnectionsDeliveryUsesRecipientAndFreezesReviewedWrite(t *testing.T) {
	m := recipientDeliveryFixture()
	m.data.nativeErr = errors.New("sender status unavailable")
	m, cmd := nativeRune(m, "D")
	if cmd != nil || m.nativeAction == nil || m.nativeAction.agent != "codex" || m.selectedID() != "pi" || m.nativeAction.policy.Version != 12 {
		t.Fatal("recipient settings used sender status or changed sender")
	}
	m, _ = consolePress(m, tea.KeyMsg{Type: tea.KeyRight})
	m, cmd = consolePress(m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd != nil || !m.nativeAction.confirm {
		t.Fatal("settings skipped final review")
	}
	m.target = 0
	m.agents[0], m.agents[1] = m.agents[1], m.agents[0]
	m.data.nativeStates = nil
	op, args := m.nativeAction.args()
	if op != "native.configure" || args["agent"] != "codex" || args["version"] != 12 || args["mode"] != "auto" || args["max_turns"] != 19 || args["notes"] != false || args["results"] != true {
		t.Fatalf("recipient, revision or saved settings changed: %s %v", op, args)
	}
}

func TestConsoleConnectionsRecipientSummaryKeepsPendingBodyPrivate(t *testing.T) {
	m := recipientDeliveryFixture()
	view := m.connectionDeliverySummary("codex")
	if !strings.Contains(view, "review") || !strings.Contains(view, "queued 1") || strings.Contains(view, "PRIVATE_PENDING_BODY") {
		t.Fatalf("recipient summary wrong or leaks body: %s", view)
	}
	m, cmd := nativeRune(m, "A")
	if cmd != nil || m.nativeAction == nil || m.nativeAction.agent != "codex" || m.selectedID() != "pi" {
		t.Fatal("recipient approval failed or changed sender")
	}
	m, cmd = consolePress(m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd != nil || !strings.Contains(ansi.Strip(m.nativeDeliveryPanel(120, 40)), "PRIVATE_PENDING_BODY") {
		t.Fatal("explicit approval did not reveal complete exact body")
	}
	_, args := m.nativeAction.args()
	if args["agent"] != "codex" || args["candidate"] != "recipient-candidate" || args["version"] != 12 {
		t.Fatalf("approval not bound to recipient: %v", args)
	}
}

func TestConsoleConnectionsRecipientStatusUnknownCannotOpenWrite(t *testing.T) {
	for _, scenario := range []string{"absent", "mismatch", "unversioned", "revoked", "unknown-config", "offline"} {
		t.Run(scenario, func(t *testing.T) {
			m := recipientDeliveryFixture()
			s := m.data.nativeStates["codex"]
			switch scenario {
			case "absent":
				delete(m.data.nativeStates, "codex")
			case "mismatch":
				s.Agent = "pi"
				m.data.nativeStates["codex"] = s
			case "unversioned":
				s.Policy.Version = 0
				m.data.nativeStates["codex"] = s
			case "revoked":
				m.agents[1].State = "revoked"
			case "unknown-config":
				m.agents[1].Config.Interaction = ""
			case "offline":
				m.offline = true
			}
			view := m.connectionDeliverySummary("codex")
			if !strings.Contains(view, "unknown") {
				t.Fatalf("unknown configuration became a worker: %s", view)
			}
			if strings.Contains(view, "queued 0") || strings.Contains(view, "review ·") || strings.Contains(view, "waits for") {
				t.Fatalf("unknown status appeared current: %s", view)
			}
			m.reviewNativeDelivery(false)
			if m.nativeAction != nil || m.message == "" {
				t.Fatal("unknown recipient opened write")
			}
		})
	}
	m := recipientDeliveryFixture()
	if view := m.connectionDeliverySummary("missing"); !strings.Contains(view, "unknown") {
		t.Fatal(view)
	}
}

func TestConsoleConnectionsUnknownPendingDoesNotBecomeEmpty(t *testing.T) {
	m := recipientDeliveryFixture()
	s := m.data.nativeStates["codex"]
	s.Pending, s.PendingCount, s.PendingKnown = nil, 0, false
	m.data.nativeStates["codex"] = s
	if view := m.connectionDeliverySummary("codex"); !strings.Contains(view, "queued unknown") || strings.Contains(view, "queued 0") {
		t.Fatal(view)
	}
	if reason := m.menuActionReason(consoleMenuAction{key: "A", target: "codex"}); !strings.Contains(reason, "unavailable") {
		t.Fatal(reason)
	}
	m.reviewNativeDelivery(true)
	if m.nativeAction != nil {
		t.Fatal("unknown pending approved")
	}
	m.reviewNativeDelivery(false)
	if m.nativeAction == nil {
		t.Fatal("known policy controls hidden by unknown admission")
	}
}

func TestConsoleConnectionsActionsFreezeRecipientWithoutSelectingSender(t *testing.T) {
	for _, key := range []string{"D", "A"} {
		t.Run(key, func(t *testing.T) {
			m := recipientDeliveryFixture()
			m.openActions()
			found := false
			for _, row := range m.actionRows {
				if row.key == key {
					found = row.target == "codex" && row.kind == "native-recipient" && strings.Contains(row.label, "recipient")
				}
			}
			if !found {
				t.Fatal("Actions did not expose frozen recipient")
			}
			m.text = []rune(map[string]string{"D": "Configure recipient delivery", "A": "Review recipient inbox"}[key])
			m.target = 0
			model, cmd := m.invokeMenuAction()
			m = model.(console)
			if cmd != nil || m.nativeAction == nil || m.nativeAction.agent != "codex" || m.selectedID() != "pi" || m.target != 1 {
				t.Fatal("Actions retargeted or selected recipient as sender")
			}
		})
	}
	m := recipientDeliveryFixture()
	m.openActions()
	m.text = []rune("Configure recipient delivery")
	m.agents = m.agents[:1]
	model, cmd := m.invokeMenuAction()
	m = model.(console)
	if cmd != nil || m.nativeAction != nil || m.message == "" {
		t.Fatal("disappeared recipient opened a write")
	}
}

func TestConsoleAutoConfirmationPreviewsQueueWithoutBodiesOrExactApprovalClaim(t *testing.T) {
	m := recipientDeliveryFixture()
	m.reviewNativeDelivery(false)
	m.nativeAction.policy.Mode = "auto"
	preview := m.nativeAction.autoQueueReview()
	for _, want := range []string{"existing eligible queued items", "Queue at review: 1", "instruction from pi", "snapshot can change", "not approval of only these items", "Choose Review"} {
		if !strings.Contains(preview, want) {
			t.Fatalf("missing %q: %s", want, preview)
		}
	}
	if strings.Contains(preview, "PRIVATE_PENDING_BODY") {
		t.Fatal("configuration preview leaked pending body")
	}
	m.nativeAction.pendingMore = true
	if !strings.Contains(m.nativeAction.autoQueueReview(), "inventory is bounded") {
		t.Fatal("bounded inventory hidden")
	}
	m.nativeAction.pendingKnown = false
	if preview := m.nativeAction.autoQueueReview(); !strings.Contains(preview, "unknown") || strings.Contains(preview, "instruction from pi") {
		t.Fatal(preview)
	}
	m.nativeAction.policy.Mode = "review"
	if m.nativeAction.autoQueueReview() != "" {
		t.Fatal("review policy implies auto dispatch")
	}
}

func TestConsoleConnectionsDeliveryAffordanceVisibleAtCommonTerminalSizes(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {180, 32}} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			m := recipientDeliveryFixture()
			m.width, m.height = size[0], size[1]
			view := ansi.Strip(m.View())
			for _, want := range []string{"Receiver delivery:", "each eligible queued item", "Shift+D", "Shift+A"} {
				if !strings.Contains(view, want) {
					t.Fatalf("missing %q: %s", want, view)
				}
			}
			if strings.Contains(view, "PRIVATE_PENDING_BODY") {
				t.Fatal("full view leaked pending body")
			}
			for _, line := range strings.Split(view, "\n") {
				if ansi.StringWidth(line) > size[0] {
					t.Fatalf("view exceeds width: %q", line)
				}
			}
			if len(strings.Split(view, "\n")) > size[1] {
				t.Fatal("view exceeds height")
			}
		})
	}
}

func TestConsoleConnectionsManagedWorkerIsIndependentOfNativeMode(t *testing.T) {
	m := recipientDeliveryFixture()
	m.agents[1].Config.Interaction = "managed"
	view := m.connectionDeliverySummary("codex")
	if !strings.Contains(view, "managed worker") || strings.Contains(view, "Manual") || strings.Contains(view, "queued") {
		t.Fatalf("managed worker incorrectly depends on native delivery: %s", view)
	}
	m.reviewNativeDelivery(false)
	if m.nativeAction != nil {
		t.Fatal("managed worker opened native policy write")
	}
}

func TestConsoleAutoPolicyRefreshUpdatesOnlyQueuePreviewAndRevision(t *testing.T) {
	m := recipientDeliveryFixture()
	m.reviewNativeDelivery(false)
	m.nativeAction.policy.Mode = "auto"
	current := m.data.nativeStates["codex"]
	current.Policy.Version = 13
	current.Policy.MaxTurns = 99
	current.Pending = []nativebridge.Delivery{{ID: "new-candidate", Kind: "boundaries", From: "owner", Text: "NEW_PRIVATE_BODY"}}
	current.PendingCount, current.PendingKnown = 17, true
	current.PendingMore = true
	m.nativeAction.latest = &current
	m, cmd := consolePress(m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd != nil || !m.nativeAction.confirm || m.nativeAction.policy.Mode != "auto" || m.nativeAction.policy.MaxTurns != 19 || m.nativeAction.policy.Version != 13 {
		t.Fatal("policy refresh changed retained owner choices")
	}
	preview := m.nativeAction.autoQueueReview()
	if !strings.Contains(preview, "Queue at review: 17") || !strings.Contains(preview, "boundaries from owner") || strings.Contains(preview, "NEW_PRIVATE_BODY") || strings.Contains(preview, "instruction from pi") {
		t.Fatal(preview)
	}
	view := ansi.Strip(m.nativeDeliveryPanel(80, 16))
	if !strings.Contains(view, "existing eligible queued items") || !strings.Contains(view, "Queue at review") {
		t.Fatalf("auto warning hidden on initial review page: %s", view)
	}
}
