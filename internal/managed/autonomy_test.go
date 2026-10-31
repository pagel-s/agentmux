package managed

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"agentmux/internal/managedconfig"
)

func autonomyConfig() managedconfig.Config {
	c := testConfig()
	a := c.Agents["bob"]
	a.Autonomy = managedconfig.Autonomy{Enabled: true, Boards: []string{"team"}, Pings: true, MaxTurns: 10, MaxBacklog: 10, BatchSize: 2, MaxHops: 3}
	c.Agents["bob"] = a
	return c
}
func autonomyStatusIn(t *testing.T, s *Store, id string) AutonomyStatus {
	t.Helper()
	var out struct {
		Status AutonomyStatus `json:"status"`
	}
	if err := json.Unmarshal(call(t, s, id, "autonomy.status", map[string]any{}), &out); err != nil {
		t.Fatal(err)
	}
	return out.Status
}
func autonomyNext(t *testing.T, s *Store, id string) (Task, string) {
	t.Helper()
	var out struct {
		Task  Task   `json:"task"`
		Lease string `json:"lease"`
	}
	if err := json.Unmarshal(call(t, s, id, "worker.next", map[string]any{}), &out); err != nil {
		t.Fatal(err)
	}
	return out.Task, out.Lease
}
func autonomyFinish(t *testing.T, s *Store, id string, task Task, lease string) {
	t.Helper()
	call(t, s, id, "task.complete", map[string]any{"id": task.ID, "version": task.Version, "lease": lease, "status": "submitted", "result": "bounded result"})
}

func TestAutonomyDefaultOffAndIdleDiscoveryDoNotWrite(t *testing.T) {
	s := testStore(t, testConfig())
	call(t, s, "alice", "ping.send", map[string]any{"to": []string{"bob"}, "text": "note, not command"})
	var records, receipts int
	s.db.QueryRow("SELECT count(*) FROM records").Scan(&records)
	s.db.QueryRow("SELECT count(*) FROM requests").Scan(&receipts)
	for i := 0; i < 50; i++ {
		status := autonomyStatusIn(t, s, "bob")
		if status.Ready || status.Pending != 0 {
			t.Fatal("default subscription activated")
		}
	}
	var afterRecords, afterReceipts int
	s.db.QueryRow("SELECT count(*) FROM records").Scan(&afterRecords)
	s.db.QueryRow("SELECT count(*) FROM requests").Scan(&afterReceipts)
	if records != afterRecords || receipts != afterReceipts {
		t.Fatal("idle reads grew durable storage")
	}
	expectFault(t, s, "alice", "autonomy.status", "forbidden", map[string]any{"agent": "bob"})
}

func TestObservationWakeDeduplicatesWithoutCommandAuthority(t *testing.T) {
	s := testStore(t, autonomyConfig())
	call(t, s, "alice", "board.post", map[string]any{"board": "team", "text": "restricted observation payload"})
	call(t, s, "bob", "board.post", map[string]any{"board": "team", "text": "own post must not wake self"})
	call(t, s, "alice", "message.send", map[string]any{"to": []string{"bob"}, "text": "direct notes disabled"})
	if status := autonomyStatusIn(t, s, "bob"); !status.Ready || status.Pending != 1 {
		t.Fatalf("bad status %+v", status)
	}
	cred := credential(t, s, "bob")
	first, problem := invoke(s, cred, "worker.next", map[string]any{}, "stable-wake")
	if problem != nil {
		t.Fatal(problem)
	}
	task := taskIn(t, first)
	var out struct {
		Lease string `json:"lease"`
	}
	json.Unmarshal(first, &out)
	if task.Origin == nil || task.Origin.Kind != "observation" || task.From != "bob" || task.To != "bob" || strings.Contains(string(first), "restricted observation payload") {
		t.Fatal("observation was cached as instruction/body")
	}
	read := call(t, s, "bob", "wake.read", map[string]any{"id": task.ID})
	if !strings.Contains(string(read), "restricted observation payload") {
		t.Fatal("authorized source missing")
	}
	expectFault(t, s, "bob", "instruct", "forbidden", map[string]any{"to": []string{"alice"}, "text": "observation gave me command authority"})
	autonomyFinish(t, s, "bob", task, out.Lease)
	again, problem := invoke(s, cred, "worker.next", map[string]any{}, "stable-wake")
	if problem != nil || !bytes.Equal(first, again) {
		t.Fatal("wake replay changed identity or executed twice", problem)
	}
	var count int
	s.db.QueryRow("SELECT count(*) FROM records WHERE kind='task'").Scan(&count)
	if count != 1 {
		t.Fatal("duplicate wake task")
	}
	status := autonomyStatusIn(t, s, "bob")
	if status.Ready || status.TurnsUsed != 1 || len(status.Receipts) != 1 || status.Receipts[0].State != "submitted" {
		t.Fatalf("bad receipt %+v", status)
	}
}

