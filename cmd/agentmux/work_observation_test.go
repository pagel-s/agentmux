package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"agentmux/internal/managed"
	"agentmux/internal/managedconfig"
)

func TestWebResearchSavedSummarySurvivesBrokerShutdownWithoutInventingLiveAgents(t *testing.T) {
	root, err := os.MkdirTemp("", "amx-observe-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(root) })
	state := filepath.Join(root, "s")
	store, err := managed.Init(state, managedconfig.Config{Version: 2, Mode: "managed", Session: "research-observation", Agents: map[string]managedconfig.Agent{"peer": {Harness: "command", Command: []string{"/bin/cat"}, Sandbox: "local", Workdir: root}}})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	proof, err := store.InitResearchRunner()
	if err != nil {
		t.Fatal(err)
	}
	credential, err := managed.LoadCredential(managed.CredentialPath(state, ""))
	if err != nil {
		t.Fatal(err)
	}
	p := managed.ResearchProgress{Trial: "run-001", Attempt: 1, Condition: "observable", StateDir: state, State: "stopped", Revision: 1, AckVersion: 1, Population: []string{"peer"}}
	args, _ := json.Marshal(p)
	if _, problem := store.Call(context.Background(), credential.Token, managed.Request{Operation: "research.publish", ResearchRunnerProof: proof, RequestID: "publish-terminal", Args: args}); problem != nil {
		t.Fatal(problem)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- managed.Serve(ctx, store) }()
	defer func() {
		cancel()
		if done != nil {
			<-done
		}
	}()
	client := managed.NewClient(credential)
	deadline := time.Now().Add(3 * time.Second)
	for {
		if _, problem := client.Call(context.Background(), managed.Request{Operation: "identity"}); problem == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("broker not ready")
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	done = nil
	handler := workWebHandler(state, client, "owner-link", "127.0.0.1:4242")
	for _, path := range []string{"/api/snapshot?agent=peer", "/api/research-status"} {
		req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:4242"+path, nil)
		req.Header.Set("Authorization", "Bearer owner-link")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, req)
		var body struct {
			Live     bool                    `json:"live"`
			Research *managed.ResearchStatus `json:"research"`
			Saved    *managed.ResearchStatus `json:"saved_research"`
			Agents   []managed.AgentInfo     `json:"agents"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		status := body.Research
		if status == nil {
			status = body.Saved
		}
		if body.Live || status == nil || status.Progress.State != "stopped" || status.RunnerHeartbeatAt != "" || len(body.Agents) != 0 {
			t.Fatalf("saved summary pretended to be a live workspace: %s", response.Body.String())
		}
	}
}
