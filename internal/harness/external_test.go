package harness

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestExternalCodexRequiresAndHonorsOuterWrapper(t *testing.T) {
	req := Request{Harness: "codex", Prompt: "unused", ExternalSandbox: true}
	if _, err := Build(req); err == nil {
		t.Fatal("external execution accepted without a wrapper")
	}
	blocked := errors.New("outer boundary unavailable")
	called := false
	req.Wrap = func(spec Spec) (Spec, error) {
		called = true
		joined := strings.Join(spec.Args, " ")
		if !strings.Contains(joined, "--sandbox danger-full-access") || strings.Contains(joined, "bypass-approvals") {
			t.Fatal("unexpected external policy arguments")
		}
		return Spec{}, blocked
	}
	if _, err := Run(context.Background(), req); !errors.Is(err, blocked) || !called {
		t.Fatal("failed outer wrapper did not prevent execution")
	}
	req.Harness = "claude"
	if _, err := Build(req); err == nil {
		t.Fatal("external Codex mode accepted for another harness")
	}
}
