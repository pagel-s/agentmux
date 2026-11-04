package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"agentmux/internal/managed"
	"agentmux/internal/managedlaunch"
)

func TestObserveRetainedCountsBoundedAttentionAndNoTaskMutation(t *testing.T) {
	state, client, _ := inventoryBroker(t)
	previous := describeWorkRuntime
	t.Cleanup(func() { describeWorkRuntime = previous })
	describeWorkRuntime = func(context.Context, string) (managedlaunch.Runtime, error) {
		return managedlaunch.Runtime{}, os.ErrNotExist // legitimate headless broker
	}
	for i := 0; i < 28; i++ {
		_, err := workCall(context.Background(), client, "instruct", map[string]any{"to": []string{"peer"}, "text": fmt.Sprintf("private task body %d", i)})
		if err != nil {
			t.Fatal(err)
		}
	}
	before, err := workCall(context.Background(), client, "task.list", map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	var out, diagnostics bytes.Buffer
	if code := runWorkObserveContext(context.Background(), []string{"--state", state, "--limit", "2", "--json"}, &out, &diagnostics); code != exitSuccess {
		t.Fatalf("observation: %d %s %s", code, out.String(), diagnostics.String())
	}
	var result workObservation
	if err = json.Unmarshal(out.Bytes(), &result); err != nil || !result.Complete || result.ObserverVersion != versionString() || result.Broker != "connected" || result.Runtime.State != "not_recorded" || result.State != state || result.Source != "explicit --state" {
		t.Fatalf("bad headless observation: %+v %v", result, err)
	}
	if result.Snapshot.Tasks != 28 || result.Snapshot.AttentionCount != 28 || len(result.Snapshot.Attention) != 2 || !result.Snapshot.AttentionMore {
		t.Fatalf("recent window or limit hid history: %+v", result.Snapshot)
	}
	if strings.Contains(out.String(), "private task body") || strings.Contains(out.String(), "lease_token") {
		t.Fatal("operational snapshot contains task body or private lease")
	}
	after, err := workCall(context.Background(), client, "task.list", map[string]any{})
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("observation changed retained task state: %v", err)
	}
	out.Reset()
	if err = formatWorkObservation(&out, result); err != nil || !strings.Contains(out.String(), "28 retained") || !strings.Contains(out.String(), "Attention list is truncated") || !strings.Contains(out.String(), "amx task") {
		t.Fatalf("missing operational drilldown: %s %v", out.String(), err)
	}
}

func TestObserveFailedRuntimeCheckCannotPublishCachedLiveness(t *testing.T) {
	state, _, _ := inventoryBroker(t)
	previous := describeWorkRuntime
	t.Cleanup(func() { describeWorkRuntime = previous })
	describeWorkRuntime = func(context.Context, string) (managedlaunch.Runtime, error) {
		return managedlaunch.Runtime{Session: "retained", State: "running", Agents: map[string]managedlaunch.AgentPane{"peer": {State: "running", PaneID: "%1"}}}, errors.New("ownership inspection unavailable")
	}
	var out, diagnostics bytes.Buffer
	code := runWorkObserveContext(context.Background(), []string{"--state", state, "--json"}, &out, &diagnostics)
	var result workObservation
	if code != exitRuntime || json.Unmarshal(out.Bytes(), &result) != nil || result.Complete || result.Snapshot == nil || result.Runtime.State != "unknown" || result.Runtime.Agents["peer"].State != "unknown" || result.RuntimeError == "" {
		t.Fatalf("cached running state became liveness: %d %s", code, out.String())
	}
}

