package main

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"agentmux/internal/managed"
	"agentmux/internal/managedconfig"
	tea "github.com/charmbracelet/bubbletea"
)

func consolePairFixture() (console, consoleTaskPairRead) {
	pair := workPairReview{
		request: managed.TaskPairRequest{Version: 12, Mode: "review", Agents: []managed.TaskPairAgent{{ID: "claude", PolicyVersion: 3, NativeSession: "native-claude"}, {ID: "codex", PolicyVersion: 7, NativeSession: "native-codex"}}},
		labels:  []string{"Implementation", "Fresh review"},
		statuses: []managed.NativeStatus{
			{Agent: "claude", Session: "native-claude", Policy: managed.NativePolicy{Version: 3, NativeDelivery: managedconfig.NativeDelivery{Mode: "manual", MaxTurns: 9, MaxHops: 2}}},
			{Agent: "codex", Session: "native-codex", Policy: managed.NativePolicy{Version: 7, NativeDelivery: managedconfig.NativeDelivery{Mode: "manual", MaxTurns: 11, MaxHops: 3}}},
		},
	}.withMode("review")
	m := console{state: "/fixture/work", tab: 2, agents: []managed.AgentInfo{{ID: "claude"}, {ID: "codex"}, {ID: "other"}}, target: 1, pairRequest: 4, pairLoading: true, busy: true}
	return m, consoleTaskPairRead{state: m.state, from: "claude", to: "codex", request: 4, review: pair}
}

func TestConsoleTaskPairModesKeepFrozenAuthority(t *testing.T) {
	m, read := consolePairFixture()
	m.receiveTaskPairRead(read)
	if m.ownerReview == nil || m.busy || m.pairLoading {
		t.Fatal("current pair did not open its owner review")
	}
	wantAgents := append([]managed.TaskPairAgent(nil), read.review.request.Agents...)
	// Polling and selection changes cannot retarget the open review. Mode keys
	// belong to this review rather than navigating to Board or Sessions.
	m.agents = []managed.AgentInfo{{ID: "other"}, {ID: "codex"}, {ID: "claude"}}
	m.selected, m.target = 0, 1
	for _, key := range []string{"2", "1", "2"} {
		next, cmd := m.ownerReviewKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)})
		m = next.(console)
		if cmd != nil || m.tab != 2 || m.busy || m.ownerReview == nil {
			t.Fatal("choosing a mode submitted or navigated")
		}
		pair := m.ownerReview.pair
		if pair.request.Version != 12 || !reflect.DeepEqual(pair.request.Agents, wantAgents) || m.ownerReview.args["version"] != 12 {
			t.Fatalf("mode selection changed frozen authority: %+v", pair.request)
		}
	}
	if m.ownerReview.pair.request.Mode != "auto" || !strings.Contains(m.ownerReview.body, "ALL") || !strings.Contains(m.ownerReview.body, "EXISTING") {
		t.Fatal("Auto scope and existing queue were hidden")
	}
	next, cmd := m.ownerReviewKey(tea.KeyMsg{Type: tea.KeyEsc})
	m = next.(console)
	if cmd != nil || m.ownerReview != nil || m.busy {
		t.Fatal("cancelling the review submitted a change")
	}
}

func TestConsoleTaskPairLateReadsCannotReplaceCurrentIntent(t *testing.T) {
	for _, scenario := range []string{"workspace", "request", "sender", "recipient", "view", "draft"} {
		t.Run(scenario, func(t *testing.T) {
			m, read := consolePairFixture()
			switch scenario {
			case "workspace":
				read.state = "/another/work"
			case "request":
				read.request--
			case "sender":
				m.selected = 2
			case "recipient":
				m.target = 2
			case "view":
				m.tab = 1
			case "draft":
				m.composer, m.text = "send", []rune("keep my draft")
			}
			m.receiveTaskPairRead(read)
			if m.ownerReview != nil {
				t.Fatal("a stale read opened task pairing")
			}
			if scenario == "draft" && (m.composer != "send" || string(m.text) != "keep my draft") {
				t.Fatal("a late read changed a draft")
			}
			if (scenario == "workspace" || scenario == "request") && !m.busy {
				t.Fatal("an unrelated read cleared the current pending action")
			}
		})
	}
}

