package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"agentmux/internal/managed"
	"agentmux/internal/managedlaunch"
)

func TestWorkTasksDelegationInspectionAndPagingAreReadOnly(t *testing.T) {
	state, client, _ := inventoryBroker(t)
	var ids []string
	for i := 0; i < 4; i++ {
		var out, diagnostics bytes.Buffer
		if code := runWorkSession("send", []string{"peer", fmt.Sprintf("needle instruction %d \x1b[31m", i), "--state", state}, strings.NewReader(""), &out, &diagnostics); code != exitSuccess {
			t.Fatalf("send: %d %s", code, diagnostics.String())
		}
		raw, err := workCall(context.Background(), client, "task.list", map[string]any{"limit": 1, "order": "desc"})
		if err != nil {
			t.Fatal(err)
		}
		var page workTaskPage
		if err = json.Unmarshal(raw, &page); err != nil || len(page.Tasks) != 1 {
			t.Fatalf("list: %s %v", raw, err)
		}
		id := page.Tasks[0].ID
		ids = append(ids, id)
		if !strings.Contains(out.String(), managedlaunch.ShellCommand([]string{"amx", "task", id, "--state", state})) {
			t.Fatalf("missing same-workspace inspect hint: %s", out.String())
		}
	}
	before, err := workCall(context.Background(), client, "task.list", map[string]any{"order": "desc"})
	if err != nil {
		t.Fatal(err)
	}
	var seen []string
	after := ""
	for {
		args := []string{"--state", state, "--to", "peer", "--status", "proposed", "--query", "needle", "--limit", "2", "--json"}
		if after != "" {
			args = append(args, "--after", after)
		}
		var out, diagnostics bytes.Buffer
		if code := runWorkTasks("tasks", args, &out, &diagnostics); code != exitSuccess {
			t.Fatalf("tasks: %d %s", code, diagnostics.String())
		}
		var page workTaskPage
		if err = json.Unmarshal(out.Bytes(), &page); err != nil {
			t.Fatal(err)
		}
		for _, task := range page.Tasks {
			seen = append(seen, task.ID)
		}
		if !page.HasMore {
			break
		}
		if page.Cursor == after {
			t.Fatal("cursor did not advance")
		}
		after = page.Cursor
	}
	want := []string{ids[3], ids[2], ids[1], ids[0]}
	if !reflect.DeepEqual(seen, want) {
		t.Fatalf("lost retained history: got %v want %v", seen, want)
	}
	var out, diagnostics bytes.Buffer
	if code := runWorkTasks("task", []string{ids[0], "--state", state}, &out, &diagnostics); code != exitSuccess {
		t.Fatalf("task: %d %s", code, diagnostics.String())
	}
	if strings.Contains(out.String(), "\x1b") || !strings.Contains(out.String(), `\x1b[31m`) || !strings.Contains(out.String(), "State: proposed\nDelivery: pending\nVersion: 1") {
		t.Fatalf("unsafe or misleading detail: %s", out.String())
	}
	afterRead, err := workCall(context.Background(), client, "task.list", map[string]any{"order": "desc"})
	if err != nil || !bytes.Equal(before, afterRead) {
		t.Fatalf("inspection changed task evidence: %s %s %v", before, afterRead, err)
	}
	out.Reset()
	if code := runWorkTasks("tasks", []string{"--state", state, "--query", "absent"}, &out, &diagnostics); code != exitSuccess || !strings.Contains(out.String(), "No matching tasks.") {
		t.Fatalf("empty search: %d %s %s", code, out.String(), diagnostics.String())
	}
}

func TestWorkTasksValidationCreatesNoWorkspace(t *testing.T) {
	t.Setenv("TMUX", "")
	state := filepath.Join(t.TempDir(), "absent")
	for _, tc := range []struct {
		op   string
		args []string
	}{
		{"task", nil}, {"task", []string{""}}, {"task", []string{"task_one", "task_two"}},
		{"task", []string{"task_one", "--limit", "25"}},
		{"task", []string{"task_one", "--open", "--json"}},
		{"task", []string{"task_one", "--open", "--open"}},
		{"tasks", []string{"--open"}}, {"tasks", []string{"--open=false"}},
		{"task", []string{"../escape"}}, {"tasks", []string{"task_one"}},
		{"tasks", []string{"--limit", "0"}}, {"tasks", []string{"--limit", "201"}},
		{"tasks", []string{"--status", "done"}}, {"tasks", []string{"--to", "../peer"}},
		{"tasks", []string{"--query", strings.Repeat("x", 513)}},
		{"tasks", []string{"--session", "other"}},
		{"tasks", []string{"--limit", "2", "--limit", "3"}},
	} {
		var out, diagnostics bytes.Buffer
		args := append(append([]string(nil), tc.args...), "--state", state)
		if code := runWorkTasks(tc.op, args, &out, &diagnostics); code != exitValidation || out.Len() != 0 {
			t.Fatalf("accepted %s %q: %d %s %s", tc.op, args, code, out.String(), diagnostics.String())
		}
	}
	if _, err := os.Stat(state); !os.IsNotExist(err) {
		t.Fatalf("validation created state: %v", err)
	}
}

func TestWorkTaskRecoveryEvidenceDoesNotImplyStoppedOrVerified(t *testing.T) {
	var out bytes.Buffer
	printWorkTask(&out, "/workspace 'exact'", managed.Task{ID: "task_old", To: "peer", State: "running", Delivery: "dispatching", Version: 7,
		Recovery: &managed.TaskRecovery{Delivery: "uncertain", Actions: []string{"fail", "retry"}, Reason: "Lease expired; stop the prior process before retrying."}})
	for _, want := range []string{"State: running", "Delivery: dispatching", "Version: 7", "Recovery delivery: uncertain", "stop the prior process", managedlaunch.ShellCommand([]string{"amx", "task", "task_old", "--open", "--state", "/workspace 'exact'"})} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("lost evidence %q: %s", want, out.String())
		}
	}
}
