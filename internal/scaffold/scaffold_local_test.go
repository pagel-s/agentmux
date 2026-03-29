package scaffold

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLocalTemplateDiscovery(t *testing.T) {
	tmpDir := t.TempDir()
	origWd, _ := os.Getwd()
	defer os.Chdir(origWd)

	// Create .agentmux/templates structure
	repoDir := filepath.Join(tmpDir, "my-repo")
	templatesDir := filepath.Join(repoDir, ".agentmux", "templates")
	if err := os.MkdirAll(templatesDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Create a local template
	localTemplatePath := filepath.Join(templatesDir, "team-custom.yaml")
	content := `version: 1
description: "A custom team template"
session: team-custom
layout:
  - window: main
`
	if err := os.WriteFile(localTemplatePath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	// Create an override template
	overridePath := filepath.Join(templatesDir, "minimal.yaml")
	overrideContent := `version: 1
description: "Overridden minimal"
session: minimal-override
`
	if err := os.WriteFile(overridePath, []byte(overrideContent), 0o644); err != nil {
		t.Fatal(err)
	}

	// Change to a subdirectory of the repo
	subDir := filepath.Join(repoDir, "src", "feature")
	if err := os.MkdirAll(subDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(subDir); err != nil {
		t.Fatal(err)
	}

	templates, err := List()
	if err != nil {
		t.Fatalf("List() failed: %v", err)
	}

	foundCustom := false
	foundMinimal := false
	for _, tmpl := range templates {
		if tmpl.Name == "team-custom" {
			foundCustom = true
			if tmpl.Description != "A custom team template" {
				t.Errorf("expected description 'A custom team template', got %q", tmpl.Description)
			}
		}
		if tmpl.Name == "minimal" {
			foundMinimal = true
			if tmpl.Description != "Overridden minimal" {
				t.Errorf("expected overridden minimal description, got %q", tmpl.Description)
			}
			if !strings.Contains(tmpl.Content, "minimal-override") {
				t.Errorf("expected overridden content, got %q", tmpl.Content)
			}
		}
	}

	if !foundCustom {
		t.Error("team-custom template not found")
	}
	if !foundMinimal {
		t.Error("minimal template override not found")
	}
}

func TestGlobalTemplateDiscovery(t *testing.T) {
	tmpHome := t.TempDir()
	origHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpHome)
	defer os.Setenv("HOME", origHome)

	// Create ~/.config/agentmux/templates structure
	globalTemplatesDir := filepath.Join(tmpHome, ".config", "agentmux", "templates")
	if err := os.MkdirAll(globalTemplatesDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Create a global template
	globalTemplatePath := filepath.Join(globalTemplatesDir, "global-standard.yaml")
	content := `version: 1
description: "A global standard template"
session: global-standard
`
	if err := os.WriteFile(globalTemplatePath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	templates, err := List()
	if err != nil {
		t.Fatalf("List() failed: %v", err)
	}

	foundGlobal := false
	for _, tmpl := range templates {
		if tmpl.Name == "global-standard" {
			foundGlobal = true
			if tmpl.Description != "A global standard template" {
				t.Errorf("expected description 'A global standard template', got %q", tmpl.Description)
			}
		}
	}

	if !foundGlobal {
		t.Error("global-standard template not found")
	}
}
