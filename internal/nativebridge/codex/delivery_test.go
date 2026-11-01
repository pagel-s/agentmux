package codex

import (
	"context"
	"encoding/json"
	"io"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"agentmux/internal/nativebridge"
)

type fakeConnection struct {
	mu            sync.Mutex
	delivery      *nativebridge.Delivery
	registrations []nativebridge.Registration
	events        []nativebridge.Event
	status        nativebridge.Status
	fencedID      string
}

func (f *fakeConnection) Register(_ context.Context, r nativebridge.Registration) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.registrations = append(f.registrations, r)
	return nil
}
func (f *fakeConnection) Poll(_ context.Context, r nativebridge.Readiness) (*nativebridge.Delivery, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r.State != "idle" {
		return nil, nil
	}
	d := f.delivery
	f.delivery = nil
	return d, nil
}
func (f *fakeConnection) Report(_ context.Context, e nativebridge.Event) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events = append(f.events, e)
	if e.ID == f.fencedID && e.Phase != "submitting" {
		return fencedFault{}
	}
	return nil
}

type fencedFault struct{}

func (fencedFault) Error() string     { return "conflict: native task attempt changed" }
func (fencedFault) ErrorCode() string { return "conflict" }
func (f *fakeConnection) Status(context.Context) (nativebridge.Status, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.status, nil
}
func (f *fakeConnection) phases() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, e := range f.events {
		out = append(out, e.Phase)
	}
	return out
}

type engineFixture struct {
	p              *proxy
	conn           *fakeConnection
	requests       chan message
	done           chan struct{}
	cancel         context.CancelFunc
	mu             sync.Mutex
	methods        []string
	queueText      string
	dropQueue      bool
	invalidReceipt bool
	threadState    string
}

func newEngineFixture(t *testing.T, dropQueue bool) *engineFixture {
	return newEngineFixtureContext(t, dropQueue, "")
}

