package harness

import (
	"strings"
	"testing"
)

func TestBuildValidationDoesNotReflectMetadata(t *testing.T) {
	const secret = "native_validation_secret_972333"
	t.Setenv(secret, "")
	for _, tc := range []struct {
		name, category string
		request        Request
	}{
		{"harness", "unsupported managed harness", Request{Harness: secret}},
		{"environment-key", "valid names", Request{Harness: "codex", Env: map[string]string{"invalid-" + secret: "safe"}}},
		{"environment-value", "without NUL", Request{Harness: "codex", Env: map[string]string{secret: "invalid\x00"}}},
		{"missing-credential", "credential environment variable is not set", Request{Harness: "codex", APIKeyEnv: secret}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Build(tc.request)
			if err == nil || strings.Contains(err.Error(), secret) || !strings.Contains(err.Error(), tc.category) {
				t.Fatalf("expected private %q diagnostic, got %v", tc.category, err)
			}
		})
	}
}
