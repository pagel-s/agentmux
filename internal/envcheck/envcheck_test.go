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
		Params:   []string{"p1"},
	}
	report := Check(reqs, map[string]string{"p1": "v1"})
	if report.HasMissing() {
		t.Errorf("expected 0 missing, got %+v", report)
	}

	// 2. Missing binary
	reqs = config.Requirements{
		Binaries: []string{"non-existent-binary-xyz"},
	}
	report = Check(reqs, nil)
	if len(report.MissingBinaries) != 1 {
		t.Errorf("expected 1 missing binary, got %v", report.MissingBinaries)
	}

	// 3. Missing env
	os.Unsetenv("TEST_AGENTMUX_ENV")
	reqs = config.Requirements{
		Env: []string{"TEST_AGENTMUX_ENV"},
	}
	report = Check(reqs, nil)
	if len(report.MissingEnv) != 1 {
		t.Errorf("expected 1 missing env, got %v", report.MissingEnv)
	}

	// 4. Missing param
	reqs = config.Requirements{
		Params: []string{"missing_p"},
	}
	report = Check(reqs, nil)
	if len(report.MissingParams) != 1 {
		t.Errorf("expected 1 missing param, got %v", report.MissingParams)
	}
}