func newEngineFixtureContext(t *testing.T, dropQueue bool, ownerContext string) *engineFixture {
	t.Helper()
	reader, writer := io.Pipe()
	ctx, cancel := context.WithCancel(context.Background())
	f := &engineFixture{p: newProxy(writer), conn: &fakeConnection{delivery: &nativebridge.Delivery{ID: "delivery-1", From: "claude", To: "codex", Kind: "instruction", Text: "peer text", TaskID: "task-1"}}, requests: make(chan message, 20), done: make(chan struct{}), cancel: cancel, dropQueue: dropQueue}
	f.p.initialized = true
	f.p.current = "root"
	f.p.state = "idle"
	f.p.ownerContext = ownerContext
	f.p.ownedRoots["root"] = true
	go func() { defer close(f.done); f.p.deliver(ctx, f.conn) }()
	go func() {
		decoder := json.NewDecoder(reader)
		for {
			var m message
			if decoder.Decode(&m) != nil {
				return
			}
			f.mu.Lock()
			f.methods = append(f.methods, m.Method)
			f.mu.Unlock()
			f.requests <- m
			var result any = map[string]any{}
			switch m.Method {
			case "thread/read":
				f.mu.Lock()
				state := f.threadState
				f.mu.Unlock()
				if state == "" {
					state = "idle"
				}
				result = map[string]any{"thread": map[string]any{"status": map[string]any{"type": state}}}
			case "thread/queue/list":
				result = map[string]any{"data": []any{}}
			case "thread/queue/add":
				var params struct {
					ClientID string `json:"clientUserMessageId"`
					Input    []struct {
						Text string `json:"text"`
					} `json:"input"`
				}
				_ = json.Unmarshal(m.Params, &params)
				f.mu.Lock()
				f.queueText = params.Input[0].Text
				invalid := f.invalidReceipt
				f.invalidReceipt = false
				f.mu.Unlock()
				if f.dropQueue {
					continue
				}
				if invalid {
					params.ClientID = "invalid-receipt"
				}
				result = map[string]any{"queuedSubmission": map[string]any{"id": "queue-1", "clientUserMessageId": params.ClientID}}
			case "thread/queue/delete":
				result = map[string]any{"deleted": true}
			}
			raw, _ := json.Marshal(map[string]any{"id": json.RawMessage(m.ID), "result": result})
			_, _, _ = f.p.serverMessage(raw)
		}
	}()
	t.Cleanup(func() {
		cancel()
		reader.Close()
		writer.Close()
		select {
		case <-f.done:
		case <-time.After(4 * time.Second):
			t.Error("adapter did not stop")
		}
	})
	return f
}
func (f *engineFixture) waitMethod(t *testing.T, method string) message {
	t.Helper()
	for {
		select {
		case m := <-f.requests:
			if m.Method == method {
				return m
			}
		case <-time.After(4 * time.Second):
			t.Fatal("no native request: " + method)
			return message{}
		}
	}
}
func waitPhase(t *testing.T, f *fakeConnection, phase string) {
	t.Helper()
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		for _, p := range f.phases() {
			if p == phase {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("no %s report; got %v", phase, f.phases())
}
func (f *engineFixture) emit(method string, params any) {
	raw, _ := json.Marshal(params)
	f.p.events <- notification{method, raw}
}

func TestQueueReceiptRunningAndCompletionAreDistinct(t *testing.T) {
	f := newEngineFixture(t, false)
	f.waitMethod(t, "thread/queue/add")
	waitPhase(t, f.conn, "submitted")
	f.conn.mu.Lock()
	reg := f.conn.registrations[0]
	f.conn.mu.Unlock()
	if reg.Session != "root" || !reg.Capabilities.NativeQueue || !reg.Capabilities.PreservesDraft {
		t.Fatalf("wrong capabilities: %+v", reg)
	}
	f.mu.Lock()
	prompt := f.queueText
	f.mu.Unlock()
	if !strings.Contains(prompt, `from "claude"`) || !strings.Contains(prompt, "not the human owner") {
		t.Fatalf("attribution missing: %q", prompt)
	}
	f.emit("item/started", map[string]any{"threadId": "child", "turnId": "wrong", "item": map[string]any{"type": "userMessage", "clientId": "delivery-1"}})
	f.emit("turn/completed", map[string]any{"threadId": "root", "turn": map[string]any{"id": "owner-turn", "status": "completed"}})
	f.emit("item/started", map[string]any{"threadId": "root", "turnId": "peer-turn", "item": map[string]any{"type": "userMessage", "clientId": "delivery-1"}})
	waitPhase(t, f.conn, "running")
	f.emit("turn/completed", map[string]any{"threadId": "root", "turn": map[string]any{"id": "peer-turn", "status": "completed", "items": []map[string]any{{"type": "agentMessage", "text": "commentary"}, {"type": "agentMessage", "text": "final answer"}}}})
	waitPhase(t, f.conn, "completed")
	if got := f.conn.phases(); !reflect.DeepEqual(got, []string{"submitting", "submitted", "running", "completed"}) {
		t.Fatalf("lifecycle incorrect: %v", got)
	}
	f.conn.mu.Lock()
	last := f.conn.events[len(f.conn.events)-1]
	f.conn.mu.Unlock()
	if last.NativeRef != "peer-turn" || last.Result != "final answer" {
		t.Fatalf("result incorrect: %+v", last)
	}
}

func TestCancellationOnlyDeletesKnownPeerQueue(t *testing.T) {
	f := newEngineFixture(t, false)
	f.waitMethod(t, "thread/queue/add")
	waitPhase(t, f.conn, "submitted")
	f.conn.mu.Lock()
	f.conn.status = nativebridge.Status{Cancel: true, ActiveID: "delivery-1"}
	f.conn.mu.Unlock()
	m := f.waitMethod(t, "thread/queue/delete")
	var params map[string]string
	_ = json.Unmarshal(m.Params, &params)
	if params["threadId"] != "root" || params["queuedSubmissionId"] != "queue-1" {
		t.Fatalf("cancel targeted unrelated work: %s", m.Params)
	}
	waitPhase(t, f.conn, "cancelled")
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, m := range f.methods {
		if m == "turn/interrupt" {
			t.Fatal("queue cancellation interrupted a human turn")
		}
	}
}

func TestActiveCancellationPinsExactPeerTurn(t *testing.T) {
	f := newEngineFixture(t, false)
	f.waitMethod(t, "thread/queue/add")
	waitPhase(t, f.conn, "submitted")
	f.emit("item/started", map[string]any{"threadId": "root", "turnId": "peer-turn", "item": map[string]any{"type": "userMessage", "clientId": "delivery-1"}})
	waitPhase(t, f.conn, "running")
	f.conn.mu.Lock()
	f.conn.status = nativebridge.Status{Cancel: true, ActiveID: "delivery-1"}
	f.conn.mu.Unlock()
	m := f.waitMethod(t, "turn/interrupt")
	var params map[string]string
	_ = json.Unmarshal(m.Params, &params)
	if params["threadId"] != "root" || params["turnId"] != "peer-turn" {
		t.Fatalf("unrelated turn targeted: %s", m.Params)
	}
	f.emit("turn/completed", map[string]any{"threadId": "root", "turn": map[string]any{"id": "peer-turn", "status": "interrupted"}})
	waitPhase(t, f.conn, "cancelled")
}

func TestUnconfirmedSubmissionNeverRetries(t *testing.T) {
	f := newEngineFixture(t, true)
	f.waitMethod(t, "thread/queue/add")
	f.cancel()
	waitPhase(t, f.conn, "uncertain")
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, m := range f.methods {
		if m == "thread/queue/add" {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("ambiguous delivery replayed %d times", n)
	}
}

func TestNewNativeConversationCannotRedirectQueuedDelivery(t *testing.T) {
	f := newEngineFixture(t, false)
	f.waitMethod(t, "thread/queue/add")
	waitPhase(t, f.conn, "submitted")
	f.p.mu.Lock()
	f.p.current = "new-root"
	f.p.state = "idle"
	f.p.mu.Unlock()
	m := f.waitMethod(t, "thread/queue/delete")
	if !strings.Contains(string(m.Params), `"threadId":"root"`) {
		t.Fatalf("old delivery redirected: %s", m.Params)
	}
	waitPhase(t, f.conn, "cancelled")
}

func TestResultLimitPreservesUTF8AndExplainsTruncation(t *testing.T) {
	for _, limit := range []int{1, 100, 256, 4096} {
		e := nativebridge.Event{Phase: "completed", Result: strings.Repeat("研究🌊", 2000)}
		limitResult(&e, limit)
		if len(e.Result) > limit || !utf8.ValidString(e.Result) || !strings.Contains(e.Reason, "full answer remains") {
			t.Fatalf("invalid limited result for %d: bytes=%d reason=%q", limit, len(e.Result), e.Reason)
		}
	}
	short := nativebridge.Event{Result: "normal answer"}
	limitResult(&short, 100)
	if short.Result != "normal answer" || short.Reason != "" {
		t.Fatalf("short result changed: %+v", short)
	}
}

func TestConfiguredResultLimitDoesNotBlockSettlement(t *testing.T) {
	f := newEngineFixture(t, false)
	f.conn.mu.Lock()
	f.conn.status.MaxResultBytes = 100
	f.conn.mu.Unlock()
	f.waitMethod(t, "thread/queue/add")
	waitPhase(t, f.conn, "submitted")
	f.emit("item/started", map[string]any{"threadId": "root", "turnId": "peer-turn", "item": map[string]any{"type": "userMessage", "clientId": "delivery-1"}})
	f.emit("turn/completed", map[string]any{"threadId": "root", "turn": map[string]any{"id": "peer-turn", "status": "completed", "items": []map[string]any{{"type": "agentMessage", "text": strings.Repeat("long answer🌊", 1000)}}}})
	waitPhase(t, f.conn, "completed")
	f.conn.mu.Lock()
	last := f.conn.events[len(f.conn.events)-1]
	f.conn.mu.Unlock()
	if len(last.Result) > 100 || !strings.Contains(last.Reason, "truncated") {
		t.Fatalf("broker bound ignored: %+v", last)
	}
}

func TestOwnerResolutionAllowsFreshDeliveryWithoutReplayingUncertainOne(t *testing.T) {
	f := newEngineFixture(t, false)
	f.mu.Lock()
	f.invalidReceipt = true
	f.mu.Unlock()
	f.conn.mu.Lock()
	f.conn.status = nativebridge.Status{State: "uncertain", ActiveID: "delivery-1"}
	f.conn.mu.Unlock()
	f.waitMethod(t, "thread/queue/add")
	waitPhase(t, f.conn, "uncertain")
	f.mu.Lock()
	f.threadState = "active"
	f.mu.Unlock()
	f.conn.mu.Lock()
	f.conn.delivery = &nativebridge.Delivery{ID: "delivery-2", From: "claude", To: "codex", Kind: "instruction", Text: "a new owner-authorized attempt"}
	f.conn.status = nativebridge.Status{State: "ready"}
	f.conn.mu.Unlock()
	time.Sleep(600 * time.Millisecond)
	f.mu.Lock()
	attempts := 0
	for _, method := range f.methods {
		if method == "thread/queue/add" {
			attempts++
		}
	}
	f.threadState = "idle"
	f.mu.Unlock()
	if attempts != 1 {
		t.Fatalf("fresh work started before uncertain native turn was quiescent: %d submissions", attempts)
	}
	m := f.waitMethod(t, "thread/queue/add")
	var params struct {
		ClientID string `json:"clientUserMessageId"`
	}
	_ = json.Unmarshal(m.Params, &params)
	if params.ClientID != "delivery-2" {
		t.Fatalf("uncertain request replayed: %s", m.Params)
	}
}

func TestBrokerFenceStopsQueuedPeerAndOwnerResolutionUnblocksFreshWork(t *testing.T) {
	f := newEngineFixture(t, false)
	f.conn.mu.Lock()
	f.conn.fencedID = "delivery-1"
	f.conn.status = nativebridge.Status{State: "uncertain", ActiveID: "delivery-1", Cancel: true}
	f.conn.mu.Unlock()
	f.waitMethod(t, "thread/queue/add")
	m := f.waitMethod(t, "thread/queue/delete")
	var cancellation map[string]string
	_ = json.Unmarshal(m.Params, &cancellation)
	if cancellation["threadId"] != "root" || cancellation["queuedSubmissionId"] != "queue-1" {
		t.Fatalf("broker recovery cancelled unrelated native work: %s", m.Params)
	}
	// The old broker receipt is fenced and cannot be acknowledged. Keep the
	// native attempt blocked until the owner explicitly resolves its task.
	time.Sleep(350 * time.Millisecond)
	f.mu.Lock()
	before := 0
	for _, method := range f.methods {
		if method == "thread/queue/add" {
			before++
		}
	}
	f.mu.Unlock()
	if before != 1 {
		t.Fatalf("fenced delivery replayed %d times before resolution", before)
	}
	f.conn.mu.Lock()
	f.conn.status = nativebridge.Status{State: "ready"}
	f.conn.delivery = &nativebridge.Delivery{ID: "delivery-2", From: "claude", To: "codex", Kind: "instruction", Text: "fresh explicit owner retry"}
	f.conn.mu.Unlock()
	m = f.waitMethod(t, "thread/queue/add")
	var submission struct {
		ID string `json:"clientUserMessageId"`
	}
	_ = json.Unmarshal(m.Params, &submission)
	if submission.ID != "delivery-2" {
		t.Fatalf("old fenced request was replayed: %s", m.Params)
	}
}

func TestBrokerFenceInterruptsOnlyCorrelatedPeerTurnBeforeResolution(t *testing.T) {
	f := newEngineFixture(t, false)
	f.waitMethod(t, "thread/queue/add")
	waitPhase(t, f.conn, "submitted")
	f.conn.mu.Lock()
	f.conn.fencedID = "delivery-1"
	f.conn.status = nativebridge.Status{State: "uncertain", ActiveID: "delivery-1", Cancel: true}
	f.conn.mu.Unlock()
	f.emit("item/started", map[string]any{"threadId": "root", "turnId": "peer-turn", "item": map[string]any{"type": "userMessage", "clientId": "delivery-1"}})
	m := f.waitMethod(t, "turn/interrupt")
	var cancellation map[string]string
	_ = json.Unmarshal(m.Params, &cancellation)
	if cancellation["threadId"] != "root" || cancellation["turnId"] != "peer-turn" {
		t.Fatalf("broker recovery interrupted unrelated turn: %s", m.Params)
	}
	f.emit("turn/completed", map[string]any{"threadId": "root", "turn": map[string]any{"id": "peer-turn", "status": "interrupted"}})
	f.conn.mu.Lock()
	f.conn.status = nativebridge.Status{State: "ready"}
	f.conn.delivery = &nativebridge.Delivery{ID: "delivery-2", From: "claude", To: "codex", Kind: "instruction", Text: "fresh authorized attempt"}
	f.conn.mu.Unlock()
	m = f.waitMethod(t, "thread/queue/add")
	var submission struct {
		ID string `json:"clientUserMessageId"`
	}
	_ = json.Unmarshal(m.Params, &submission)
	if submission.ID != "delivery-2" {
		t.Fatalf("old fenced turn replayed: %s", m.Params)
	}
}
