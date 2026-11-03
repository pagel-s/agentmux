package main

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"
)

func TestWorkTaskOpenHandsOffExactOlderTaskAndWorkspaceWithoutMutation(t *testing.T) {
	state, client, _ := inventoryBroker(t)
	otherState, _, _ := inventoryBroker(t)
	ctx := context.Background()
	id, kind, err := workSend(ctx, client, state, "peer", "Original instruction")
	if err != nil || kind != "Instruction" {
		t.Fatalf("seed task: %s %s %v", id, kind, err)
	}
	if _, _, err = workSend(ctx, client, state, "peer", "Newer instruction mentions "+id); err != nil {
		t.Fatal(err)
	}
	before, err := workCall(ctx, client, "task.list", map[string]any{"order": "desc"})
	if err != nil {
		t.Fatal(err)
	}
	in := strings.NewReader("input belongs to this workbench")
	calls := 0
	old := openWorkTaskWorkbench
	t.Cleanup(func() { openWorkTaskWorkbench = old })
	openWorkTaskWorkbench = func(selectedState, selectedID string, selectedIn io.Reader, out, diagnostics io.Writer) int {
		calls++
		if selectedState != state || selectedID != id || selectedIn != in {
			t.Fatalf("handoff changed target or input: %q %q %v", selectedState, selectedID, selectedIn)
		}
		return exitSuccess
	}
	var out, diagnostics bytes.Buffer
	args, err := normalizeWorkCLI([]string{"--state", state, "task", id, "-o"})
	if err != nil {
		t.Fatal(err)
	}
	if code := runWorkTasksIO(args[0], args[1:], in, &out, &diagnostics); code != exitSuccess || calls != 1 || out.Len() != 0 {
		t.Fatalf("open: %d calls=%d %s %s", code, calls, out.String(), diagnostics.String())
	}
	for _, args := range [][]string{
		{id, "--open", "--state", otherState},
		{"task_missing", "--open", "--state", state},
	} {
		out.Reset()
		diagnostics.Reset()
		if code := runWorkTasksIO("task", args, in, &out, &diagnostics); code != exitRuntime || calls != 1 || out.Len() != 0 {
			t.Fatalf("missing task opened another view: %q %d calls=%d %s %s", args, code, calls, out.String(), diagnostics.String())
		}
	}
	for _, options := range [][]string{{"--json"}, {"--open=false"}} {
		out.Reset()
		diagnostics.Reset()
		args := append([]string{id, "--state", state}, options...)
		if code := runWorkTasksIO("task", args, in, &out, &diagnostics); code != exitSuccess || calls != 1 {
			t.Fatalf("ordinary inspection opened workbench: %d %s", code, diagnostics.String())
		}
	}
	after, err := workCall(ctx, client, "task.list", map[string]any{"order": "desc"})
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("opening changed task evidence: %s %s %v", before, after, err)
	}
	openWorkTaskWorkbench = func(string, string, io.Reader, io.Writer, io.Writer) int { return exitRuntime }
	if code := runWorkTasksIO("task", []string{id, "--open", "--state", state}, in, &out, &diagnostics); code != exitRuntime {
		t.Fatalf("failed workbench silently fell back: %d", code)
	}
}

func TestTaskOpenIsDiscoverableFromSessionAndTaskHelp(t *testing.T) {
	for _, operation := range []string{"send", "reply", "attach"} {
		var out, diagnostics bytes.Buffer
		if code := runWorkSession(operation, []string{"--help"}, strings.NewReader(""), &out, &diagnostics); code != exitSuccess {
			t.Fatalf("%s help: %d %s", operation, code, diagnostics.String())
		}
		for _, want := range []string{"amx context", "amx task TASK_ID [--json | --open]", "amx tasks", "Opening never retries or resolves"} {
			if !strings.Contains(out.String(), want) {
				t.Fatalf("%s help hides %q: %s", operation, want, out.String())
			}
		}
	}
	var out, diagnostics bytes.Buffer
	if code := runWorkTasks("task", []string{"--help"}, &out, &diagnostics); code != exitSuccess || !strings.Contains(out.String(), "--open (-o)") || !strings.Contains(out.String(), "explicit task/version review") {
		t.Fatalf("task help hides recovery route: %d %s %s", code, out.String(), diagnostics.String())
	}
}
