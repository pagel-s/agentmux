package managed

import (
	"context"
	"encoding/json"
	"testing"
)

func TestResearchCompletionFencesConcurrentControls(t *testing.T) {
	for _, action := range []string{"pause", "step", "stop"} {
		t.Run(action, func(t *testing.T) {
			s, proof, p := researchFixture(t)
			p.Revision++
			p.State = "running"
			researchRunnerCall(t, s, proof, "research.publish", p)
			call(t, s, "", "research.control", map[string]any{"version": 1, "action": action, "reason": "control won terminal race"})
			p.Revision++
			p.State = "completed"
			publishConflict := func() {
				t.Helper()
				raw, _ := json.Marshal(p)
				_, problem := s.Call(context.Background(), credential(t, s, "").Token, Request{Operation: "research.publish", ResearchRunnerProof: proof, RequestID: randomID("complete"), Args: raw})
				if problem == nil || problem.Code != "conflict" {
					t.Fatalf("completion lost accepted %s: %v", action, problem)
				}
				if state := researchState(t, s); state.Progress.State != "running" || state.Progress.AckVersion != 1 || state.Control.Version != 2 {
					t.Fatalf("rejected completion mutated progress: %+v", state)
				}
			}
			publishConflict()
			p.AckVersion = 2
			if action == "stop" {
				publishConflict()
				p.State = "stopped"
			}
			researchRunnerCall(t, s, proof, "research.publish", p)
			for _, late := range []string{"pause", "resume", "step", "stop"} {
				expectFault(t, s, "", "research.control", "conflict", map[string]any{"version": 2, "action": late, "reason": "completion won terminal race"})
			}
		})
	}
}
