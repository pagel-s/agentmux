package codex

import (
	"agentmux/internal/nativebridge"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestOwnerContextPrecedesHumanTurnWithoutReplacingNativeConfig(t *testing.T) {
	for _, failure := range []bool{false, true} {
		name := "success"
		if failure {
			name = "unsupported"
		}
		t.Run(name, func(t *testing.T) {
			r, w := io.Pipe()
			defer r.Close()
			defer w.Close()
			p := newProxy(w)
			p.ownerContext = "OWNER CONFIGURED ROLE"
			p.current = "root"
			p.ownedRoots["root"] = true
			var diagnostics bytes.Buffer
			p.diagnostics = &diagnostics
			var mu sync.Mutex
			var methods []string
			var injected string
			done := make(chan struct{})
			go func() {
				defer close(done)
				dec := json.NewDecoder(r)
				for {
					var m message
					if dec.Decode(&m) != nil {
						return
					}
					mu.Lock()
					methods = append(methods, m.Method)
					if m.Method == "thread/inject_items" {
						injected = string(m.Params)
					}
					mu.Unlock()
					if m.Method == "thread/inject_items" {
						reply := map[string]any{"id": json.RawMessage(m.ID), "result": map[string]any{}}
						if failure {
							reply = map[string]any{"id": json.RawMessage(m.ID), "error": map[string]any{"code": -32601, "message": "unsupported"}}
						}
						raw, _ := json.Marshal(reply)
						_, _, _ = p.serverMessage(raw)
					}
				}
			}()
			for i := 0; i < 2; i++ {
				if err := p.forwardClient([]byte(`{"id":1,"method":"turn/start","params":{"threadId":"root","input":[{"type":"text","text":"native human input"}],"model":"owner-model","approvalPolicy":"on-request"}}`)); err != nil {
					t.Fatal(err)
				}
			}
			w.Close()
			<-done
			mu.Lock()
			defer mu.Unlock()
			if strings.Join(methods, ",") != "thread/inject_items,turn/start,turn/start" {
				t.Fatalf("context duplicated or human turn lost: %v", methods)
			}
			var body struct {
				ThreadID string `json:"threadId"`
				Items    []struct {
					Role    string                        `json:"role"`
					Content []struct{ Type, Text string } `json:"content"`
				} `json:"items"`
			}
			if json.Unmarshal([]byte(injected), &body) != nil || body.ThreadID != "root" || len(body.Items) != 1 || body.Items[0].Role != "developer" || !strings.Contains(body.Items[0].Content[0].Text, "OWNER CONFIGURED ROLE") {
				t.Fatalf("invalid attributed owner context: %s", injected)
			}
			if failure && (p.fault == "" || !strings.Contains(diagnostics.String(), "manual inbox")) {
				t.Fatal("unsupported append did not preserve an actionable manual mode")
			}
			if !failure && p.fault != "" {
				t.Fatal(p.fault)
			}
		})
	}
}

func TestAmbiguousOwnerContextAppendIsNeverRetried(t *testing.T) {
	r, w := io.Pipe()
	defer r.Close()
	defer w.Close()
	p := newProxy(w)
	p.ownerContext = "role"
	p.current = "root"
	p.ownedRoots["root"] = true
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	requests := make(chan string, 4)
	go func() {
		dec := json.NewDecoder(r)
		for {
			var m message
			if dec.Decode(&m) != nil {
				return
			}
			requests <- m.Method
		}
	}()
	if p.prepareContext(ctx, "root") == nil {
		t.Fatal("lost append response was treated as confirmed")
	}
	if p.prepareContext(context.Background(), "root") == nil {
		t.Fatal("ambiguous append was retried")
	}
	select {
	case method := <-requests:
		if method != "thread/inject_items" {
			t.Fatal(method)
		}
	case <-time.After(time.Second):
		t.Fatal("append was not sent")
	}
	select {
	case method := <-requests:
		t.Fatalf("ambiguous context append replayed: %s", method)
	default:
	}
	if p.fault == "" {
		t.Fatal("unknown owner context did not disable automatic delivery")
	}
}

func TestOnlyNativeSelectedRootsReceiveOwnerContext(t *testing.T) {
	var output bytes.Buffer
	p := newProxy(&output)
	p.ownerContext = "role"
	p.current = "human-root"
	p.ownedRoots["human-root"] = true
	for _, thread := range []string{"internal-title", "native-subagent"} {
		if err := p.prepareContext(context.Background(), thread); err != nil {
			t.Fatal(err)
		}
	}
	if output.Len() != 0 {
		t.Fatal("owner role context was appended to a background thread")
	}
}

func TestPeerQueueWaitsForOwnerContext(t *testing.T) {
	f := newEngineFixtureContext(t, false, "owner-configured role")
	f.waitMethod(t, "thread/queue/add")
	f.mu.Lock()
	defer f.mu.Unlock()
	inject, queue := -1, -1
	for i, method := range f.methods {
		if method == "thread/inject_items" {
			if inject >= 0 {
				t.Fatal("owner context repeated")
			}
			inject = i
		}
		if method == "thread/queue/add" {
			queue = i
		}
	}
	if inject < 0 || queue <= inject {
		t.Fatalf("peer native input preceded owner context: %v", f.methods)
	}
}

func TestPrivateOwnerContextFileIsBoundedAndTakesPrecedence(t *testing.T) {
	t.Setenv(ContextEnv, "environment fallback")
	t.Setenv(ContextFileEnv, "")
	if got, err := ownerContextFromEnvironment(); err != nil || got != "environment fallback" {
		t.Fatalf("context fallback: %q %v", got, err)
	}
	path := filepath.Join(t.TempDir(), "context")
	if err := os.WriteFile(path, []byte("private file context"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(ContextFileEnv, path)
	if got, err := ownerContextFromEnvironment(); err != nil || got != "private file context" {
		t.Fatalf("file precedence: %q %v", got, err)
	}
	if err := os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := ownerContextFromEnvironment(); err == nil {
		t.Fatal("public context file accepted")
	}
	if err := os.Chmod(path, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(strings.Repeat("x", (4<<20)+1)), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := ownerContextFromEnvironment(); err == nil {
		t.Fatal("oversized context file accepted")
	}
	t.Setenv(ContextFileEnv, path+"-missing")
	if _, err := ownerContextFromEnvironment(); err == nil {
		t.Fatal("missing file silently fell back to environment")
	}
}

func TestHumanInterventionWithholdsExactPeerOutputAndError(t *testing.T) {
	for _, kind := range []string{"steer", "answer", "separate turn"} {
		t.Run(kind, func(t *testing.T) {
			var output bytes.Buffer
			p := newProxy(&output)
			turn := "peer-turn"
			if kind == "separate turn" {
				turn = "other-human-turn"
			}
			if kind == "answer" {
				raw, _ := json.Marshal(map[string]any{"id": 99, "method": "item/tool/requestUserInput", "params": map[string]any{"threadId": "root", "turnId": turn, "questions": []map[string]any{{"question": "PRIVATE QUESTION"}}}})
				if _, _, err := p.serverMessage(raw); err != nil {
					t.Fatal(err)
				}
				if err := p.forwardClient([]byte(`{"id":99,"result":{"answers":{"q":{"answers":["PRIVATE HUMAN ANSWER"]}}}}`)); err != nil {
					t.Fatal(err)
				}
			} else {
				raw, _ := json.Marshal(map[string]any{"id": 1, "method": "turn/steer", "params": map[string]any{"threadId": "root", "expectedTurnId": turn, "input": []map[string]any{{"type": "text", "text": "PRIVATE HUMAN STEERING"}}}})
				if err := p.forwardClient(raw); err != nil {
					t.Fatal(err)
				}
			}
			marker := <-p.events
			if strings.Contains(string(marker.params), "PRIVATE") || strings.Contains(string(marker.params), "answers") {
				t.Fatalf("human text leaked into native marker: %s", marker.params)
			}
			d := pendingDelivery{delivery: nativebridge.Delivery{ID: "peer-delivery"}, session: "root", turn: "peer-turn"}
			d.observe(marker)
			raw, _ := json.Marshal(map[string]any{"threadId": "root", "turn": map[string]any{"id": "peer-turn", "status": "completed", "error": map[string]any{"message": "PRIVATE NATIVE ERROR"}, "items": []map[string]any{{"type": "agentMessage", "text": "PRIVATE NATIVE ANSWER"}}}})
			report := d.observe(notification{"turn/completed", raw})
			if report == nil || report.Phase != "completed" {
				t.Fatalf("lifecycle evidence lost: %+v", report)
			}
			if kind == "separate turn" {
				if report.Result != "PRIVATE NATIVE ANSWER" {
					t.Fatal("unrelated human turn withheld peer output")
				}
			} else {
				encoded, _ := json.Marshal(report)
				if strings.Contains(string(encoded), "PRIVATE") || !strings.Contains(report.Result, "withheld") {
					t.Fatalf("human intervention leaked output/error: %s", encoded)
				}
			}
		})
	}
}
