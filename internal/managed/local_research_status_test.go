package managed

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLocalResearchStatusAfterBrokerExit(t *testing.T) {
	for _, state := range []string{"completed", "stopped"} {
		t.Run(state, func(t *testing.T) {
			dir, err := os.MkdirTemp("/tmp", "amx-local-research-")
			if err != nil {
				t.Fatal(err)
			}
			defer os.RemoveAll(dir)
			s, err := Init(filepath.Join(dir, "s"), testConfig())
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			proof, err := s.InitResearchRunner()
			if err != nil {
				t.Fatal(err)
			}
			p := ResearchProgress{Trial: "run-001", Attempt: 1, StateDir: s.dir, State: "starting", Revision: 1, AckVersion: 1, Tasks: map[string]string{}}
			researchRunnerCall(t, s, proof, "research.publish", p)
			client := NewClient(credential(t, s, ""))
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan error, 1)
			go func() { done <- Serve(ctx, s) }()
			stopped := false
			defer func() {
				if !stopped {
					cancel()
					<-done
				}
			}()
			ready := false
			for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); {
				if _, problem := client.Call(ctx, Request{Operation: "identity"}); problem == nil {
					ready = true
					break
				}
				select {
				case err := <-done:
					stopped = true
					t.Fatalf("broker exited before readiness: %v", err)
				default:
				}
				time.Sleep(10 * time.Millisecond)
			}
			if !ready {
				t.Fatal("broker never became ready")
			}
			researchRunnerCall(t, s, proof, "research.heartbeat", map[string]any{})
			if state == "stopped" {
				call(t, s, "", "research.control", map[string]any{"version": 1, "action": "stop", "reason": "finish local checkpoint fixture"})
				p.AckVersion = 2
			}
			p.State = state
			p.Revision = 2
			researchRunnerCall(t, s, proof, "research.publish", p)
			cancel()
			if err := <-done; err != nil {
				t.Fatal(err)
			}
			stopped = true
			var beforeRecords, beforeRequests int
			s.db.QueryRow("SELECT count(*) FROM records").Scan(&beforeRecords)
			s.db.QueryRow("SELECT count(*) FROM requests").Scan(&beforeRequests)
			if err := s.Close(); err != nil {
				t.Fatal(err)
			}
			status, problem := client.LocalResearchStatus(context.Background(), s.dir)
			if problem != nil || status == nil || status.Progress.State != state || status.RunnerHeartbeatAt != "" {
				t.Fatalf("wrong durable checkpoint after broker exit: %+v %v", status, problem)
			}
			check, err := Open(s.dir)
			if err != nil {
				t.Fatal(err)
			}
			defer check.Close()
			var afterRecords, afterRequests int
			check.db.QueryRow("SELECT count(*) FROM records").Scan(&afterRecords)
			check.db.QueryRow("SELECT count(*) FROM requests").Scan(&afterRequests)
			if beforeRecords != afterRecords || beforeRequests != afterRequests {
				t.Fatal("local status mutated durable records or receipts")
			}
		})
	}
}

func TestLocalResearchStatusRetainsOriginalAuthorization(t *testing.T) {
	s, _, _ := researchFixture(t)
	original := NewClient(credential(t, s, ""))
	participant := NewClient(credential(t, s, "alice"))
	if _, problem := participant.LocalResearchStatus(context.Background(), s.dir); problem == nil || problem.Code != "forbidden" {
		t.Fatal("participant obtained owner-only research status")
	}
	participant.credential.AgentID = ownerID
	if _, problem := participant.LocalResearchStatus(context.Background(), s.dir); problem == nil || problem.Code != "forbidden" {
		t.Fatal("declared owner ID overrode authenticated participant identity")
	}
	if _, problem := original.LocalResearchStatus(context.Background(), filepath.Join(s.dir, "elsewhere")); problem == nil || problem.Code != "invalid" {
		t.Fatal("client read a different broker state")
	}
	if _, err := s.Rotate(ownerID); err != nil {
		t.Fatal(err)
	}
	if _, problem := original.LocalResearchStatus(context.Background(), s.dir); problem == nil || problem.Code != "unauthorized" {
		t.Fatalf("revoked client silently reloaded owner credentials: %v", problem)
	}
	if status, problem := NewClient(credential(t, s, "")).LocalResearchStatus(context.Background(), s.dir); problem != nil || status == nil {
		t.Fatalf("current owner could not read checkpoint: %v", problem)
	}
}

func TestLocalResearchStatusRejectsCorruptCheckpointAndNeverRecoversTasks(t *testing.T) {
	s, _, _ := researchFixture(t)
	client := NewClient(credential(t, s, ""))
	task := instruction(t, s)
	claimed := taskIn(t, call(t, s, "bob", "task.claim", map[string]any{}))
	if task.ID != claimed.ID {
		t.Fatal("fixture claim mismatch")
	}
	before := taskIn(t, call(t, s, "", "task.get", map[string]any{"id": task.ID}))
	if _, problem := client.LocalResearchStatus(context.Background(), s.dir); problem != nil {
		t.Fatal(problem)
	}
	after := taskIn(t, call(t, s, "", "task.get", map[string]any{"id": task.ID}))
	if before.State != after.State || before.Delivery != after.Delivery || before.Version != after.Version {
		t.Fatal("local status recovered or mutated live delivery")
	}
	if _, err := s.db.Exec("UPDATE records SET data='{}' WHERE kind='research'"); err != nil {
		t.Fatal(err)
	}
	if _, problem := client.LocalResearchStatus(context.Background(), s.dir); problem == nil || problem.Code != "internal" {
		t.Fatalf("corrupt checkpoint accepted: %v", problem)
	}
}
