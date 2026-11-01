package main

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"agentmux/internal/managed"
	"agentmux/internal/sessioninventory"
	tea "github.com/charmbracelet/bubbletea"
)

func TestConsoleWorkspaceLoadingImmediatelyCapturesInputAndCancelsLateResults(t *testing.T) {
	for _, kind := range []string{"draft", "revoke"} {
		t.Run(kind, func(t *testing.T) {
			t.Setenv("TMUX", "")
			m := sessionSwitcherFixture()
			m.tab = 1
			if kind == "draft" {
				m.compose("send", "alice", "", "UNSENT")
			} else {
				m.reviewStop()
			}
			review, draft := m.ownerReview, string(m.text)
			next, load := m.key(tea.KeyMsg{Type: tea.KeyCtrlW})
			m = next.(console)
			if load == nil || m.workspacePicker == nil || !m.workspacePicker.loading || !strings.Contains(m.View(), "Loading saved workspaces") {
				t.Fatal("loading chooser did not become modal immediately")
			}
			next, action := m.key(tea.KeyMsg{Type: tea.KeyEnter})
			m = next.(console)
			if action != nil || m.busy || m.ownerReview != review || string(m.text) != draft {
				t.Fatal("loading chooser submitted underlying action")
			}
			request := m.workspaceRequest
			next, _ = m.key(tea.KeyMsg{Type: tea.KeyEsc})
			m = next.(console)
			next, _ = m.Update(consoleWorkspaceList{request: request, rows: []workspaceListing{{Session: "late"}}})
			m = next.(console)
			if m.workspacePicker != nil || m.ownerReview != review || string(m.text) != draft {
				t.Fatal("cancelled inventory result reopened chooser or changed draft")
			}
		})
	}
}

func TestConsoleNewerNativeFocusSurvivesOlderSnapshotAndDeferredFocus(t *testing.T) {
	for _, editing := range []bool{false, true} {
		m := sessionSwitcherFixture()
		if editing {
			m.compose("send", "alice", "", "draft")
		}
		old, newer := m.data, m.data
		old.agents, newer.agents = m.agents, m.agents
		old.selected, newer.selected = "alice", "alice"
		old.focusID, old.focusSequence = "bob", 10
		newer.focusID, newer.focusSequence = "carol", 11
		next, _ := m.Update(newer)
		m = next.(console)
		next, _ = m.Update(old)
		m = next.(console)
		if editing {
			if m.selectedID() != "alice" || m.pendingFocus != "carol" || string(m.text) != "draft" {
				t.Fatal("older focus replaced deferred newer identity")
			}
			m.composer = ""
			m.text = nil
			d := m.data
			d.agents = m.agents
			d.selected = "alice"
			d.focusID = ""
			next, _ = m.Update(d)
			m = next.(console)
		}
		if m.selectedID() != "carol" {
			t.Fatal("delayed snapshot reselected an older native session")
		}
	}
}

func TestConsoleHistoryFailureKeepsConnectedRosterAndScopesFallback(t *testing.T) {
	state, client := permissionCLIBroker(t)
	m := console{state: state, client: client, messageQuery: strings.Repeat("x", 5000)}
	d := m.fetch().(consoleData)
	if d.err != nil || d.historyErr == nil || len(d.agents) == 0 || len(d.activity.Messages) != 0 {
		t.Fatalf("history error became global or widened search: %+v", d)
	}
	next, _ := m.Update(d)
	m = next.(console)
	if m.offline || len(m.agents) == 0 || !strings.Contains(m.View(), "Complete history unavailable") {
		t.Fatal("unavailable history discarded the healthy workspace")
	}
	for _, tab := range []int{0, 7} {
		m.tab = tab
		d = m.data
		d.selected = m.selectedID()
		d.historyErr = errors.New("invalid: unknown field agent (older controller)")
		next, _ = m.Update(d)
		m = next.(console)
		if m.offline || !strings.Contains(m.View(), "updated controller") {
			t.Fatal("older controller history disabled all controls")
		}
	}
}

