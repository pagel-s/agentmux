package managed

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"agentmux/internal/managedconfig"
	"agentmux/internal/nativebridge"
)

func nativeStore(t *testing.T) *Store {
	cfg := testConfig()
	for _, id := range []string{"alice", "bob"} {
		a := cfg.Agents[id]
		a.Harness = "pi"
		a.Command = nil
		a.Interaction = "interactive"
		cfg.Agents[id] = a
	}
	return testStore(t, cfg)
}

func TestNativeLargeBacklogKeepsStatusAndClaimWithinWireLimit(t *testing.T) {
	s := nativeStore(t)
	nativeRegister(t, s, "bob", "native-bob")
	nativeMode(t, s, "bob", "auto", nil)
	text := strings.Repeat("x", s.cfg.Limits.MaxMessageBytes)
	for i := 0; i < 80; i++ {
		call(t, s, "alice", "instruct", map[string]any{"to": []string{"bob"}, "text": text})
	}
	raw := call(t, s, "", "native.status", map[string]any{"agent": "bob"})
	if len(raw) > maxWireBytes {
		t.Fatal("selected status exceeded transport limit")
	}
	var selected struct {
		Status NativeStatus `json:"status"`
	}
	if err := json.Unmarshal(raw, &selected); err != nil {
		t.Fatal(err)
	}
	if !selected.Status.PendingMore || len(selected.Status.Pending) < 1 || selected.Status.Pending[0].Text != text {
		t.Fatal("bounded status truncated reviewable text or concealed more items")
	}
	raw = call(t, s, "", "native.status", map[string]any{})
	if len(raw) > 65536 {
		t.Fatal("overview repeated pending message bodies")
	}
	d := nativeNext(t, s, "bob", "native-bob", "idle")
	if d == nil || d.Text != text {
		t.Fatal("claim lost full message body")
	}
}

func TestNativeOverviewPaginatesStableIdentities(t *testing.T) {
	s := nativeStore(t)
	var page struct {
		Statuses []NativeStatus `json:"statuses"`
		Cursor   string         `json:"cursor"`
		HasMore  bool           `json:"has_more"`
	}
	if err := json.Unmarshal(call(t, s, "", "native.status", map[string]any{"limit": 1}), &page); err != nil {
		t.Fatal(err)
	}
	if len(page.Statuses) != 1 || page.Statuses[0].Agent != "alice" || !page.HasMore {
		t.Fatalf("bad first page %+v", page)
	}
	if err := json.Unmarshal(call(t, s, "", "native.status", map[string]any{"limit": 1, "after": page.Cursor}), &page); err != nil {
		t.Fatal(err)
	}
	if len(page.Statuses) != 1 || page.Statuses[0].Agent != "bob" || page.HasMore {
		t.Fatalf("bad second page %+v", page)
	}
}

func TestNativeRecoveryMetadataCannotRestorePausedDefaultsOrRenewTurns(t *testing.T) {
	t.Run("policy", func(t *testing.T) {
		s := nativeStore(t)
		nativeRegister(t, s, "bob", "native-bob")
		nativeMode(t, s, "bob", "auto", nil)
		nativeMode(t, s, "bob", "manual", nil)
		if _, err := s.db.Exec("DELETE FROM records WHERE kind='native_policy'"); err != nil {
			t.Fatal(err)
		}
		expectFault(t, s, "bob", "native.peek", "internal", map[string]any{"session": "native-bob", "state": "idle"})
	})
	for _, kind := range []string{"native_usage", "native_delivery"} {
		t.Run(kind, func(t *testing.T) {
			s := nativeStore(t)
			nativeRegister(t, s, "bob", "native-bob")
			instruction(t, s)
			nativeMode(t, s, "bob", "auto", map[string]any{"max_turns": 1})
			d := nativeNext(t, s, "bob", "native-bob", "idle")
			nativeReport(t, s, "bob", "native-bob", d, "running")
			nativeReport(t, s, "bob", "native-bob", d, "completed")
			if _, err := s.db.Exec("DELETE FROM records WHERE kind=?", kind); err != nil {
				t.Fatal(err)
			}
			expectFault(t, s, "bob", "native.peek", "internal", map[string]any{"session": "native-bob", "state": "idle"})
		})
	}
}

