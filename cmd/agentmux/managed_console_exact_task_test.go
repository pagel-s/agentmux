package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"agentmux/internal/managed"
	tea "github.com/charmbracelet/bubbletea"
)

func TestConsoleExactTaskReadsOlderIdentityWithoutSearchingOrMutating(t *testing.T) {
	state, client, _ := inventoryBroker(t)
	ctx := context.Background()
	id, _, err := workSendIntent(ctx, client, state, "peer", "Exact older task", "instruction", "")
	if err != nil {
		t.Fatal(err)
	}
	for i := range 43 {
		if _, _, err = workSendIntent(ctx, client, state, "peer", fmt.Sprintf("Newer task %d references %s", i, id), "instruction", ""); err != nil {
			t.Fatal(err)
		}
	}
	before, err := workCall(ctx, client, "task.list", map[string]any{"order": "desc", "limit": 100})
	if err != nil {
		t.Fatal(err)
	}
	page := &consoleTaskPage{ExactID: id, Epoch: 1, ReadSequence: 1}
	d := fetchWorkSnapshot(state, client, "peer", page)
	if d.err != nil || len(d.activity.Tasks) != 1 || d.activity.Tasks[0].ID != id || d.activity.Tasks[0].Text != "Exact older task" || d.taskPage.More {
		t.Fatalf("exact lookup fell back to newer references: %+v", d)
	}
	after, err := workCall(ctx, client, "task.list", map[string]any{"order": "desc", "limit": 100})
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("opening changed retained task evidence: %v\n%s\n%s", err, before, after)
	}
	if _, err = workCall(ctx, client, "task.cancel", map[string]any{"id": id, "version": d.activity.Tasks[0].Version}); err != nil {
		t.Fatal(err)
	}
	refreshed := fetchWorkSnapshot(state, client, "peer", page)
	if refreshed.err != nil || len(refreshed.activity.Tasks) != 1 || refreshed.activity.Tasks[0].ID != id || refreshed.activity.Tasks[0].Version <= d.activity.Tasks[0].Version || refreshed.activity.Tasks[0].State != "cancelled" {
		t.Fatalf("refresh did not reread exact task's current version: %+v", refreshed)
	}
}

func TestConsoleExactTaskRejectsMissingMismatchedAndInvalidRecords(t *testing.T) {
	root, err := os.MkdirTemp("", "amx-exact-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	socket := filepath.Join(root, "broker.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	response := make(chan string, 1)
	server := &http.Server{Handler: http.HandlerFunc(func(out http.ResponseWriter, request *http.Request) {
		var r managed.Request
		if err := json.NewDecoder(request.Body).Decode(&r); err != nil {
			t.Error(err)
		}
		if r.Operation != "task.get" || string(r.Args) != `{"id":"task_exact"}` {
			t.Errorf("opening invoked an unrelated operation: %+v", r)
		}
		out.Header().Set("Content-Type", "application/json")
		_, _ = out.Write([]byte(<-response))
	})}
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { _ = server.Close() })
	client := managed.NewClient(managed.Credential{AgentID: "$owner", Socket: socket})
	for _, body := range []string{
		`{"result":{}}`,
		`{"result":{"task":{"id":"task_other","version":1,"to":"peer"}}}`,
		`{"result":{"task":{"id":"task_exact","version":0,"to":"peer"}}}`,
		`{"result":{"task":{"id":"task_exact","version":1,"to":"../peer"}}}`,
		`{"result":{"task":{"id":"task_exact","version":1,"to":"peer","state":"made-up","delivery":"pending"}}}`,
		`{"result":{"task":{"id":"task_exact","version":1,"to":"peer","state":"proposed","delivery":"made-up"}}}`,
		`{"error":{"code":"not_found","message":"missing exact task"}}`,
	} {
		response <- body
		if task, err := loadConsoleTask(context.Background(), client, "task_exact"); err == nil || task.ID != "" {
			t.Fatalf("invalid exact record was accepted: %+v %v", task, err)
		}
	}
	response <- `{"result":{"task":{"id":"task_exact","version":1,"to":"peer","state":"proposed","delivery":"pending"}}}`
	if task, err := loadConsoleTask(context.Background(), client, "task_exact"); err != nil || task.ID != "task_exact" {
		t.Fatalf("valid exact response was refused: %+v %v", task, err)
	}
}