func TestInstructionsConsumeAutonomyBudgetAndTakePriority(t *testing.T) {
	c := autonomyConfig()
	a := c.Agents["bob"]
	a.Autonomy.MaxTurns = 1
	c.Agents["bob"] = a
	s := testStore(t, c)
	call(t, s, "alice", "ping.send", map[string]any{"to": []string{"bob"}, "text": "waiting observation"})
	instruction := taskIn(t, call(t, s, "", "instruct", map[string]any{"to": []string{"bob"}, "text": "authorized explicit instruction"}))
	task, lease := autonomyNext(t, s, "bob")
	if task.ID != instruction.ID {
		t.Fatal("observation outranked an instruction")
	}
	autonomyFinish(t, s, "bob", task, lease)
	status := autonomyStatusIn(t, s, "bob")
	if status.Ready || status.State != "halted" || status.Pending != 1 || status.TurnsUsed != 1 {
		t.Fatalf("budget bypass %+v", status)
	}
	expectFault(t, s, "bob", "task.claim", "busy", map[string]any{})
	call(t, s, "", "autonomy.control", map[string]any{"agent": "bob", "mode": "running", "version": status.Version})
	if autonomyStatusIn(t, s, "bob").Ready {
		t.Fatal("resume silently renewed turn budget")
	}
}

func TestWakeOverflowRetainsMessagesAndBoundsQueue(t *testing.T) {
	c := autonomyConfig()
	a := c.Agents["bob"]
	a.Autonomy.MaxBacklog = 2
	a.Autonomy.BatchSize = 1
	c.Agents["bob"] = a
	s := testStore(t, c)
	var ids []string
	for _, text := range []string{"first", "second", "third"} {
		var out struct {
			Message Message `json:"message"`
		}
		json.Unmarshal(call(t, s, "alice", "ping.send", map[string]any{"to": []string{"bob"}, "text": text}), &out)
		ids = append(ids, out.Message.ID)
	}
	status := autonomyStatusIn(t, s, "bob")
	if status.Pending != 2 || status.Dropped != 1 {
		t.Fatalf("unbounded queue %+v", status)
	}
	for _, id := range ids {
		call(t, s, "bob", "message.get", map[string]any{"id": id})
	}
	task, lease := autonomyNext(t, s, "bob")
	if len(task.Origin.MessageIDs) != 1 || task.Origin.MessageIDs[0] != ids[0] {
		t.Fatal("queue reordered or silently evicted message")
	}
	autonomyFinish(t, s, "bob", task, lease)
	if autonomyStatusIn(t, s, "bob").Pending != 1 {
		t.Fatal("batch consumed excess messages")
	}
}

func TestOwnerPauseCooldownAckAndStopHaveExplicitEffects(t *testing.T) {
	c := autonomyConfig()
	a := c.Agents["bob"]
	a.Autonomy.CooldownSeconds = 60
	c.Agents["bob"] = a
	s := testStore(t, c)
	call(t, s, "alice", "ping.send", map[string]any{"to": []string{"bob"}, "text": "first"})
	state := autonomyStatusIn(t, s, "bob")
	expectFault(t, s, "bob", "autonomy.control", "forbidden", map[string]any{"mode": "stopped", "version": state.Version})
	call(t, s, "", "autonomy.control", map[string]any{"agent": "bob", "mode": "paused", "version": state.Version})
	if autonomyStatusIn(t, s, "bob").Ready {
		t.Fatal("pause allowed dispatch")
	}
	state = autonomyStatusIn(t, s, "bob")
	call(t, s, "", "autonomy.control", map[string]any{"agent": "bob", "mode": "running", "version": state.Version})
	task, lease := autonomyNext(t, s, "bob")
	autonomyFinish(t, s, "bob", task, lease)
	var posted struct {
		Message Message `json:"message"`
	}
	json.Unmarshal(call(t, s, "alice", "ping.send", map[string]any{"to": []string{"bob"}, "text": "next"}), &posted)
	if status := autonomyStatusIn(t, s, "bob"); status.Ready || status.State != "cooldown" {
		t.Fatalf("cooldown ignored %+v", status)
	}
	call(t, s, "bob", "autonomy.ack", map[string]any{"message_id": posted.Message.ID})
	if status := autonomyStatusIn(t, s, "bob"); status.Pending != 0 || status.Cursor != posted.Message.ID {
		t.Fatal("manual read cursor not durable")
	}
	state = autonomyStatusIn(t, s, "bob")
	call(t, s, "", "autonomy.control", map[string]any{"agent": "bob", "mode": "stopped", "version": state.Version})
	if autonomyStatusIn(t, s, "bob").Mode != "stopped" {
		t.Fatal("stop not durable")
	}
}

