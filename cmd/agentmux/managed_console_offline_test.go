package main

import (
	"errors"
	"strings"
	"testing"

	"agentmux/internal/managed"
	"agentmux/internal/managedconfig"
	"agentmux/internal/managedlaunch"
)

func TestConsoleOfflineStatusSurvivesNavigationMessagesAndQualifiesSavedCounters(t *testing.T) {
	agents := []managed.AgentInfo{{ID: "worker", State: "active", Config: managedconfig.Agent{Harness: "command", Autonomy: managedconfig.Autonomy{Enabled: true}}}}
	m := console{tab: 1, width: 80, height: 24, agents: agents, data: consoleData{selected: "worker", autonomy: managed.AutonomyStatus{Agent: "worker", Enabled: true, Mode: "running", State: "working", Inflight: 1, Pending: 2}, runtime: managedlaunch.Runtime{Session: "work", State: "running"}}}
	next, _ := m.Update(consoleData{selected: "worker", err: errors.New("broker disconnected")})
	m = next.(console)
	// Selection feedback must not hide loss of broker observability.
	m.message = "Board: #research"
	view := m.View()
	for _, text := range []string{"BROKER OFFLINE", "saved snapshot", "writes disabled", "Saved runtime snapshot", "execution now unknown", "in-flight 1"} {
		if !strings.Contains(view, text) {
			t.Fatalf("offline runtime presented without %q", text)
		}
	}
	fresh := m.data
	fresh.agents = agents
	next, _ = m.Update(fresh)
	m = next.(console)
	if m.offline || strings.Contains(m.View(), "BROKER OFFLINE") || strings.Contains(m.View(), "execution now unknown") {
		t.Fatal("successful current refresh did not restore live observation")
	}
}

func TestConsoleOfflineHeaderPreservesIndependentNativeInteraction(t *testing.T) {
	m := console{tab: 1, width: 80, height: 24, offline: true, agents: []managed.AgentInfo{{ID: "claude", State: "active", Config: managedconfig.Agent{Interaction: "interactive"}}}}
	view := m.View()
	if !strings.Contains(view, "BROKER OFFLINE") || !strings.Contains(view, "Human-led native session") || !strings.Contains(view, "Enter native terminal") {
		t.Fatal("offline broker obscured independent native terminal interaction")
	}
	if strings.Contains(view, "execution now unknown") {
		t.Fatal("managed execution counters were attributed to a native interactive session")
	}
}