func TestConsoleExactTaskEntryRejectsInvalidTargetsBeforeCredentialsOrUI(t *testing.T) {
	state := filepath.Join(t.TempDir(), "absent-workspace")
	for _, id := range []string{"", "peer", "../task_one", "task_\x1b[31m"} {
		var out, diagnostics bytes.Buffer
		if code := runManagedConsoleTask(state, id, strings.NewReader(""), &out, &diagnostics); code != exitValidation || out.Len() != 0 {
			t.Fatalf("invalid target reached credentials or generic UI: %q %d %q %q", id, code, out.String(), diagnostics.String())
		}
	}
	if _, err := os.Stat(state); !os.IsNotExist(err) {
		t.Fatalf("invalid target created state: %v", err)
	}
}

func exactTaskFixture() (console, managed.Task) {
	task := managed.Task{ID: "task_exact", To: "retired-recipient", Text: "Inspect exact instruction", State: "running", Delivery: "dispatching", Version: 7,
		Recovery: &managed.TaskRecovery{Delivery: "uncertain", Actions: []string{"fail", "retry"}, Reason: "Prior process outcome unconfirmed; inspect side effects."}}
	m := sessionSwitcherFixture()
	m.tab, m.contentFocus, m.taskExactID, m.taskEpoch = 4, true, task.ID, 2
	m.data.activity.Tasks = []managed.Task{task}
	m.data.taskPage = &consoleTaskPage{ExactID: task.ID, Epoch: 2}
	return m, task
}

func exactTaskSnapshot(m console, task managed.Task, sequence uint64) consoleData {
	d := m.data
	d.selected, d.agents = m.selectedID(), m.agents
	d.taskPage = &consoleTaskPage{ExactID: m.taskExactID, Epoch: m.taskEpoch, ReadSequence: sequence}
	d.activity.Tasks = []managed.Task{task}
	return d
}

func TestConsoleExactTaskRefreshRejectsLateReadsAndVersionRegression(t *testing.T) {
	m, task := exactTaskFixture()
	task.Version = 8
	current := exactTaskSnapshot(m, task, 2)
	next, _ := m.Update(current)
	m = next.(console)
	old := task
	old.Version = 7
	for _, data := range []consoleData{
		exactTaskSnapshot(m, old, 1),
		exactTaskSnapshot(m, old, 3),
		{selected: m.selectedID(), agents: m.agents, taskPage: &consoleTaskPage{ExactID: "task_other", Epoch: m.taskEpoch, ReadSequence: 4}, activity: managedActivitySnapshot{Tasks: []managed.Task{{ID: "task_other", Version: 99}}}},
	} {
		next, _ = m.Update(data)
		m = next.(console)
		if m.offline || len(m.data.activity.Tasks) != 1 || m.data.activity.Tasks[0].ID != task.ID || m.data.activity.Tasks[0].Version != 8 {
			t.Fatalf("late read replaced current exact task: %+v", m.data.activity.Tasks)
		}
	}
	failed := exactTaskSnapshot(m, task, 5)
	failed.err = errors.New("exact task missing")
	next, _ = m.Update(failed)
	m = next.(console)
	if !m.offline || len(m.data.activity.Tasks) != 0 || !strings.Contains(m.View(), "Exact task unavailable") {
		t.Fatal("missing task left a stale actionable row")
	}
	for _, stale := range []consoleData{current, {selected: m.selectedID(), agents: m.agents}} {
		next, _ = m.Update(stale)
		m = next.(console)
		if !m.offline || len(m.data.activity.Tasks) != 0 {
			t.Fatal("late or unscoped success resurrected a missing exact task")
		}
	}
	old.Version = 7
	next, _ = m.Update(exactTaskSnapshot(m, old, 6))
	m = next.(console)
	if !m.offline || len(m.data.activity.Tasks) != 0 || m.taskVersion != 8 {
		t.Fatal("lookup failure lost the version high-water mark across reconnect")
	}
	recovered := exactTaskSnapshot(m, task, 7)
	next, _ = m.Update(recovered)
	m = next.(console)
	if m.offline || len(m.data.activity.Tasks) != 1 || m.data.activity.Tasks[0].Version != 8 {
		t.Fatal("fresh exact task could not recover after lookup failure")
	}
}

