package managedlaunch

import (
	"context"
	"errors"
	"os"
	"testing"
)

func TestDescribeTransportOrCancellationIsUnknownRatherThanAbsent(t *testing.T) {
	state, err := os.MkdirTemp("", "amx-describe-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(state)
	runtime := Runtime{Version: 1, Session: "example", AttemptID: "fixture", State: "running", Agents: map[string]AgentPane{}}
	if err := writeRuntime(state, runtime); err != nil {
		t.Fatal(err)
	}
	l := &Launcher{options: Options{StateDir: state}, run: func(ctx context.Context, _ ...string) (string, error) {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return "", errors.New("transport unavailable")
	}}
	for _, canceled := range []bool{false, true} {
		ctx, cancel := context.WithCancel(context.Background())
		if canceled {
			cancel()
		}
		r, err := l.Describe(ctx)
		cancel()
		if err == nil || r.State != "unknown" {
			t.Fatalf("uncertainty represented as absence: %+v %v", r, err)
		}
	}
}
