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

	if len(templates) < 7 {
		t.Fatalf("len(templates) = %d, want at least 7", len(templates))
	}

	names := make([]string, 0, len(templates))
	for _, template := range templates {
		names = append(names, template.Name)
	}

	for _, want := range []string{"minimal", "ai-dev", "coding-starter", "on-exit", "review-loop", "review-starter", "ssh-ops"} {
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

func TestLookupSupportsDefaultAlias(t *testing.T) {
	template, ok, err := Lookup("default")
	if err != nil {
		t.Fatalf("Lookup returned error: %v", err)
	}
	if !ok {
		t.Fatal("Lookup(default) reported ok=false")
	}
	if template.Name != "ai-dev" {
		t.Fatalf("template.Name = %q, want %q", template.Name, "ai-dev")
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
