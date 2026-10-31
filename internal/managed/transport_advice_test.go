package managed

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

func TestTransportAdviceDistinguishesObservationFromUncertainWrite(t *testing.T) {
	c := NewClient(Credential{Socket: filepath.Join(t.TempDir(), "offline.sock"), Token: "fixture-only"})
	for _, op := range []string{"identity", "agent.list", "research.status", "memory.put"} {
		_, problem := c.Call(context.Background(), Request{Operation: op, RequestID: "explicit-uat-request"})
		if problem == nil || problem.Code != "transport" {
			t.Fatalf("offline broker not reported: %s %+v", op, problem)
		}
		uncertain := strings.Contains(problem.Message, "uncertain") && strings.Contains(problem.Message, "same request_id")
		if uncertain != (op == "memory.put") {
			t.Fatalf("misleading retry advice for %s: %s", op, problem.Message)
		}
	}
}
