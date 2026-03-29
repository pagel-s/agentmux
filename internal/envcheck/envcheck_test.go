package envcheck

import (
	"os"
	"testing"

	"agentmux/internal/config"
)

func TestCheck(t *testing.T) {
	// 1. All met
	reqs := config.Requirements{
		Binaries: []string{"ls", "sh"},
		Env:      []string{"PATH"},
	}
	missing := Check(reqs)
	if len(missing) > 0 {
		t.Errorf("expected 0 missing, got %v", missing)
	}

	// 2. Missing binary
	reqs = config.Requirements{
		Binaries: []string{"non-existent-binary-xyz"},
	}
	missing = Check(reqs)
	if len(missing) != 1 {
		t.Errorf("expected 1 missing binary, got %v", missing)
	}

	// 3. Missing env
	os.Unsetenv("TEST_AGENTMUX_ENV")
	reqs = config.Requirements{
		Env: []string{"TEST_AGENTMUX_ENV"},
	}
	missing = Check(reqs)
	if len(missing) != 1 {
		t.Errorf("expected 1 missing env, got %v", missing)
	}

	// 4. Met env
	os.Setenv("TEST_AGENTMUX_ENV", "value")
	missing = Check(reqs)
	if len(missing) != 0 {
		t.Errorf("expected 0 missing env, got %v", missing)
	}
}
