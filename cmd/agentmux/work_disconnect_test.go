package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestUnlinkDisablesExactGroupAndPreservesSavedCommandChoice(t *testing.T) {
	state, client := permissionCLIBroker(t)
	controls := permissionCLIControls(t, client)
	if _, err := workCall(context.Background(), client, "control.update", map[string]any{"version": controls.Version, "kind": "collaboration", "agents": []string{"alice", "bob"}, "enabled": true, "commands": true}); err != nil {
		t.Fatal(err)
	}
	var out, diag bytes.Buffer
	if code := runWorkSession("disconnect", []string{"bob", "alice", "--state", state}, strings.NewReader(""), &out, &diag); code != 0 {
		t.Fatalf("disconnect %d %s %s", code, out.String(), diag.String())
	}
	controls = permissionCLIControls(t, client)
	if len(controls.Groups) != 1 || controls.Groups[0].State == "active" || !controls.Groups[0].Commands {
		t.Fatalf("unlink changed saved preferences: %+v", controls.Groups)
	}
	if permissionCLIBoundary(t, client, "alice", "bob", "instruct") {
		t.Fatal("disabled exact group still contributes instruction grant")
	}
}

func TestRetireCLIKeepsImmutableIdentityAndListsArchived(t *testing.T) {
	state, client := permissionCLIBroker(t)
	if _, err := workCall(context.Background(), client, "agent.stop", map[string]any{"id": "alice"}); err != nil {
		t.Fatal(err)
	}
	var out, diag bytes.Buffer
	if code := runWorkSession("retire", []string{"alice", "--state", state}, strings.NewReader(""), &out, &diag); code != 0 {
		t.Fatalf("retire: %d %s", code, diag.String())
	}
	rows, err := workArchivedRegistry(context.Background(), client)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if row.ID == "alice" && row.State == "retired" {
			return
		}
	}
	t.Fatal("retired stable identity disappeared")
}
