package main

import (
	"bytes"
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"agentmux/internal/managed"
	"agentmux/internal/nativebridge"
)

func TestPairCLIPreviewsWithoutMutationAndAppliesOneTaskOnlyPair(t *testing.T) {
	state, client := permissionCLIBroker(t)
	ctx := context.Background()
	if _, err := workCall(ctx, client, "instruct", map[string]any{"to": []string{"bob"}, "text": "PRIVATE_EXISTING_REQUEST_BODY"}); err != nil {
		t.Fatal(err)
	}
	before := permissionCLIControls(t, client)
	var out, diagnostics bytes.Buffer
	code := runWorkPair([]string{"alice", "bob", "--delivery", "auto", "--state", state}, &out, &diagnostics)
	if code != 0 || diagnostics.Len() != 0 || !reflect.DeepEqual(before, permissionCLIControls(t, client)) {
		t.Fatalf("preview changed permissions or failed: %d %s %s", code, out.String(), diagnostics.String())
	}
	for _, want := range []string{"Preview only", "BOTH directions", "AUTO CAN START EXISTING", "ALL their already-authorized senders", "newly eligible", "Queued: instruction", "no settings changed"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("preview omitted %q: %s", want, out.String())
		}
	}
	if strings.Contains(out.String(), "PRIVATE_EXISTING_REQUEST_BODY") {
		t.Fatal("pair metadata preview printed a private queued body")
	}
	for _, id := range []string{"alice", "bob"} {
		raw, err := workCall(ctx, client, "native.status", map[string]any{"agent": id})
		var status struct {
			Status managed.NativeStatus `json:"status"`
		}
		if err != nil || json.Unmarshal(raw, &status) != nil || status.Status.Policy.Version != 1 || status.Status.Policy.Mode != "manual" {
			t.Fatalf("preview changed native policy for %s: %s %v", id, raw, err)
		}
	}
	out.Reset()
	code = runWorkPair([]string{"alice", "bob", "--delivery", "auto", "--apply", "--state", state}, &out, &diagnostics)
	if code != 0 || !strings.Contains(out.String(), "Task pair saved") {
		t.Fatalf("apply failed: %d %s %s", code, out.String(), diagnostics.String())
	}
	for i, id := range []string{"alice", "bob"} {
		peer := []string{"bob", "alice"}[i]
		agent := permissionCLIClient(t, state, id)
		if _, err := workCall(ctx, agent, "instruct", map[string]any{"to": []string{peer}, "text": "Pair task"}); err != nil {
			t.Fatal(err)
		}
		if _, err := workCall(ctx, agent, "message.send", map[string]any{"to": []string{peer}, "text": "Still no message grant"}); err == nil {
			t.Fatal("task pair granted notes")
		}
	}
	after := permissionCLIControls(t, client)
	if len(after.Edges) != 2 || len(after.Scopes) != 0 || len(after.Groups) != 0 {
		t.Fatalf("pair changed shared scopes: %+v", after)
	}
}

func TestPairReviewModeChoiceKeepsTargetsVersionsQueueAndOriginalFrozen(t *testing.T) {
	_, client := permissionCLIBroker(t)
	review, err := prepareWorkPairReview(context.Background(), client, "alice", "bob", "review")
	if err != nil {
		t.Fatal(err)
	}
	// Older controllers return a pending page without its additive count field.
	review.statuses[1].Pending = []nativebridge.Delivery{{Kind: "instruction", From: "alice"}}
	review.statuses[1].PendingCount = 0
	review.statuses[1].PendingKnown = true
	review = review.withMode("review")
	auto := review.withMode("auto")
	if review.request.Mode != "review" || auto.request.Mode != "auto" || !reflect.DeepEqual(review.request.Agents, auto.request.Agents) || review.request.Version != auto.request.Version || !reflect.DeepEqual(review.statuses, auto.statuses) {
		t.Fatal("mode choice replaced a frozen target, version, session or queue")
	}
	if !strings.Contains(auto.body, "known eligible queue 1") || !strings.Contains(auto.body, "AUTO CAN START EXISTING") || strings.Contains(review.body, "AUTO CAN START EXISTING") {
		t.Fatal("mode preview lost queue coverage or changed the original review")
	}
	if _, err := workCall(context.Background(), client, "native.configure", map[string]any{"agent": "bob", "version": review.request.Agents[1].PolicyVersion, "mode": "review"}); err != nil {
		t.Fatal(err)
	}
	if _, err := workCall(context.Background(), client, "control.pair", auto.args()); err == nil {
		t.Fatal("frozen pair bypassed a native policy conflict")
	}
	if len(permissionCLIControls(t, client).Edges) != 0 {
		t.Fatal("stale pair left partial grants")
	}
}

func TestPairCLIRejectsAmbiguityBeforeWorkspaceIOAndHelpIsPortable(t *testing.T) {
	for _, args := range [][]string{
		{"alice", "bob", "--delivery", "auto", "--delivery", "review"},
		{"alice", "bob", "--apply", "--apply"},
		{"alice", "bob", "--session", "one", "--state", "/missing"},
		{"alice", "bob", "--delivery", "manual"},
	} {
		var out, diagnostics bytes.Buffer
		if code := runWorkPair(args, &out, &diagnostics); code == 0 || strings.Contains(diagnostics.String(), "workspace not found") {
			t.Fatalf("ambiguous pair reached workspace I/O: %v %d %s", args, code, diagnostics.String())
		}
	}
	var out, diagnostics bytes.Buffer
	if runWorkPair([]string{"--help"}, &out, &diagnostics) != 0 || diagnostics.Len() != 0 || !strings.Contains(out.String(), "--apply") {
		t.Fatal("pair help needs a workspace or omits the apply boundary")
	}
	got, err := normalizeWorkCLI([]string{"-s", "daily", "pair", "--delivery", "auto", "alice", "bob", "--apply"})
	want := []string{"pair", "alice", "bob", "--delivery", "auto", "--apply", "--session", "daily"}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("pair normalization changed intent: %#v %v", got, err)
	}
}