func TestWakeDeliveryAndReplayRecheckCurrentTeamHistory(t *testing.T) {
	c := dynamicConfig()
	a := c.Agents["bob"]
	a.Autonomy = managedconfig.Autonomy{Enabled: true, Boards: []string{"*"}}
	c.Agents["bob"] = a
	s := testStore(t, c)
	team := teamIn(t, call(t, s, "", "team.create", map[string]any{"name": "wake boundary"}))
	team = teamIn(t, call(t, s, "", "team.invite", map[string]any{"id": team.ID, "agent": "bob", "version": team.Version}))
	team = teamIn(t, call(t, s, "bob", "team.join", map[string]any{"id": team.ID, "version": team.Version}))
	before := historyPost(t, s, team.Board, "private before rejoin")
	cred := credential(t, s, "bob")
	raw, problem := invoke(s, cred, "worker.next", map[string]any{}, "before-leave")
	if problem != nil {
		t.Fatal(problem)
	}
	task := taskIn(t, raw)
	team = teamIn(t, call(t, s, "bob", "team.leave", map[string]any{"id": team.ID, "version": team.Version}))
	read := call(t, s, "bob", "wake.read", map[string]any{"id": task.ID})
	if strings.Contains(string(read), before.Text) || !strings.Contains(string(read), `"skipped":true`) {
		t.Fatal("revoked source delivered")
	}
	again, problem := invoke(s, cred, "worker.next", map[string]any{}, "before-leave")
	if problem != nil || strings.Contains(string(again), before.ID) {
		t.Fatalf("cached claim restored revoked reference: %s %v", again, problem)
	}
	team = teamIn(t, call(t, s, "", "team.invite", map[string]any{"id": team.ID, "agent": "bob", "version": team.Version}))
	team = teamIn(t, call(t, s, "bob", "team.join", map[string]any{"id": team.ID, "version": team.Version}))
	if read = call(t, s, "bob", "wake.read", map[string]any{"id": task.ID}); strings.Contains(string(read), before.Text) || !strings.Contains(string(read), `"skipped":true`) {
		t.Fatal("rejoin bypassed since-join history floor")
	}
}

func TestWakePromptBytesAreBoundedAndOversizeSourcesExplicit(t *testing.T) {
	c := autonomyConfig()
	c.Limits.MaxMessageBytes = 256 << 10
	s := testStore(t, c)
	body := strings.Repeat("\x00", 50000)
	call(t, s, "alice", "board.post", map[string]any{"board": "team", "text": body})
	task, _ := autonomyNext(t, s, "bob")
	raw := call(t, s, "bob", "wake.read", map[string]any{"id": task.ID})
	if len(raw) > wakeContextBytes || !strings.Contains(string(raw), "Source content omitted") || !strings.Contains(string(raw), "omitted_message_ids") {
		t.Fatal("oversized source caused unbounded/implicit context")
	}
	original := call(t, s, "bob", "message.get", map[string]any{"id": task.Origin.MessageIDs[0]})
	var out struct {
		Message Message `json:"message"`
	}
	json.Unmarshal(original, &out)
	if out.Message.Text != body {
		t.Fatal("context bound changed original message")
	}
}

func TestOversizeSourceMetadataUsesMinimalReferenceAndCapsAncestry(t *testing.T) {
	c := autonomyConfig()
	a := c.Agents["bob"]
	a.Autonomy.DirectNotes = true
	c.Agents["bob"] = a
	s := testStore(t, c)
	message := Message{ID: randomID("msg"), Kind: "note", From: "alice", To: []string{"bob"}, Text: "small body, large historic fanout", CreatedAt: now(), Provenance: &Provenance{Hop: 2}}
	for i := 0; i < 20000; i++ {
		message.To = append(message.To, strings.Repeat("recipient", 8))
	}
	for i := 0; i < 100; i++ {
		message.Provenance.RootMessageIDs = append(message.Provenance.RootMessageIDs, randomID("root"))
	}
	if err := save(context.Background(), s.db, "message", message.ID, message.From, "", "", message); err != nil {
		t.Fatal(err)
	}
	if err := s.enqueueWakes(context.Background(), s.db, message); err != nil {
		t.Fatal(err)
	}
	task, _ := autonomyNext(t, s, "bob")
	if len(task.Origin.RootMessageIDs) != maxProvenanceRoots || !task.Origin.RootsTruncated || task.Origin.Hop != 2 {
		t.Fatal("ancestry grew without visible omission or lost hop")
	}
	raw := call(t, s, "bob", "wake.read", map[string]any{"id": task.ID})
	if len(raw) > wakeContextBytes || !strings.Contains(string(raw), "Source content omitted") {
		t.Fatal("fanout metadata bypassed context cap")
	}
	var read struct {
		Messages []Message `json:"messages"`
	}
	json.Unmarshal(raw, &read)
	if len(read.Messages) != 1 || len(read.Messages[0].To) != 0 || read.Messages[0].Provenance != nil {
		t.Fatal("reference-only source retained large optional metadata")
	}
	posted := call(t, s, "bob", "board.post", map[string]any{"board": "team", "text": "causal continuation"})
	var out struct {
		Message Message `json:"message"`
	}
	json.Unmarshal(posted, &out)
	if len(out.Message.Provenance.RootMessageIDs) != maxProvenanceRoots || !out.Message.Provenance.RootsTruncated || out.Message.Provenance.Hop != 3 {
		t.Fatal("continuation lost bounded ancestry/hop")
	}
}

