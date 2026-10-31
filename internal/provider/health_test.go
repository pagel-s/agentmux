package provider

import (
	"errors"
	"strings"
	"testing"
)

func TestCheckCLIReportsMissingBinary(t *testing.T) {
	report := checkCLIWith("claude", func(string) (string, error) {
		return "", errors.New("missing")
	}, func(string, ...string) (string, error) {
		t.Fatal("run should not be called when binary lookup fails")
		return "", nil
	})

	if len(report.Issues) != 1 || report.Issues[0].Severity != "error" {
		t.Fatalf("report.Issues = %#v, want missing-binary error", report.Issues)
	}
}

func TestCheckAntigravityCLIRequiresOnlyBridgeFlags(t *testing.T) {
	report := checkCLIWith("agy", func(name string) (string, error) {
		if name != "agy" {
			t.Fatalf("binary = %q, want agy", name)
		}
		return "/tmp/agy", nil
	}, func(string, ...string) (string, error) {
		return "--prompt-interactive\n--model\n--effort\n", nil
	})
	if report.HasErrors() || len(report.Issues) != 0 {
		t.Fatalf("report.Issues = %#v, want healthy CLI bridge", report.Issues)
	}
	if report.Launch.Mode != string(LaunchModeCLIBridge) {
		t.Fatalf("report.Launch.Mode = %q, want CLI bridge", report.Launch.Mode)
	}
}

func TestCheckCLIReportsMissingRequiredHelpToken(t *testing.T) {
	report := checkCLIWith("gemini", func(string) (string, error) {
		return "/tmp/gemini", nil
	}, func(string, ...string) (string, error) {
		return "--model\n--prompt-interactive\n", nil
	})

	found := false
	for _, issue := range report.Issues {
		if issue.Severity == "error" && strings.Contains(issue.Message, "--allowed-mcp-server-names") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("report.Issues = %#v, want missing --allowed-mcp-server-names error", report.Issues)
	}
}

func TestCheckCLIReportsNativeFallbackWarning(t *testing.T) {
	report := checkCLIWith("claude", func(string) (string, error) {
		return "/tmp/claude", nil
	}, func(string, ...string) (string, error) {
		return "--append-system-prompt\n--model\n--effort\n", nil
	})

	if report.Launch.Mode != string(LaunchModeCLIBridge) {
		t.Fatalf("report.Launch.Mode = %q, want %q", report.Launch.Mode, LaunchModeCLIBridge)
	}

	found := false
	for _, issue := range report.Issues {
		if issue.Severity == "warning" && strings.Contains(issue.Message, "falling back to CLI bridge") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("report.Issues = %#v, want native fallback warning", report.Issues)
	}
}

func TestModelWarning(t *testing.T) {
	tests := []struct {
		name  string
		model string
		want  bool
	}{
		{name: "claude", model: "claude-3-5-sonnet", want: true},
		{name: "gemini", model: "gemini-1.5-pro", want: true},
		{name: "codex", model: "gpt-4o", want: true},
		{name: "codex", model: "gpt-5-codex", want: false},
	}

	for _, tc := range tests {
		t.Run(tc.name+"-"+tc.model, func(t *testing.T) {
			got := ModelWarning(tc.name, tc.model) != ""
			if got != tc.want {
				t.Fatalf("ModelWarning(%q, %q) presence = %v, want %v", tc.name, tc.model, got, tc.want)
			}
		})
	}
}