func TestNativeUnrelatedPairChangesDoNotWakeIndependentSessions(t *testing.T) {
	s := nativeStore(t)
	nativeRegister(t, s, "bob", "native-bob")
	nativeMode(t, s, "bob", "auto", nil)
	d := nativeNext(t, s, "bob", "native-bob", "idle")
	if d == nil || d.Kind != "boundaries" {
		t.Fatal("expected initial sync")
	}
	nativeReport(t, s, "bob", "native-bob", d, "running")
	nativeReport(t, s, "bob", "native-bob", d, "completed")
	var c ControlState
	_ = json.Unmarshal(call(t, s, "", "control.get", map[string]any{}), &c)
	call(t, s, "", "control.update", map[string]any{"version": c.Version, "kind": "edge", "from": "eve", "to": "alice", "action": "message.send", "allow": true})
	if next := nativeNext(t, s, "bob", "native-bob", "idle"); next != nil {
		t.Fatalf("unrelated pair woke Bob: %+v", next)
	}
	_ = json.Unmarshal(call(t, s, "", "control.get", map[string]any{}), &c)
	call(t, s, "", "control.update", map[string]any{"version": c.Version, "kind": "edge", "from": "bob", "to": "alice", "action": "message.send", "allow": true})
	if next := nativeNext(t, s, "bob", "native-bob", "idle"); next == nil || next.Kind != "boundaries" {
		t.Fatal("changed recipient boundaries were not delivered")
	}
}

func TestNativeIncomingCommandChangesNotifyTheirRecipient(t *testing.T) {
	s := nativeStore(t)
	nativeRegister(t, s, "bob", "native-bob")
	nativeMode(t, s, "bob", "auto", nil)
	d := nativeNext(t, s, "bob", "native-bob", "idle")
	nativeReport(t, s, "bob", "native-bob", d, "running")
	nativeReport(t, s, "bob", "native-bob", d, "completed")
	var c ControlState
	_ = json.Unmarshal(call(t, s, "", "control.get", map[string]any{}), &c)
	call(t, s, "", "control.update", map[string]any{"version": c.Version, "kind": "edge", "from": "alice", "to": "bob", "action": "instruct", "allow": false})
	next := nativeNext(t, s, "bob", "native-bob", "idle")
	if next == nil || next.Kind != "boundaries" {
		t.Fatal("receiver was not notified of a changed incoming grant")
	}
	var b Boundaries
	_ = json.Unmarshal(call(t, s, "bob", "boundaries.get", map[string]any{}), &b)
	for _, source := range b.Incoming {
		if source.From == "alice" && contains(source.Actions, "instruct") {
			t.Fatal("revoked incoming command remained in boundary notice")
		}
	}
}

func TestNativeUnrelatedPairEditsPreserveExactBoundaryApproval(t *testing.T) {
	s := nativeStore(t)
	nativeRegister(t, s, "bob", "native-bob")
	nativeMode(t, s, "bob", "review", nil)
	st := nativeGetStatus(t, s, "bob")
	candidate := st.Pending[0]
	call(t, s, "", "native.approve", map[string]any{"agent": "bob", "version": st.Policy.Version, "candidate": candidate.ID})
	var c ControlState
	_ = json.Unmarshal(call(t, s, "", "control.get", map[string]any{}), &c)
	call(t, s, "", "control.update", map[string]any{"version": c.Version, "kind": "edge", "from": "eve", "to": "alice", "action": "message.send", "allow": true})
	next := nativeNext(t, s, "bob", "native-bob", "idle")
	if next == nil || next.Kind != "boundaries" || next.Text != candidate.Text {
		t.Fatalf("unrelated pair changed a reviewed boundary notice: %+v", next)
	}
}

func TestNativeNewConversationReceivesCurrentRoleAndBoundarySync(t *testing.T) {
	s := nativeStore(t)
	nativeRegister(t, s, "bob", "native-bob")
	nativeMode(t, s, "bob", "auto", nil)
	first := nativeNext(t, s, "bob", "native-bob", "idle")
	nativeReport(t, s, "bob", "native-bob", first, "running")
	nativeReport(t, s, "bob", "native-bob", first, "completed")
	nativeRegister(t, s, "bob", "another-native-conversation")
	next := nativeNext(t, s, "bob", "another-native-conversation", "idle")
	if next == nil || next.Kind != "boundaries" || next.ID == first.ID {
		t.Fatal("new native conversation missed its current boundary context")
	}
}