func TestCausalHopLimitIsBrokerDerivedAndStopsWakeChains(t *testing.T) {
	c := autonomyConfig()
	for _, id := range []string{"alice", "bob"} {
		a := c.Agents[id]
		a.Autonomy = managedconfig.Autonomy{Enabled: true, Boards: []string{"team"}, MaxHops: 1}
		c.Agents[id] = a
	}
	s := testStore(t, c)
	historyPost(t, s, "team", "root observation")
	task, lease := autonomyNext(t, s, "bob")
	if task.Origin.Hop != 0 {
		t.Fatal("root hop not zero")
	}
	call(t, s, "bob", "board.post", map[string]any{"board": "team", "text": "first causal response"})
	autonomyFinish(t, s, "bob", task, lease)
	other, otherLease := autonomyNext(t, s, "alice")
	if other.Origin.Hop != 1 {
		t.Fatal("broker lost causal hop")
	}
	call(t, s, "alice", "board.post", map[string]any{"board": "team", "text": "second causal response exceeds receiver hop budget"})
	autonomyFinish(t, s, "alice", other, otherLease)
	if autonomyStatusIn(t, s, "bob").Ready {
		t.Fatal("hop-limited loop woke receiver")
	}
	expectFault(t, s, "alice", "board.post", "invalid", map[string]any{"board": "team", "text": "forge chain reset", "provenance": map[string]any{"hop": 0}})
}

func TestAutonomySafetyControlsAtQuotaAndAfterDeadline(t *testing.T) {
	s := testStore(t, autonomyConfig())
	call(t, s, "alice", "ping.send", map[string]any{"to": []string{"bob"}, "text": "wake"})
	var records, requests int
	s.db.QueryRow("SELECT count(*) FROM records").Scan(&records)
	s.db.QueryRow("SELECT count(*) FROM requests").Scan(&requests)
	reserve, err := lifecycleReserve(context.Background(), s.db)
	if err != nil {
		t.Fatal(err)
	}
	s.cfg.Limits.MaxRecords = records + reserve
	s.cfg.Limits.MaxRequests = requests + reserve
	status := autonomyStatusIn(t, s, "bob")
	call(t, s, "", "autonomy.control", map[string]any{"agent": "bob", "mode": "paused", "version": status.Version})
	status = autonomyStatusIn(t, s, "bob")
	call(t, s, "", "autonomy.control", map[string]any{"agent": "bob", "mode": "stopped", "version": status.Version})
	if _, err = s.db.Exec("INSERT OR REPLACE INTO metadata(key,value) VALUES('runtime_deadline',?)", time.Now().Add(-time.Minute).UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	status = autonomyStatusIn(t, s, "bob")
	expectFault(t, s, "", "autonomy.control", "limit", map[string]any{"agent": "bob", "mode": "running", "version": status.Version})
}

func TestFirstAutonomyPauseIsReservedBeforeAnyMessageOrClaim(t *testing.T) {
	s := testStore(t, autonomyConfig())
	var stateRows int
	s.db.QueryRow("SELECT count(*) FROM records WHERE kind='autonomy'").Scan(&stateRows)
	if stateRows != 1 {
		t.Fatal("enabled agent did not receive initial bounded state")
	}
	var records, requests int
	s.db.QueryRow("SELECT count(*) FROM records").Scan(&records)
	s.db.QueryRow("SELECT count(*) FROM requests").Scan(&requests)
	reserved, err := lifecycleReserve(context.Background(), s.db)
	if err != nil {
		t.Fatal(err)
	}
	s.cfg.Limits.MaxRecords = records + reserved
	s.cfg.Limits.MaxRequests = requests + reserved
	status := autonomyStatusIn(t, s, "bob")
	call(t, s, "", "autonomy.control", map[string]any{"agent": "bob", "mode": "paused", "version": status.Version})
	status = autonomyStatusIn(t, s, "bob")
	call(t, s, "", "autonomy.control", map[string]any{"agent": "bob", "mode": "stopped", "version": status.Version})
}