func TestConsoleExactTaskRecoveryWorksWithoutActiveRecipientAndFreezesReview(t *testing.T) {
	m, task := exactTaskFixture()
	m.width, m.height = 160, 50
	for _, text := range []string{"Exact task: " + task.ID, "version 7", task.Text, "Recovery: uncertain", "Ctrl+K reviews recovery"} {
		if !strings.Contains(m.View(), text) {
			t.Fatalf("exact-task recovery route omitted %q: %s", text, m.View())
		}
	}
	m.openActions()
	m.text = []rune("Retry uncertain task")
	next, cmd := m.invokeMenuAction()
	m = next.(console)
	if cmd != nil || m.ownerReview == nil || m.ownerReview.args["id"] != task.ID || m.ownerReview.args["version"] != 7 {
		t.Fatalf("retired recipient blocked exact task review or opening mutated it: %+v", m.ownerReview)
	}
	review := m.ownerReview
	task.Version = 8
	next, _ = m.Update(exactTaskSnapshot(m, task, 1))
	m = next.(console)
	if m.ownerReview != review || m.ownerReview.args["version"] != 7 || m.data.activity.Tasks[0].Version != 8 {
		t.Fatal("refresh silently retargeted or rebound the frozen recovery review")
	}
	m.ownerReview = nil
	m.openReader()
	if m.reader == nil || !strings.Contains(m.reader.body, `"version": 8`) || !strings.Contains(m.reader.body, `"to": "retired-recipient"`) || !strings.Contains(m.reader.body, `"delivery": "dispatching"`) {
		t.Fatal("full exact task reader omitted current task evidence")
	}
}

func TestConsoleExactTaskScopeSurvivesRosterChangesAndExplicitFiltersExitIt(t *testing.T) {
	m, task := exactTaskFixture()
	m.explicitSessionChoice(1)
	if m.taskExactID != task.ID || len(m.data.activity.Tasks) != 1 {
		t.Fatal("session navigation discarded the independent exact task")
	}
	staleRoster := exactTaskSnapshot(m, task, 1)
	staleRoster.selected = "alice"
	task.Version = 9
	staleRoster.activity.Tasks = []managed.Task{task}
	next, _ := m.Update(staleRoster)
	m = next.(console)
	if m.taskExactID != task.ID || len(m.data.activity.Tasks) != 1 || m.data.activity.Tasks[0].Version != 9 {
		t.Fatal("a roster-selection mismatch discarded fresh exact-task evidence")
	}
	for _, key := range []string{"y", "z", "search", "session-tasks"} {
		fresh, _ := exactTaskFixture()
		before := fresh.taskEpoch
		var next tea.Model
		if key == "search" {
			fresh.compose("task-search", "Search", "", "query")
			next, _ = fresh.key(tea.KeyMsg{Type: tea.KeyEnter})
		} else if key == "session-tasks" {
			fresh.openActions()
			fresh.text = []rune("Tasks for selected session")
			next, _ = fresh.invokeMenuAction()
		} else {
			next, _ = fresh.key(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)})
		}
		fresh = next.(console)
		if fresh.taskExactID != "" || fresh.taskEpoch <= before || fresh.taskVersion != 0 || len(fresh.data.activity.Tasks) != 0 {
			t.Fatalf("explicit %s did not leave exact-task scope safely", key)
		}
	}
}
