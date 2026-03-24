package scaffold

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestListIncludesCanonicalTemplates(t *testing.T) {
	templates, err := List()
	if err != nil {
		t.Fatalf("List returned error: %v", err)
	}

	if len(templates) < 5 {
		t.Fatalf("len(templates) = %d, want at least 5", len(templates))
	}

	names := make([]string, 0, len(templates))
	for _, template := range templates {
		names = append(names, template.Name)
	}

	for _, want := range []string{"minimal", "ai-dev", "on-exit", "review-loop", "ssh-ops"} {
		found := false
		for _, name := range names {
			if name == want {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("templates = %v, want %q", names, want)
		}
	}
}

func TestInitWritesTemplateAndDerivesSessionFromTarget(t *testing.T) {
	target := filepath.Join(t.TempDir(), "My Workflow.yaml")

	absPath, session, err := Init("minimal", target, InitOptions{})
	if err != nil {
		t.Fatalf("Init returned error: %v", err)
	}
	if session != "my-workflow" {
		t.Fatalf("session = %q, want %q", session, "my-workflow")
	}

	content, err := os.ReadFile(absPath)
	if err != nil {
		t.Fatalf("ReadFile returned error: %v", err)
	}
	if !strings.Contains(string(content), "session: my-workflow") {
		t.Fatalf("content = %q, want rewritten session", string(content))
	}
}

func TestInitRejectsExistingTargetWithoutForce(t *testing.T) {
	target := filepath.Join(t.TempDir(), "minimal.yaml")
	if err := os.WriteFile(target, []byte("existing"), 0o644); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}

	if _, _, err := Init("minimal", target, InitOptions{}); err == nil {
		t.Fatal("Init returned nil error for existing target without force")
	}
}
