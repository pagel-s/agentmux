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

func nativeConsoleFixture() console {
	return console{tab: 1, width: 80, height: 24, agents: []managed.AgentInfo{{ID: "pi", Title: "Poem collaborator", State: "active", Config: managedconfig.Agent{Interaction: "interactive", Harness: "pi"}}, {ID: "codex", State: "active"}},
		data: consoleData{selected: "pi", native: managed.NativeStatus{Agent: "pi", Policy: managed.NativePolicy{NativeDelivery: managedconfig.NativeDelivery{Mode: "manual", MaxTurns: 20, MaxHops: 4, Results: true}, Version: 7}, Adapter: "pi", Capabilities: nativebridge.Capabilities{Instructions: true, Completion: true}, Pending: []nativebridge.Delivery{{ID: "candidate-1", TaskID: "task-1", From: "codex", Kind: "instruction", Text: "First exact request"}, {ID: "candidate-2", TaskID: "task-2", From: "claude", Kind: "instruction", Text: "Second exact request\n" + strings.Repeat("review full text\n", 30) + "REQUEST TAIL"}}}}}
}

func nativeRune(m console, key string) (console, tea.Cmd) {
	return consolePress(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)})
}

func TestConsoleNativeDeliveryReviewFreezesPolicyAndRecipient(t *testing.T) {
	m := nativeConsoleFixture()
	m, cmd := nativeRune(m, "D")
	if cmd != nil || m.nativeAction == nil || m.busy {
		t.Fatal("settings unexpectedly applied without review")
	}
	m, _ = consolePress(m, tea.KeyMsg{Type: tea.KeyRight})
	m, _ = consolePress(m, tea.KeyMsg{Type: tea.KeyRight})
	if m.tab != 1 || m.nativeAction.policy.Mode != "auto" {
		t.Fatal("settings arrows changed the global tab")
	}
	m, _ = consolePress(m, tea.KeyMsg{Type: tea.KeyDown})
	m, _ = nativeRune(m, "42")
	m, cmd = consolePress(m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd != nil || !m.nativeAction.confirm {
		t.Fatal("Enter skipped final policy review")
	}
	// Polling and a changed roster must not redirect the frozen write.
	m.selected = 1
	m.data.native.Agent, m.data.native.Policy.Version = "codex", 99
	op, args := m.nativeAction.args()
	if op != "native.configure" || args["agent"] != "pi" || args["version"] != 7 || args["mode"] != "auto" || args["max_turns"] != 42 {
		t.Fatalf("review retargeted: %s %v", op, args)
	}
	m, cmd = consolePress(m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil || !m.busy || m.nativeAction != nil {
		t.Fatal("final reviewed policy did not produce one write")
	}
}

func TestConsoleNativeApprovalReviewsExactFrozenCandidateAndAllContent(t *testing.T) {
	m := nativeConsoleFixture()
	m.data.native.Policy.Mode = "review"
	m, cmd := nativeRune(m, "A")
	if cmd != nil || m.nativeAction == nil {
		t.Fatal("pending chooser failed")
	}
	m, _ = consolePress(m, tea.KeyMsg{Type: tea.KeyDown})
	m, cmd = consolePress(m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd != nil || !m.nativeAction.confirm {
		t.Fatal("request approved before content review")
	}
	op, args := m.nativeAction.args()
	if op != "native.approve" || args["candidate"] != "candidate-2" || args["agent"] != "pi" || args["version"] != 7 {
		t.Fatalf("wrong exact request: %v", args)
	}
	view := ansi.Strip(m.nativeDeliveryPanel(80, 14))
	if !strings.Contains(view, "Second exact request") || !strings.Contains(view, "candidate-2") {
		t.Fatal("approval hides request identity/content")
	}
	m.data.native.Pending[1].ID, m.data.native.Pending[1].Text = "replacement", "changed concurrently"
	m.selected = 1
	m, _ = consolePress(m, tea.KeyMsg{Type: tea.KeyPgDown})
	m, _ = consolePress(m, tea.KeyMsg{Type: tea.KeyPgDown})
	m, _ = consolePress(m, tea.KeyMsg{Type: tea.KeyPgDown})
	m, _ = consolePress(m, tea.KeyMsg{Type: tea.KeyPgDown})
	if !strings.Contains(ansi.Strip(m.nativeDeliveryPanel(80, 14)), "REQUEST TAIL") {
		t.Fatal("full long request cannot be reviewed")
	}
	_, args = m.nativeAction.args()
	if args["candidate"] != "candidate-2" {
		t.Fatal("poll retargeted approval")
	}
	m, cmd = consolePress(m, tea.KeyMsg{Type: tea.KeyEsc})
	if cmd != nil || m.nativeAction == nil || m.nativeAction.confirm {
		t.Fatal("Escape did not return without approving")
	}
}

func TestConsoleNativeDeliveryUnavailableOrStaleStateCannotWrite(t *testing.T) {
	for _, scenario := range []string{"offline", "error", "other", "unversioned", "empty-review"} {
		t.Run(scenario, func(t *testing.T) {
			m := nativeConsoleFixture()
			approve := false
			switch scenario {
			case "offline":
				m.offline = true
			case "error":
				m.data.nativeErr = errors.New("offline")
			case "other":
				m.data.native.Agent = "other"
			case "unversioned":
				m.data.native.Policy.Version = 0
			case "empty-review":
				approve = true
				m.data.native.Policy.Mode = "review"
				m.data.native.Pending = nil
			}
			m.reviewNativeDelivery(approve)
			if m.nativeAction != nil || m.message == "" || m.busy {
				t.Fatal("unavailable state opened a write")
			}
		})
	}
}

func TestConsoleNativeReviewOfflineKeepsExactPolicy(t *testing.T) {
	m := nativeConsoleFixture()
	m.reviewNativeDelivery(false)
	m, _ = consolePress(m, tea.KeyMsg{Type: tea.KeyEnter})
	m.offline = true
	m, cmd := consolePress(m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd != nil || m.busy || m.nativeAction == nil || !m.nativeAction.confirm || !strings.Contains(m.message, "offline") {
		t.Fatal("offline confirmation discarded or issued review")
	}
}

func TestConsoleNativeDeliveryLimitsAndObservationToggles(t *testing.T) {
	m := nativeConsoleFixture()
	m.reviewNativeDelivery(false)
	m.nativeAction.field = 2
	m, _ = nativeRune(m, "65")
	m, cmd := consolePress(m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd != nil || m.nativeAction.confirm || !strings.Contains(m.message, "1–64") {
		t.Fatal("invalid hop limit passed review")
	}
	m, _ = consolePress(m, tea.KeyMsg{Type: tea.KeyCtrlU})
	m, _ = nativeRune(m, "8")
	m.nativeAction.field = 4
	m, _ = consolePress(m, tea.KeyMsg{Type: tea.KeyRight})
	m, _ = consolePress(m, tea.KeyMsg{Type: tea.KeyEnter})
	_, args := m.nativeAction.args()
	if !m.nativeAction.confirm || args["max_hops"] != 8 || args["notes"] != true || args["boards"] != false || args["results"] != true {
		t.Fatalf("limits or observation scope changed: %v", args)
	}
}

func TestConsoleNativeDeliveryControlsPreserveEditorAndGlobalNavigation(t *testing.T) {
	m := nativeConsoleFixture()
	m.compose("send", "pi", "", "human draft")
	m, cmd := nativeRune(m, "D")
	if cmd != nil || m.nativeAction != nil || string(m.text) != "human draftD" {
		t.Fatal("settings shortcut stole draft text")
	}
	m.composer, m.text = "", nil
	m, _ = nativeRune(m, "D")
	m, _ = consolePress(m, tea.KeyMsg{Type: tea.KeyCtrlG})
	if m.reader == nil || !strings.Contains(m.reader.body, "policy version") {
		t.Fatal("review help unavailable")
	}
	m, _ = consolePress(m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.reader != nil || m.nativeAction == nil {
		t.Fatal("help lost settings")
	}
	m, _ = consolePress(m, tea.KeyMsg{Type: tea.KeyEsc})
	m, _ = consolePress(m, tea.KeyMsg{Type: tea.KeyRight})
	if m.tab != 2 || m.nativeAction != nil {
		t.Fatal("global arrows did not resume")
	}
}

func TestConsoleNativeDeliverySmallTerminalKeepsSelectionAndActionsVisible(t *testing.T) {
	for _, size := range [][2]int{{60, 16}, {80, 24}, {100, 24}, {231, 62}} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			m := nativeConsoleFixture()
			m.width, m.height = size[0], size[1]
			m.reviewNativeDelivery(false)
			m.nativeAction.field = 8
			view := ansi.Strip(m.View())
			for _, text := range []string{"Returned results", "Enter reviews", "pi", "policy v7"} {
				if !strings.Contains(view, text) {
					t.Fatalf("missing %q: %s", text, view)
				}
			}
			for _, line := range strings.Split(view, "\n") {
				if ansi.StringWidth(line) > size[0] {
					t.Fatalf("line exceeds width: %q", line)
				}
			}
			if len(strings.Split(view, "\n")) > size[1] {
				t.Fatal("settings exceeds terminal height")
			}
		})
	}
}

func TestConsoleNativeSummaryDistinguishesPermissionReadinessAndCapability(t *testing.T) {
	m := nativeConsoleFixture()
	m.data.native.State = "draft"
	m.data.native.Reason = "Preserving your terminal draft"
	m.data.native.Policy.Mode = "auto"
	m.data.native.Active = &managed.NativeReceipt{ID: "delivery-1", Phase: "submitted"}
	view := ansi.Strip(m.nativeDeliverySummary(100))
	for _, text := range []string{"auto", "draft", "Turns 0/20", "pending 2", "submitted", "instructions on", "replies on", "Shift+D"} {
		if !strings.Contains(view, text) {
			t.Fatalf("missing native evidence %q: %s", text, view)
		}
	}
	m.offline = true
	if text := m.nativeDeliverySummary(100); !strings.Contains(text, "current delivery unknown") || strings.Contains(text, "auto") {
		t.Fatal("offline data appears current")
	}
}

func TestConsoleNativeQueuePageDoesNotClaimToBeComplete(t *testing.T) {
	m := nativeConsoleFixture()
	m.data.native.Policy.Mode = "review"
	m.data.native.PendingCount = 200
	m.data.native.PendingMore = true
	if view := ansi.Strip(m.nativeDeliverySummary(100)); !strings.Contains(view, "pending 200") || !strings.Contains(view, "More queued items follow") {
		t.Fatalf("partial native queue was hidden: %s", view)
	}
	m.reviewNativeDelivery(true)
	if view := ansi.Strip(m.nativeDeliveryPanel(80, 16)); !strings.Contains(view, "More queued items follow") {
		t.Fatalf("partial approval page was hidden: %s", view)
	}
}

func TestConsoleNativeWeakerAdapterGuaranteesStayVisibleBeforeAuto(t *testing.T) {
	m := nativeConsoleFixture()
	m.data.native.Adapter = "claude"
	m.data.native.Capabilities.CompletionEvidence = "receiver-ack"
	m.reviewNativeDelivery(false)
	m.nativeAction.policy.Mode = "auto"
	m.nativeAction.confirm = true
	m.nativeAction.offset = 100
	if view := ansi.Strip(m.nativeDeliveryPanel(100, 20)); !strings.Contains(view, "Draft preservation is not verified") || !strings.Contains(view, "cannot be retracted") {
		t.Fatalf("weaker adapter guarantees hidden in final review: %s", view)
	}
	if view := ansi.Strip(m.nativeDeliverySummary(100)); !strings.Contains(view, "Draft preservation unverified") || !strings.Contains(view, "cancellation unavailable") {
		t.Fatalf("weaker guarantees missing from summary: %s", view)
	}
	if view := ansi.Strip(m.nativeDeliverySummary(120)); !strings.Contains(view, "completion: receiver acknowledgment") {
		t.Fatalf("receiver acknowledgment presented as native completion: %s", view)
	}
}

func TestConsoleNativeFailedDraftRequiresExplicitCurrentVersionReview(t *testing.T) {
	m := nativeConsoleFixture()
	m.reviewNativeDelivery(false)
	draft := *m.nativeAction
	draft.policy.Mode = "auto"
	draft.policy.MaxTurns = 42
	draft.confirm = true
	m.nativeAction = nil
	next, _ := m.Update(consoleAction{err: &managed.Fault{Code: "conflict", Message: "policy changed"}, nativeDraft: &draft})
	m = next.(console)
	if m.nativeAction == nil || !m.nativeAction.needsRefresh || m.nativeAction.policy.Mode != "auto" || m.nativeAction.policy.MaxTurns != 42 {
		t.Fatal("CAS failure lost native policy draft")
	}
	m, cmd := consolePress(m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd != nil || m.busy {
		t.Fatal("failed stale review could immediately resubmit")
	}
	current := m.data.native
	current.Policy.Version = 8
	current.Policy.Mode = "review"
	current.Policy.Notes = true
	next, _ = m.Update(consoleNativeVersionReview{state: m.state, agent: "pi", version: 7, current: current})
	m = next.(console)
	if m.nativeAction.latest == nil || !strings.Contains(m.nativeDeliveryPanel(100, 40), "Your retained draft") {
		t.Fatal("latest-versus-draft comparison unavailable")
	}
	m, cmd = consolePress(m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd != nil || m.nativeAction.latest != nil || m.nativeAction.needsRefresh || !m.nativeAction.confirm {
		t.Fatal("adopting version saved or skipped final review")
	}
	_, args := m.nativeAction.args()
	if args["version"] != 8 || args["mode"] != "auto" || args["max_turns"] != 42 || args["notes"] != false {
		t.Fatalf("explicit version adoption replaced retained choices: %v", args)
	}
}

func TestConsoleNativeApprovalRefreshCannotApproveChangedCandidate(t *testing.T) {
	m := nativeConsoleFixture()
	m.data.native.Policy.Mode = "review"
	m.reviewNativeDelivery(true)
	m.nativeAction.confirm = true
	m.nativeAction.needsRefresh = true
	current := m.data.native
	current.Policy.Version = 8
	current.Pending = append([]nativebridge.Delivery(nil), current.Pending...)
	current.Pending[0].Text = "Different request under same proposed identity"
	next, _ := m.Update(consoleNativeVersionReview{state: m.state, agent: "pi", version: 7, current: current})
	m = next.(console)
	m, cmd := consolePress(m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd != nil || m.nativeAction.policy.Version != 7 || m.nativeAction.latest == nil || !strings.Contains(m.message, "no longer eligible") {
		t.Fatal("version refresh widened an exact request approval")
	}
}

func TestConsoleNativeApprovalReviewKeepsTailBeyondMessageLimitPrefix(t *testing.T) {
	m := nativeConsoleFixture()
	m.data.native.Policy.Mode = "review"
	m.data.native.Pending[0].Text = strings.Repeat("x", (1<<20)+200) + "\nFULL_OBSERVATION_TAIL"
	m.reviewNativeDelivery(true)
	m.nativeAction.confirm = true
	m.nativeAction.offset = 1 << 22
	if view := ansi.Strip(m.nativeDeliveryPanel(80, 20)); !strings.Contains(view, "FULL_OBSERVATION_TAIL") {
		t.Fatal("valid bounded observation prefix caused its tail to be omitted from approval")
	}
}