func TestObserveMissingWorkspaceAndValidationCreateNoState(t *testing.T) {
	t.Setenv("TMUX", "")
	state := filepath.Join(t.TempDir(), "absent")
	for _, args := range [][]string{{"--limit", "0"}, {"--limit", "201"}, {"--interval", "1s"}, {"--watch", "--interval", "0s"}, {"--watch", "--interval", "61s"}, {"--session", "other"}, {"unexpected"}, {"--project", "."}} {
		var out, diagnostics bytes.Buffer
		if code := runWorkObserveContext(context.Background(), append([]string{"--state", state}, args...), &out, &diagnostics); code != exitValidation || out.Len() != 0 {
			t.Fatalf("invalid input reached observation: %q %d %s", args, code, out.String())
		}
	}
	var out, diagnostics bytes.Buffer
	code := runWorkObserveContext(context.Background(), []string{"--state", state, "--json"}, &out, &diagnostics)
	var result workObservation
	if code != exitRuntime || json.Unmarshal(out.Bytes(), &result) != nil || result.Complete || result.Broker != "unavailable" || result.Snapshot != nil || result.BrokerError == "" {
		t.Fatalf("missing broker appeared healthy: %d %s", code, out.String())
	}
	if _, err := os.Stat(state); !os.IsNotExist(err) {
		t.Fatalf("read-only observation created state: %v", err)
	}
}

func TestNativeObservationSeparatesStaleReportingFromReceipt(t *testing.T) {
	status := managed.NativeStatus{ReadinessObservedAt: time.Now().UTC().Add(-time.Minute).Format(time.RFC3339Nano), Active: &managed.NativeReceipt{ID: "delivery_one", Phase: "running", UpdatedAt: "2026-11-01T12:00:00Z"}}
	text := nativeObservationSummary(status)
	if !strings.Contains(text, "not reporting") || !strings.Contains(text, "not confirmed") || status.Active.Phase != "running" {
		t.Fatalf("stale reporting changed or confirmed execution: %s %+v", text, status)
	}
	status.ReadinessObservedAt = ""
	if !strings.Contains(nativeObservationSummary(status), "unknown") {
		t.Fatal("missing readiness was treated as current")
	}
}

func TestObserveStorageIsBoundedAndDoesNotFollowLinks(t *testing.T) {
	state := t.TempDir()
	if err := os.WriteFile(filepath.Join(state, "state.sqlite"), []byte("private bytes"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(state, "state.sqlite"), filepath.Join(state, "state.sqlite-wal")); err != nil {
		t.Fatal(err)
	}
	result := observeWorkStorage(state)
	if file := result.Files["database"]; file.State != "present" || file.Bytes == nil || *file.Bytes != 13 {
		t.Fatalf("regular database size missing: %+v", file)
	}
	if file := result.Files["wal"]; file.State != "unknown" || file.Bytes != nil || file.Problem == "" {
		t.Fatalf("followed link or reported unknown size as zero: %+v", file)
	}
	if file := result.Files["shared_memory"]; file.State != "absent" || file.Bytes == nil || *file.Bytes != 0 {
		t.Fatalf("absence is not represented separately: %+v", file)
	}
	raw, _ := json.Marshal(result)
	if strings.Contains(string(raw), "private bytes") || len(result.Files) != 3 {
		t.Fatalf("file contents or unbounded paths entered observation: %s", raw)
	}
}

type stopObservationWriter struct {
	bytes.Buffer
	stop context.CancelFunc
}

func (w *stopObservationWriter) Write(p []byte) (int, error) {
	n, err := w.Buffer.Write(p)
	w.stop()
	return n, err
}

func TestObserveWatchPublishesPartialJSONThenStopsWithoutCreatingState(t *testing.T) {
	t.Setenv("TMUX", "")
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	state := filepath.Join(t.TempDir(), "missing")
	out := &stopObservationWriter{stop: stop}
	var diagnostics bytes.Buffer
	if code := runWorkObserveContext(ctx, []string{"--state", state, "--watch", "--json"}, out, &diagnostics); code != exitSuccess {
		t.Fatalf("watch interruption: %d %s", code, diagnostics.String())
	}
	var result workObservation
	if json.Unmarshal(out.Bytes(), &result) != nil || result.Complete || result.BrokerError == "" || bytes.Count(out.Bytes(), []byte("\n")) != 1 {
		t.Fatalf("partial JSONL lost its error or framing: %s", out.String())
	}
	if _, err := os.Stat(state); !os.IsNotExist(err) {
		t.Fatalf("watch created state: %v", err)
	}
}