func TestNativeObservationRetryKeepsSourceAuthorityAndKind(t *testing.T) {
	s := nativeStore(t)
	nativeRegister(t, s, "bob", "native-bob")
	nativeMode(t, s, "bob", "auto", map[string]any{"notes": true})
	call(t, s, "alice", "message.send", map[string]any{"to": []string{"bob"}, "text": "Information, not an instruction"})
	d := nativeNext(t, s, "bob", "native-bob", "idle")
	if d == nil || d.Kind != "note" {
		t.Fatal("expected note")
	}
	nativeReport(t, s, "bob", "native-bob", d, "uncertain")
	task := taskIn(t, call(t, s, "", "task.get", map[string]any{"id": d.TaskID}))
	call(t, s, "", "task.resolve", map[string]any{"id": task.ID, "version": task.Version, "resolution": "retry"})
	next := nativeNext(t, s, "bob", "native-bob", "idle")
	if next == nil || next.Kind != "note" || next.TaskID != d.TaskID || next.ID == d.ID {
		t.Fatalf("observation retry changed authority: %+v", next)
	}
	if nativeGetStatus(t, s, "bob").Cancel {
		t.Fatal("retry lost its original source authorization")
	}
	got, err := loadTask(context.Background(), s.db, d.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Origin.Kind != "observation" {
		t.Fatal("retry promoted a note to an instruction")
	}
}

func TestNativeObservationBodiesCannotBeRecoveredFromRevokedClaimReceipts(t *testing.T) {
	s := nativeStore(t)
	nativeRegister(t, s, "bob", "native-bob")
	nativeMode(t, s, "bob", "auto", map[string]any{"memory_updates": true})
	call(t, s, "alice", "memory.put", map[string]any{"namespace": "team", "key": "sensitive", "value": "SCOPED_SOURCE_SENTINEL", "version": 0})
	var peek struct {
		Candidate string `json:"candidate"`
	}
	_ = json.Unmarshal(call(t, s, "bob", "native.peek", map[string]any{"session": "native-bob", "state": "idle"}), &peek)
	args := map[string]any{"session": "native-bob", "candidate": peek.Candidate}
	key := randomID("request")
	raw, problem := invoke(s, credential(t, s, "bob"), "native.claim", args, key)
	if problem != nil {
		t.Fatal(problem)
	}
	var out struct {
		Delivery *nativebridge.Delivery `json:"delivery"`
	}
	_ = json.Unmarshal(raw, &out)
	if out.Delivery == nil || !strings.Contains(out.Delivery.Text, "SCOPED_SOURCE_SENTINEL") {
		t.Fatal("authorized claim did not deliver source")
	}
	var c ControlState
	_ = json.Unmarshal(call(t, s, "", "control.get", map[string]any{}), &c)
	call(t, s, "", "control.update", map[string]any{"version": c.Version, "kind": "memory", "agent": "bob", "namespace": "team", "read": false, "write": false})
	replay, problem := invoke(s, credential(t, s, "bob"), "native.claim", args, key)
	if problem != nil {
		t.Fatal(problem)
	}
	if strings.Contains(string(replay), "SCOPED_SOURCE_SENTINEL") || !strings.Contains(string(replay), `"delivery":null`) {
		t.Fatal("claim receipt replayed revoked native input")
	}
	task := call(t, s, "bob", "task.get", map[string]any{"id": out.Delivery.TaskID})
	if strings.Contains(string(task), "SCOPED_SOURCE_SENTINEL") {
		t.Fatal("observation task exposed a source body after revocation")
	}
	if !nativeGetStatus(t, s, "bob").Cancel {
		t.Fatal("revoked observation was not cancelled")
	}
}

func TestNativeReceiptsAreRetryableWithoutReexecution(t *testing.T) {
	s := nativeStore(t)
	nativeRegister(t, s, "bob", "native-bob")
	instruction(t, s)
	nativeMode(t, s, "bob", "auto", nil)
	d := nativeNext(t, s, "bob", "native-bob", "idle")
	for _, phase := range []string{"submitting", "submitted", "running", "completed"} {
		nativeReport(t, s, "bob", "native-bob", d, phase)
		nativeReport(t, s, "bob", "native-bob", d, phase)
	}
	st := nativeGetStatus(t, s, "bob")
	if st.Turns != 1 {
		t.Fatalf("retry created a new turn: %+v", st)
	}
}

func TestNativeExplicitRetryRequiresFreshApprovalAndAttempt(t *testing.T) {
	s := nativeStore(t)
	nativeRegister(t, s, "bob", "native-bob")
	task := instruction(t, s)
	nativeMode(t, s, "bob", "review", nil)
	st := nativeGetStatus(t, s, "bob")
	call(t, s, "", "native.approve", map[string]any{"agent": "bob", "version": st.Policy.Version, "candidate": task.ID})
	d := nativeNext(t, s, "bob", "native-bob", "idle")
	nativeReport(t, s, "bob", "native-bob", d, "uncertain")
	task = taskIn(t, call(t, s, "", "task.get", map[string]any{"id": task.ID}))
	call(t, s, "", "task.resolve", map[string]any{"id": task.ID, "version": task.Version, "resolution": "retry"})
	if next := nativeNext(t, s, "bob", "native-bob", "idle"); next != nil {
		t.Fatal("old approval authorized a new attempt")
	}
	st = nativeGetStatus(t, s, "bob")
	candidate := ""
	for _, pending := range st.Pending {
		if pending.TaskID == task.ID {
			candidate = pending.ID
		}
	}
	if candidate == "" || candidate == task.ID {
		t.Fatal("explicit retry has no fresh candidate identity")
	}
	call(t, s, "", "native.approve", map[string]any{"agent": "bob", "version": st.Policy.Version, "candidate": candidate})
	next := nativeNext(t, s, "bob", "native-bob", "idle")
	if next == nil || next.TaskID != task.ID || next.ID == d.ID {
		t.Fatalf("retry did not create a new attempt: %+v", next)
	}
}

func TestClaudeAcknowledgmentRechecksPermissionAndTransportCannotRegressIt(t *testing.T) {
	cfg := testConfig()
	a := cfg.Agents["bob"]
	a.Interaction = "interactive"
	a.Harness = "claude"
	a.Command = nil
	cfg.Agents["bob"] = a
	s := testStore(t, cfg)
	call(t, s, "bob", "native.register", nativebridge.Registration{Adapter: "claude", Session: "native-claude", Capabilities: nativebridge.Capabilities{Instructions: true, Observations: true, Completion: true, CompletionEvidence: "receiver-ack", NativeQueue: true}})
	instruction(t, s)
	nativeMode(t, s, "bob", "auto", nil)
	d := nativeNext(t, s, "bob", "native-claude", "ready")
	nativeMode(t, s, "bob", "manual", nil)
	expectFault(t, s, "bob", "native.event", "forbidden", map[string]any{"session": "native-claude", "id": d.ID, "phase": "running"})
	nativeMode(t, s, "bob", "auto", nil)
	nativeReport(t, s, "bob", "native-claude", d, "running")
	call(t, s, "bob", "native.event", map[string]any{"session": "native-claude", "id": d.ID, "phase": "submitted", "native_ref": "late-inbox-write"})
	if st := nativeGetStatus(t, s, "bob"); st.Active.Phase != "running" {
		t.Fatal("late transport receipt regressed a receiver acknowledgment")
	}
	nativeReport(t, s, "bob", "native-claude", d, "completed")
	call(t, s, "bob", "native.event", map[string]any{"session": "native-claude", "id": d.ID, "phase": "submitted", "native_ref": "late-inbox-write"})
	task := taskIn(t, call(t, s, "", "task.get", map[string]any{"id": d.TaskID}))
	if task.Native.Phase != "completed" || task.Native.Evidence != "receiver-ack" {
		t.Fatalf("completion evidence changed: %+v", task.Native)
	}
}

func TestNativeClosedAgentDoesNotBreakWorkspaceStatus(t *testing.T) {
	s := nativeStore(t)
	nativeRegister(t, s, "bob", "native-bob")
	call(t, s, "", "agent.stop", map[string]any{"id": "bob"})
	var out struct {
		Statuses []NativeStatus `json:"statuses"`
	}
	if err := json.Unmarshal(call(t, s, "", "native.status", map[string]any{}), &out); err != nil {
		t.Fatal(err)
	}
	for _, st := range out.Statuses {
		if st.Agent == "bob" && st.State != "stopped" {
			t.Fatalf("closed agent not stopped: %+v", st)
		}
	}
}

func TestNativeExpiryRequestsOnlyPeerCancellation(t *testing.T) {
	s := nativeStore(t)
	nativeRegister(t, s, "bob", "native-bob")
	instruction(t, s)
	nativeMode(t, s, "bob", "auto", nil)
	d := nativeNext(t, s, "bob", "native-bob", "idle")
	task, err := loadTask(context.Background(), s.db, d.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	task.Expires = time.Now().Add(-time.Second).Unix()
	if err = saveTask(context.Background(), s.db, task); err != nil {
		t.Fatal(err)
	}
	st := nativeGetStatus(t, s, "bob")
	if !st.Cancel || st.State != "uncertain" {
		t.Fatalf("expiry did not request peer cancellation: %+v", st)
	}
	nativeReport(t, s, "bob", "native-bob", d, "cancelled")
}

func TestNativeResultHopBudgetCannotResetAcrossObservations(t *testing.T) {
	s := nativeStore(t)
	nativeRegister(t, s, "bob", "native-bob")
	nativeRegister(t, s, "alice", "native-alice")
	nativeMode(t, s, "bob", "auto", map[string]any{"max_hops": 1})
	nativeMode(t, s, "alice", "auto", map[string]any{"max_hops": 4})
	initial := instruction(t, s)
	d := nativeNext(t, s, "bob", "native-bob", "idle")
	nativeReport(t, s, "bob", "native-bob", d, "running")
	nativeReport(t, s, "bob", "native-bob", d, "completed")
	back := nativeNext(t, s, "alice", "native-alice", "idle")
	if back == nil || back.Kind != "result" {
		t.Fatal("expected result")
	}
	nativeReport(t, s, "alice", "native-alice", back, "running")
	next := instruction(t, s)
	if next.Origin == nil || next.Origin.Hop != 2 || next.Origin.ParentTaskID != back.TaskID {
		t.Fatalf("result reset causal chain: initial=%+v next=%+v", initial.Origin, next.Origin)
	}
	st := nativeGetStatus(t, s, "bob")
	for _, pending := range st.Pending {
		if pending.TaskID == next.ID {
			t.Fatal("instruction beyond hop budget remained eligible")
		}
	}
	call(t, s, "alice", "message.send", map[string]any{"to": []string{"bob"}, "text": "Same causal chain as a note"})
	nativeMode(t, s, "bob", "auto", map[string]any{"notes": true})
	st = nativeGetStatus(t, s, "bob")
	for _, pending := range st.Pending {
		if pending.Kind == "note" {
			t.Fatal("note bypassed causal hop budget")
		}
	}
}
func nativeRegister(t *testing.T, s *Store, id, session string) {
	t.Helper()
	call(t, s, id, "native.register", nativebridge.Registration{Adapter: "pi", Session: session, Capabilities: nativebridge.Capabilities{Instructions: true, Observations: true, Completion: true, PreservesDraft: true}})
}
func nativeGetStatus(t *testing.T, s *Store, id string) NativeStatus {
	t.Helper()
	var out struct {
		Status NativeStatus `json:"status"`
	}
	if err := json.Unmarshal(call(t, s, "", "native.status", map[string]any{"agent": id}), &out); err != nil {
		t.Fatal(err)
	}
	return out.Status
}
func nativeMode(t *testing.T, s *Store, id, mode string, extra map[string]any) {
	t.Helper()
	in := map[string]any{"agent": id, "mode": mode, "version": nativeGetStatus(t, s, id).Policy.Version}
	for k, v := range extra {
		in[k] = v
	}
	call(t, s, "", "native.configure", in)
}
func nativeNext(t *testing.T, s *Store, id, session, state string) *nativebridge.Delivery {
	t.Helper()
	var peek struct {
		Candidate string `json:"candidate"`
	}
	if err := json.Unmarshal(call(t, s, id, "native.peek", map[string]any{"session": session, "state": state}), &peek); err != nil {
		t.Fatal(err)
	}
	if peek.Candidate == "" {
		return nil
	}
	var out struct {
		Delivery *nativebridge.Delivery `json:"delivery"`
	}
	if err := json.Unmarshal(call(t, s, id, "native.claim", map[string]any{"session": session, "candidate": peek.Candidate}), &out); err != nil {
		t.Fatal(err)
	}
	return out.Delivery
}
func nativeReport(t *testing.T, s *Store, id, session string, d *nativebridge.Delivery, phase string) {
	t.Helper()
	call(t, s, id, "native.event", map[string]any{"session": session, "id": d.ID, "phase": phase, "result": "An attributed result"})
}
func TestNativeManualReviewAutoAndLifecycle(t *testing.T) {
	s := nativeStore(t)
	nativeRegister(t, s, "bob", "native-bob")
	task := instruction(t, s)
	if d := nativeNext(t, s, "bob", "native-bob", "idle"); d != nil {
		t.Fatal("manual mode woke model")
	}
	nativeMode(t, s, "bob", "review", nil)
	if d := nativeNext(t, s, "bob", "native-bob", "idle"); d != nil {
		t.Fatal("unapproved task delivered")
	}
	st := nativeGetStatus(t, s, "bob")
	call(t, s, "", "native.approve", map[string]any{"agent": "bob", "version": st.Policy.Version, "candidate": task.ID})
	for _, state := range []string{"busy", "draft", "approval", "offline"} {
		if d := nativeNext(t, s, "bob", "native-bob", state); d != nil {
			t.Fatalf("delivered while %s", state)
		}
	}
	d := nativeNext(t, s, "bob", "native-bob", "idle")
	if d == nil || d.TaskID != task.ID || d.From != "alice" {
		t.Fatalf("wrong delivery: %+v", d)
	}
	expectFault(t, s, "bob", "native.event", "conflict", map[string]any{"session": "native-bob", "id": d.ID, "phase": "completed"})
	nativeReport(t, s, "bob", "native-bob", d, "submitted")
	if nativeGetStatus(t, s, "bob").Cancel {
		t.Fatal("an authorized instruction was spuriously cancelled")
	}
	if got := nativeGetStatus(t, s, "bob"); got.Active.Phase != "submitted" {
		t.Fatalf("transport receipt lied: %+v", got)
	}
	nativeReport(t, s, "bob", "native-bob", d, "running")
	nativeReport(t, s, "bob", "native-bob", d, "completed")
	nativeReport(t, s, "bob", "native-bob", d, "completed") // Idempotent evidence, no replay.
	got := taskIn(t, call(t, s, "alice", "task.get", map[string]any{"id": task.ID}))
	if got.State != "submitted" || got.Result == "" || got.Native.Phase != "completed" {
		t.Fatalf("bad completion: %+v", got)
	}
	if d := nativeNext(t, s, "bob", "native-bob", "idle"); d != nil {
		t.Fatal("task replayed")
	}
}
func TestNativeClaimRechecksPermissionAndApprovalRevision(t *testing.T) {
	s := nativeStore(t)
	nativeRegister(t, s, "bob", "native-bob")
	task := instruction(t, s)
	nativeMode(t, s, "bob", "auto", nil)
	call(t, s, "bob", "native.peek", map[string]any{"session": "native-bob", "state": "idle"})
	controlRaw := call(t, s, "", "control.get", map[string]any{})
	var controls ControlState
	_ = json.Unmarshal(controlRaw, &controls)
	call(t, s, "", "control.update", map[string]any{"version": controls.Version, "kind": "edge", "from": "alice", "to": "bob", "action": "instruct", "allow": false})
	if d := nativeNext(t, s, "bob", "native-bob", "idle"); d != nil && d.Kind == "instruction" {
		t.Fatal("revoked instruction delivered")
	}
	_, f := invoke(s, credential(t, s, "bob"), "native.claim", map[string]any{"session": "native-bob", "candidate": task.ID}, randomID("request"))
	if f != nil && f.Code != "conflict" {
		t.Fatalf("unexpected stale candidate fault: %v", f)
	}
}
func TestNativeCancellationUncertaintyAndOwnerResolution(t *testing.T) {
	s := nativeStore(t)
	nativeRegister(t, s, "bob", "native-bob")
	instruction(t, s)
	nativeMode(t, s, "bob", "auto", nil)
	d := nativeNext(t, s, "bob", "native-bob", "idle")
	nativeReport(t, s, "bob", "native-bob", d, "submitted")
	nativeMode(t, s, "bob", "manual", nil)
	if !nativeGetStatus(t, s, "bob").Cancel {
		t.Fatal("pause did not request native cancellation")
	}
	nativeReport(t, s, "bob", "native-bob", d, "uncertain")
	nativeMode(t, s, "bob", "auto", nil)
	if next := nativeNext(t, s, "bob", "native-bob", "idle"); next != nil {
		t.Fatal("uncertain turn automatically replayed")
	}
	task := taskIn(t, call(t, s, "", "task.get", map[string]any{"id": d.TaskID}))
	call(t, s, "", "task.resolve", map[string]any{"id": task.ID, "version": task.Version, "resolution": "fail"})
	if got := nativeGetStatus(t, s, "bob"); got.Active != nil {
		t.Fatalf("resolved task blocked forever: %+v", got)
	}
	expectFault(t, s, "bob", "native.event", "conflict", map[string]any{"session": "native-bob", "id": d.ID, "phase": "running"})
}
func TestNativeReceiverBindingAndSessionSwitch(t *testing.T) {
	s := nativeStore(t)
	nativeRegister(t, s, "bob", "native-bob")
	instruction(t, s)
	nativeMode(t, s, "bob", "auto", nil)
	d := nativeNext(t, s, "bob", "native-bob", "idle")
	nativeRegister(t, s, "alice", "native-alice")
	expectFault(t, s, "alice", "native.event", "conflict", map[string]any{"session": "native-alice", "id": d.ID, "phase": "completed"})
	expectFault(t, s, "", "native.register", "forbidden", map[string]any{"agent": "bob", "adapter": "pi", "session": "spoof"})
	nativeRegister(t, s, "bob", "new-conversation")
	expectFault(t, s, "bob", "native.peek", "conflict", map[string]any{"session": "native-bob", "state": "idle"})
	if st := nativeGetStatus(t, s, "bob"); st.State != "uncertain" {
		t.Fatalf("conversation switch lost evidence: %+v", st)
	}
}
func TestNativeResultsReturnWithoutReverseCommandGrant(t *testing.T) {
	s := nativeStore(t)
	nativeRegister(t, s, "bob", "native-bob")
	nativeRegister(t, s, "alice", "native-alice")
	task := instruction(t, s)
	nativeMode(t, s, "bob", "auto", nil)
	nativeMode(t, s, "alice", "auto", nil)
	d := nativeNext(t, s, "bob", "native-bob", "idle")
	nativeReport(t, s, "bob", "native-bob", d, "running")
	nativeReport(t, s, "bob", "native-bob", d, "completed")
	back := nativeNext(t, s, "alice", "native-alice", "idle")
	if back == nil || back.Kind != "result" || back.From != "bob" {
		t.Fatalf("missing result: %+v", back)
	}
	if back.TaskID == task.ID {
		t.Fatal("result reused original task lease")
	}
	if nativeGetStatus(t, s, "alice").Cancel {
		t.Fatal("correlated result was spuriously cancelled")
	}
	nativeReport(t, s, "alice", "native-alice", back, "running")
	nativeReport(t, s, "alice", "native-alice", back, "completed")
	st := nativeGetStatus(t, s, "bob")
	for _, pending := range st.Pending {
		if pending.Kind == "result" {
			t.Fatal("result caused result loop")
		}
	}
}
func TestNativeSubscriptionsAndTurnLimits(t *testing.T) {
	s := nativeStore(t)
	nativeRegister(t, s, "bob", "native-bob")
	nativeMode(t, s, "bob", "auto", map[string]any{"notes": true, "memory_updates": true, "max_turns": 1})
	call(t, s, "alice", "message.send", map[string]any{"to": []string{"bob"}, "text": "A note, not a command"})
	call(t, s, "alice", "memory.put", map[string]any{"namespace": "team", "key": "facts", "value": "Visible shared knowledge", "version": 0})
	d := nativeNext(t, s, "bob", "native-bob", "idle")
	if d == nil || d.Kind != "note" {
		t.Fatalf("missing note: %+v", d)
	}
	if nativeGetStatus(t, s, "bob").Cancel {
		t.Fatal("subscribed note was spuriously cancelled")
	}
	nativeReport(t, s, "bob", "native-bob", d, "running")
	nativeReport(t, s, "bob", "native-bob", d, "completed")
	if got := nativeGetStatus(t, s, "bob"); got.State != "limited" {
		t.Fatalf("turn limit not applied: %+v", got)
	}
	nativeMode(t, s, "bob", "auto", map[string]any{"max_turns": 10})
	d = nativeNext(t, s, "bob", "native-bob", "idle")
	if d == nil || d.Kind != "memory" {
		t.Fatalf("missing memory: %+v", d)
	}
}
func TestNativePollDoesNotConsumeWriteReceipts(t *testing.T) {
	s := nativeStore(t)
	nativeRegister(t, s, "bob", "native-bob")
	var before, after int
	s.db.QueryRow("SELECT count(*) FROM requests").Scan(&before)
	for i := 0; i < 15; i++ {
		call(t, s, "bob", "native.peek", map[string]any{"session": "native-bob", "state": "idle"})
	}
	s.db.QueryRow("SELECT count(*) FROM requests").Scan(&after)
	if before != after {
		t.Fatal("idle polling consumed request budget")
	}
	// Readiness does not survive a broker restart or imply a model is active.
	s.nativePresence = nil
	st, err := s.nativeStatus(context.Background(), s.db, "bob")
	if err != nil || st.Connected {
		t.Fatalf("stale readiness %+v %v", st, err)
	}
}

func TestNativeLifecycleRetainsCapacityAfterSubmission(t *testing.T) {
	s := nativeStore(t)
	nativeRegister(t, s, "bob", "native-bob")
	instruction(t, s)
	nativeMode(t, s, "bob", "auto", nil)
	d := nativeNext(t, s, "bob", "native-bob", "idle")
	nativeReport(t, s, "bob", "native-bob", d, "submitting")
	reserve, err := lifecycleReserve(context.Background(), s.db)
	if err != nil {
		t.Fatal(err)
	}
	var records, requests int
	if err = s.db.QueryRow("SELECT count(*) FROM records").Scan(&records); err != nil {
		t.Fatal(err)
	}
	if err = s.db.QueryRow("SELECT count(*) FROM requests").Scan(&requests); err != nil {
		t.Fatal(err)
	}
	s.cfg.Limits.MaxRecords = records + reserve
	s.cfg.Limits.MaxRequests = requests + reserve
	expectFault(t, s, "alice", "board.post", "limit", map[string]any{"board": "public", "text": "ordinary write cannot consume peer receipt reserves"})
	nativeReport(t, s, "bob", "native-bob", d, "submitted")
	nativeReport(t, s, "bob", "native-bob", d, "running")
	nativeReport(t, s, "bob", "native-bob", d, "completed")
	if got := nativeGetStatus(t, s, "bob"); got.Active != nil {
		t.Fatalf("completed native turn remained active at capacity: %+v", got.Active)
	}
}

func TestNativeManualPauseRetainsStorageCapacity(t *testing.T) {
	for _, mode := range []string{"auto", "review"} {
		for _, origin := range []string{"launch", "persisted", "added", "spawned"} {
			t.Run(mode+"/"+origin, func(t *testing.T) {
				cfg := testConfig()
				agent := managedconfig.Agent{Harness: "pi", Interaction: "interactive", Sandbox: "local", Workdir: t.TempDir(), NativeDelivery: &managedconfig.NativeDelivery{Mode: mode}}
				id := "bob"
				if origin == "launch" || origin == "persisted" {
					cfg.Agents[id] = agent
					if origin == "persisted" {
						agent.NativeDelivery = nil
						cfg.Agents[id] = agent
					}
				}
				if origin == "spawned" {
					cfg = dynamicConfig()
					agent.Sandbox = "bubblewrap"
					agent.Network = "none"
					agent.Workdir = "" // Spawn allocates a private child workspace.
					cfg.Spawn.Templates["echo"] = agent
				}
				s := testStore(t, cfg)
				if origin == "added" {
					id = "dynamic-native"
					call(t, s, "", "agent.add", map[string]any{"id": id, "config": agent})
				}
				if origin == "spawned" {
					id = spawned(t, s, "alice", Capabilities{}).ID
				}
				if origin == "persisted" {
					nativeMode(t, s, id, mode, nil)
				}
				nativeRegister(t, s, id, "native-"+id)
				// A currently submitted native peer turn must remain pausable without
				// consuming capacity held for its remaining transport lifecycle.
				task := taskIn(t, call(t, s, "", "instruct", map[string]any{"to": []string{id}, "text": "A bounded native peer instruction"}))
				if mode == "review" {
					st := nativeGetStatus(t, s, id)
					call(t, s, "", "native.approve", map[string]any{"agent": id, "version": st.Policy.Version, "candidate": task.ID})
				}
				d := nativeNext(t, s, id, "native-"+id, "idle")
				if d == nil {
					t.Fatal("fixture native instruction did not claim")
				}
				nativeReport(t, s, id, "native-"+id, d, "submitted")
				reserve, err := lifecycleReserve(context.Background(), s.db)
				if err != nil {
					t.Fatal(err)
				}
				var records, requests int
				if err = s.db.QueryRow("SELECT count(*) FROM records").Scan(&records); err != nil {
					t.Fatal(err)
				}
				if err = s.db.QueryRow("SELECT count(*) FROM requests").Scan(&requests); err != nil {
					t.Fatal(err)
				}
				s.cfg.Limits.MaxRecords = records + reserve
				s.cfg.Limits.MaxRequests = requests + reserve
				turns := nativeGetStatus(t, s, id).Turns
				nativeMode(t, s, id, "manual", nil)
				after := nativeGetStatus(t, s, id)
				if after.Mode != "manual" || !after.Cancel || after.Turns != turns {
					t.Fatalf("pause lost safety cancellation or renewed usage: %+v", after)
				}
				// Turning delivery back on requires fresh reserve capacity. A pause is
				// never a bypass that renews storage or native turn budgets.
				expectFault(t, s, "", "native.configure", "limit", map[string]any{"agent": id, "version": after.Policy.Version, "mode": mode})
				nativeReport(t, s, id, "native-"+id, d, "cancelled")
				if got := nativeGetStatus(t, s, id); got.Mode != "manual" || got.Turns != turns {
					t.Fatal("terminal receipt changed pause or native usage")
				}
			})
		}
	}
}
