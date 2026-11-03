package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"agentmux/internal/managed"
	"agentmux/internal/managedlaunch"
)

func TestWorkTasksReviewCommandHintsPreserveQuotesAndRejectControlValues(t *testing.T) {
	state, client, _ := inventoryBroker(t)
	const ordinaryQuery = "needle 'quoted'"
	const instruction = ordinaryQuery + " escape:\x1b[31m bidi:\u202e newline:\nnext"
	for i := range 2 {
		var out, diagnostics bytes.Buffer
		if code := runWorkSession("send", []string{"peer", fmt.Sprintf("%s %d", instruction, i), "--state", state}, strings.NewReader(""), &out, &diagnostics); code != exitSuccess {
			t.Fatalf("seed task: %d %s", code, diagnostics.String())
		}
	}
	raw, err := workCall(context.Background(), client, "task.list", map[string]any{"limit": 1, "order": "desc"})
	if err != nil {
		t.Fatal(err)
	}
	var first workTaskPage
	if err = json.Unmarshal(raw, &first); err != nil || len(first.Tasks) != 1 || !first.HasMore {
		t.Fatalf("seed page: %s %v", raw, err)
	}
	for _, tc := range []struct {
		name, state, query string
		unsafeState        bool
	}{
		{name: "ordinary quotes", state: state, query: ordinaryQuery},
		{name: "escape query", state: state, query: "\x1b[31m"},
		{name: "bidi query", state: state, query: "\u202e"},
		{name: "newline query", state: state, query: "\nnext"},
		{name: "control state", query: ordinaryQuery, unsafeState: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.unsafeState {
				tc.state = filepath.Join(t.TempDir(), "state\x1b[31m\n\u202e")
				// Read the same disposable broker through a different state path.
				// The private credential retains the fixture's original socket.
				if err := os.Symlink(state, tc.state); err != nil {
					t.Fatal(err)
				}
			}
			args := []string{"--state", tc.state, "--to", "peer", "--status", "proposed", "--query", tc.query, "--limit", "1"}
			var out, diagnostics bytes.Buffer
			if code := runWorkTasks("tasks", args, &out, &diagnostics); code != exitSuccess {
				t.Fatalf("tasks: %d %s", code, diagnostics.String())
			}
			if strings.ContainsAny(out.String()+diagnostics.String(), "\x1b\u202e") {
				t.Fatalf("terminal controls escaped their display boundary: %q %q", out.String(), diagnostics.String())
			}
			inspect := managedlaunch.ShellCommand([]string{"amx", "task", first.Tasks[0].ID, "--state", tc.state})
			more := managedlaunch.ShellCommand([]string{"amx", "tasks", "--state", tc.state, "--limit", "1", "--to", "peer", "--status", "proposed", "--query", tc.query, "--after", first.Cursor})
			if tc.name == "ordinary quotes" {
				if !strings.Contains(out.String(), "Inspect: "+inspect) || !strings.Contains(out.String(), "More: "+more) {
					t.Fatalf("ordinary quoted follow-up lost exact argv: %s", out.String())
				}
			} else {
				if strings.Contains(out.String(), "More: "+more) || tc.unsafeState && strings.Contains(out.String(), "Inspect: "+inspect) {
					t.Fatalf("unsafe value was offered as a copyable command: %q", out.String())
				}
				if !tc.unsafeState && !strings.Contains(out.String(), "Inspect: "+inspect) {
					t.Fatalf("safe exact-task follow-up was lost: %s", out.String())
				}
			}
			out.Reset()
			if code := runWorkTasks("tasks", append(args, "--json"), &out, &diagnostics); code != exitSuccess {
				t.Fatalf("JSON tasks: %d %s", code, diagnostics.String())
			}
			var page workTaskPage
			if err := json.Unmarshal(out.Bytes(), &page); err != nil || len(page.Tasks) != 1 || page.Tasks[0].Text != instruction+" 1" {
				t.Fatalf("JSON did not retain exact task evidence: %q %v", out.String(), err)
			}
		})
	}
}

func TestWorkTasksReviewRecoveryHintKeepsExactWorkspaceWithoutTerminalControls(t *testing.T) {
	for _, state := range []string{"/workspace 'exact'", "/workspace\x1b[31m\n\u202e"} {
		var out bytes.Buffer
		printWorkTask(&out, state, managed.Task{ID: "task_exact", To: "peer", State: "running", Delivery: "dispatching", Version: 7,
			Recovery: &managed.TaskRecovery{Delivery: "uncertain", Actions: []string{"fail", "retry"}, Reason: "Prior process outcome is unconfirmed."}})
		if strings.ContainsAny(out.String(), "\x1b\u202e") {
			t.Fatalf("recovery workspace emitted terminal controls: %q", out.String())
		}
		command := managedlaunch.ShellCommand([]string{"amx", "ui", "--state", state})
		if strings.ContainsAny(state, "\x1b\u202e\n") {
			if strings.Contains(out.String(), command) {
				t.Fatalf("unsafe recovery hint was offered as a copyable command: %q", out.String())
			}
		} else if !strings.Contains(out.String(), command) {
			t.Fatalf("recovery hint lost the exact quoted workspace: %s", out.String())
		}
	}
}

func TestWorkTasksReviewReceiptMetadataCannotReorderHumanEvidence(t *testing.T) {
	var out bytes.Buffer
	printWorkTask(&out, "/workspace", managed.Task{ID: "task_exact", To: "peer", State: "running", Delivery: "uncertain", Version: 7,
		Native: &managed.NativeReceipt{Reason: "native reason \u202ealtered"},
		Trace:  &managed.TraceSummary{Coverage: "visible coverage \u202ealtered"}})
	if strings.ContainsAny(out.String(), "\x1b\u202e") {
		t.Fatalf("receipt JSON bypassed the human display boundary: %q", out.String())
	}
	if !strings.Contains(out.String(), "Native receipt:") || !strings.Contains(out.String(), "Trace:") || !strings.Contains(out.String(), `\u202ealtered`) {
		t.Fatalf("receipt metadata was lost instead of escaped for display: %s", out.String())
	}
}