func TestConsoleTaskPairUncertainResultRetainsExactRequest(t *testing.T) {
	m, read := consolePairFixture()
	m.receiveTaskPairRead(read)
	review := *m.ownerReview
	m.ownerReview, m.busy = nil, true
	m.receiveTaskPairApplied(consoleTaskPairApplied{state: m.state, review: review, err: errors.New("transport lost")})
	if m.busy || m.ownerReview == nil || m.ownerReview.requestID != review.requestID || !reflect.DeepEqual(m.ownerReview.args, review.args) {
		t.Fatal("uncertain result lost the original idempotent request")
	}
	if !strings.Contains(m.ownerReview.body, "unconfirmed") || !strings.Contains(m.ownerReview.body, "No automatic retry") {
		t.Fatal("uncertain result implied a rollback or automatic retry")
	}
	m.ownerReview, m.busy = nil, true
	m.receiveTaskPairApplied(consoleTaskPairApplied{state: "/another/work", review: review, err: errors.New("old failure")})
	if !m.busy || m.ownerReview != nil {
		t.Fatal("another workspace's result changed the current review")
	}
}

func TestConsoleTaskPairDefiniteConflictNeedsFreshReview(t *testing.T) {
	for _, code := range []string{"conflict", "unsupported", "unauthorized"} {
		t.Run(code, func(t *testing.T) {
			m, read := consolePairFixture()
			m.receiveTaskPairRead(read)
			review := *m.ownerReview
			m.ownerReview, m.busy = nil, true
			m.receiveTaskPairApplied(consoleTaskPairApplied{state: m.state, review: review, err: &managed.Fault{Code: code, Message: "pair rejected"}})
			if m.busy || m.ownerReview != nil || !strings.Contains(m.message, "rejected") || !strings.Contains(m.message, "No pair settings changed") {
				t.Fatal("a definite atomic rejection was presented as an uncertain retry")
			}
		})
	}
}

func TestConsoleTaskPairReviewAppliesThroughActualBroker(t *testing.T) {
	state, client := permissionCLIBroker(t)
	ctx := context.Background()
	alice, err := workAgentForLabel(ctx, client, "alice")
	if err != nil {
		t.Fatal(err)
	}
	bob, err := workAgentForLabel(ctx, client, "bob")
	if err != nil {
		t.Fatal(err)
	}
	m := console{state: state, client: client, tab: 2, agents: []managed.AgentInfo{alice, bob}, target: 1}
	before := permissionCLIControls(t, client)
	m, cmd := nativeRune(m, "C")
	if cmd == nil || !m.busy || m.ownerReview != nil {
		t.Fatal("task pairing did not request a read-only concrete review")
	}
	next, _ := m.Update(cmd())
	m = next.(console)
	if m.ownerReview == nil || !reflect.DeepEqual(before, permissionCLIControls(t, client)) {
		t.Fatal("opening the review changed permissions")
	}
	m, cmd = nativeRune(m, "2")
	if cmd != nil || m.ownerReview == nil || m.ownerReview.pair.request.Mode != "auto" {
		t.Fatal("choosing Auto submitted a change")
	}
	m, cmd = consolePress(m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil || !m.busy || m.ownerReview != nil {
		t.Fatal("final confirmation did not submit the atomic request")
	}
	next, _ = m.Update(cmd())
	m = next.(console)
	if m.busy || m.ownerReview != nil || !strings.Contains(m.message, "Task pair saved") {
		t.Fatalf("pair result was not confirmed: %s", m.message)
	}
	for _, ids := range [][2]string{{"alice", "bob"}, {"bob", "alice"}} {
		if !permissionCLIBoundary(t, client, ids[0], ids[1], "instruct") || permissionCLIBoundary(t, client, ids[0], ids[1], "message.send") {
			t.Fatal("pair did not grant exactly mutual task permission")
		}
		raw, err := workCall(ctx, client, "native.status", map[string]any{"agent": ids[0]})
		var response struct {
			Status managed.NativeStatus `json:"status"`
		}
		if err != nil || json.Unmarshal(raw, &response) != nil || response.Status.Policy.Mode != "auto" || !response.Status.Policy.Results {
			t.Fatalf("pair did not enable delivery and returned results: %s %v", raw, err)
		}
	}
}