func TestCLIListsKnownWorkspaceOriginsBeforeBridgeSetup(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("TMUX", "")
	state, _, observed := inventoryBroker(t)
	observed.WorkspaceOrigins = []string{"another-workspace"}
	original := discoverWorkSessions
	defer func() { discoverWorkSessions = original }()
	discoverWorkSessions = func(context.Context, string) []sessioninventory.Session { return []sessioninventory.Session{observed} }
	var out, diag bytes.Buffer
	if code := runWorkInventory([]string{"--state", state}, &out, &diag); code != 0 || !strings.Contains(out.String(), "another-workspace") {
		t.Fatalf("inventory provenance: %d %s %s", code, out.String(), diag.String())
	}
	out.Reset()
	diag.Reset()
	if code := runWorkConnect([]string{observed.ID, "--name", "second-identity", "--state", state}, &out, &diag); code != 0 || !strings.Contains(out.String(), "another-workspace") || strings.Index(out.String(), "another-workspace") > strings.Index(out.String(), "This existing session is Agentmux agent") {
		t.Fatalf("bridge hides prior identity: %d %s %s", code, out.String(), diag.String())
	}
}

func TestConsoleNonLiveMemoryIntentSurvivesFailedDraftRestore(t *testing.T) {
	m := console{failedDraft: &consoleDraft{kind: "memory-value", target: "team/note", namespace: "team", key: "note", version: 4, memoryReplace: true, text: "replacement"}}
	m.restoreDraft()
	if !m.memoryReplace || m.memoryVersion != 4 || m.composer != "memory-value" || string(m.text) != "replacement" {
		t.Fatal("restore lost reviewed replacement intent")
	}
}

func TestConsoleExpiredTaskRecoveryUsesOwnerProjectionAndKeepsVersion(t *testing.T) {
	for _, delivery := range []string{"uncertain", "cancel_uncertain"} {
		actions := []string{"fail", "retry"}
		if delivery == "cancel_uncertain" {
			actions = []string{"fail"}
		}
		task := managed.Task{ID: "expired-exact", Version: 7, To: "bob", State: "running", Delivery: "dispatching", Recovery: &managed.TaskRecovery{Delivery: delivery, Actions: actions, Reason: "Execution lease expired; old outcome unconfirmed."}}
		m := console{tab: 4, data: consoleData{activity: managedActivitySnapshot{Tasks: []managed.Task{task}}}}
		m.reviewTaskResolution("fail")
		if m.ownerReview == nil || m.ownerReview.args["version"] != 7 || !strings.Contains(m.ownerReview.body, "lease expired") {
			t.Fatal("expired task did not open exact owner recovery")
		}
		m.ownerReview = nil
		m.reviewTaskResolution("retry")
		if (m.ownerReview != nil) != (delivery == "uncertain") {
			t.Fatal("expired cancellation recovery allowed retry or ordinary expiry hid it")
		}
		if got := m.menuActionReason(consoleMenuAction{key: "resolve-fail", taskID: task.ID}); got != "" {
			t.Fatal(got)
		}
	}
}

func TestConsoleIsolationReviewEnumeratesPermanentTeamEffects(t *testing.T) {
	m := sessionSwitcherFixture()
	m.data.teams = []managed.Team{
		{ID: "owned", Name: "Owned", Owner: "alice", State: "active", Version: 2, Members: []string{"alice", "bob"}},
		{ID: "joined", Owner: "bob", State: "active", Version: 3, Members: []string{"alice", "bob"}},
		{ID: "invited", Owner: "bob", State: "active", Version: 4, Invited: []string{"alice"}},
	}
	m.reviewIsolation()
	if m.ownerReview == nil {
		t.Fatal("no isolation review")
	}
	for _, text := range []string{"DISSOLVE for all members", "REMOVE membership", "WITHDRAW pending invitation", "Reconnecting does not restore"} {
		if !strings.Contains(m.ownerReview.body, text) {
			t.Fatal("missing impact", text)
		}
	}
	impacts, ok := m.ownerReview.args["expected_teams"].([]managed.IsolationTeamImpact)
	if !ok || len(impacts) != 3 {
		t.Fatal("team effects were not frozen with their versions")
	}
	m.ownerReview = nil
	m.data.teamsErr = errors.New("unavailable")
	m.reviewIsolation()
	if m.ownerReview != nil || !strings.Contains(m.message, "Team impact is unavailable") {
		t.Fatal("unknown team impact was treated as empty")
	}
}
