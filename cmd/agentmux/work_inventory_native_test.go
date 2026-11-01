package main

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"agentmux/internal/managed"
	"agentmux/internal/managedlaunch"
	"agentmux/internal/sessioninventory"
)

func TestInventorySeparatesCoordinationFromWorkerLiveness(t *testing.T) {
	agent := managed.AgentInfo{ID: "peer", State: "active"}
	cases := []struct {
		name, runtimeState, workerState string
		runtimeErr                      error
		want                            string
	}{
		{name: "running", runtimeState: "running", workerState: "running", want: "worker running"},
		{name: "native exited", runtimeState: "running", workerState: "exited", want: "worker exited"},
		{name: "pane unavailable", runtimeState: "running", workerState: "unavailable", want: "worker unavailable"},
		{name: "stopped", runtimeState: "stopped", workerState: "stopped", want: "worker stopped"},
		{name: "whole runtime unavailable", runtimeState: "unavailable", workerState: "running", want: "worker unavailable"},
		{name: "failed stale running", runtimeState: "failed", workerState: "running", want: "worker unknown"},
		{name: "no metadata", runtimeErr: errors.New("missing metadata"), want: "worker unknown"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			runtime := managedlaunch.Runtime{State: tc.runtimeState, Agents: map[string]managedlaunch.AgentPane{"peer": {State: tc.workerState}}}
			got := workInventoryMode(agent, nil, runtime, tc.runtimeErr)
			if !strings.Contains(got, "coordination active") || !strings.Contains(got, tc.want) {
				t.Fatalf("mode %q; want active coordination and %s", got, tc.want)
			}
		})
	}
	if got := workInventoryMode(agent, nil, managedlaunch.Runtime{}, nil); !strings.Contains(got, "worker unknown") {
		t.Fatal(got)
	}
}

func TestInventoryExternalBridgeUsesObservationNotOwnedWorker(t *testing.T) {
	agent := managed.AgentInfo{ID: "bridge", State: "active", External: true, ExternalRef: "observed:one"}
	runtime := managedlaunch.Runtime{State: "running", Agents: map[string]managedlaunch.AgentPane{"bridge": {State: "running"}}}
	for _, observed := range []bool{false, true} {
		var observations []sessioninventory.Session
		want := "native not currently observed"
		if observed {
			observations = []sessioninventory.Session{{ID: agent.ExternalRef}}
			want = "native observed"
		}
		got := workInventoryMode(agent, observations, runtime, nil)
		if !strings.Contains(got, want) || strings.Contains(got, "worker running") {
			t.Fatalf("external mode %q; want %s without owned liveness", got, want)
		}
	}
}

func TestWorkInventoryShowsExitedWorkerDespiteActiveCoordination(t *testing.T) {
	state, _, _ := inventoryBroker(t)
	previous := describeWorkRuntime
	describeWorkRuntime = func(context.Context, string) (managedlaunch.Runtime, error) {
		return managedlaunch.Runtime{State: "running", Agents: map[string]managedlaunch.AgentPane{"peer": {State: "exited"}}}, nil
	}
	t.Cleanup(func() { describeWorkRuntime = previous })
	var out, diagnostics bytes.Buffer
	if code := runWorkInventory([]string{"--state", state}, &out, &diagnostics); code != exitSuccess {
		t.Fatalf("exit %d: %s", code, diagnostics.String())
	}
	if !strings.Contains(out.String(), "workspace coordination active · worker exited") {
		t.Fatalf("inventory hides dead worker:\n%s", out.String())
	}
}
