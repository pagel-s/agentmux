package validate

import (
	"path/filepath"
	"strings"
	"testing"

	"agentmux/internal/config"
)

func TestValidationFixtures(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name           string
		path           string
		params         map[string]string
		wantErrors     []string
		wantWarnings   []string
		wantNoErrors   bool
		wantNoWarnings bool
	}{
		{
			name:           "valid minimal",
			path:           fixturePath("valid", "minimal.yaml"),
			wantNoErrors:   true,
			wantNoWarnings: true,
		},
		{
			name:           "valid env overrides",
			path:           fixturePath("valid", "env-overrides.yaml"),
			params:         map[string]string{"project_dir": "/tmp/app"},
			wantNoErrors:   true,
			wantNoWarnings: true,
		},
		{
			name:       "missing param",
			path:       fixturePath("invalid", "missing-param.yaml"),
			wantErrors: []string{`missing required param "project_dir"`},
		},
		{
			name:       "unknown agent",
			path:       fixturePath("invalid", "unknown-agent.yaml"),
			wantErrors: []string{`references unknown agent "ghost"`},
		},
		{
			name:       "both agent and command",
			path:       fixturePath("invalid", "both-agent-and-command.yaml"),
			wantErrors: []string{`specifies multiple sources`},
		},
		{
			name:         "warning fixture",
			path:         fixturePath("warning", "unknown-top-level.yaml"),
			wantWarnings: []string{`unknown top-level field "future_field"`},
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			doc, err := config.LoadFile(tc.path)
			if err != nil {
				t.Fatalf("LoadFile returned error: %v", err)
			}

			issues := Document(doc, tc.params)
			errorMessages := collectMessages(issues, SeverityError)
			warningMessages := collectMessages(issues, SeverityWarning)

			if tc.wantNoErrors && len(errorMessages) != 0 {
				t.Fatalf("expected no errors, got %v", errorMessages)
			}
			if tc.wantNoWarnings && len(warningMessages) != 0 {
				t.Fatalf("expected no warnings, got %v", warningMessages)
			}

			for _, want := range tc.wantErrors {
				if !containsSubstring(errorMessages, want) {
					t.Fatalf("expected error containing %q, got %v", want, errorMessages)
				}
			}
			for _, want := range tc.wantWarnings {
				if !containsSubstring(warningMessages, want) {
					t.Fatalf("expected warning containing %q, got %v", want, warningMessages)
				}
			}
		})
	}
}

func fixturePath(parts ...string) string {
	all := append([]string{"..", "..", "testdata", "fixtures"}, parts...)
	return filepath.Join(all...)
}

func collectMessages(issues []Issue, severity Severity) []string {
	var messages []string
	for _, issue := range issues {
		if issue.Severity == severity {
			messages = append(messages, issue.Message)
		}
	}
	return messages
}

func containsSubstring(messages []string, want string) bool {
	for _, message := range messages {
		if strings.Contains(message, want) {
			return true
		}
	}
	return false
}
