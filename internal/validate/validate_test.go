package validate

import (
	"testing"

	"agentmux/internal/config"
)

func TestDocumentReportsMissingParamAndUnknownAgent(t *testing.T) {
	doc := &config.Document{
		Config: config.Config{
			Version: 1,
			Session: "demo",
			Layout: []config.Window{
				{
					Window: "main",
					Panes: []config.Pane{
						{Agent: "critic"},
						{Command: "echo {{project_dir}}"},
					},
				},
			},
		},
	}

	issues := Document(doc, nil)

	if !HasErrors(issues) {
		t.Fatal("HasErrors = false, want true")
	}

	got := map[string]bool{}
	for _, issue := range issues {
		got[issue.Message] = true
	}

	if !got[`pane 1 in window "main" references unknown agent "critic"`] {
		t.Fatalf("issues missing unknown agent error: %v", issues)
	}
	if !got[`missing required param "project_dir"`] {
		t.Fatalf("issues missing missing-param error: %v", issues)
	}
}

func TestDocumentRejectsInvalidGridDefinition(t *testing.T) {
	doc := &config.Document{
		Config: config.Config{
			Version: 1,
			Session: "demo",
			Layout: []config.Window{
				{
					Window:      "grid",
					Layout:      "grid",
					GridRows:    3,
					GridColumns: 3,
					Panes: []config.Pane{
						{Command: "echo 1"},
						{Command: "echo 2"},
					},
				},
			},
		},
	}

	issues := Document(doc, nil)

	if !HasErrors(issues) {
		t.Fatal("HasErrors = false, want true")
	}

	found := false
	for _, issue := range issues {
		if issue.Message == `window "grid" grid 3x3 requires exactly 9 panes, got 2` {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("issues missing grid size error: %v", issues)
	}
}

func TestDocumentCollectsParamsFromWindowEnv(t *testing.T) {
	doc := &config.Document{
		Config: config.Config{
			Version: 1,
			Session: "demo",
			Layout: []config.Window{
				{
					Window: "main",
					Env: map[string]string{
						"PROJECT_DIR": "{{project_dir}}",
					},
					Panes: []config.Pane{
						{Command: "pwd"},
					},
				},
			},
		},
	}

	issues := Document(doc, nil)

	found := false
	for _, issue := range issues {
		if issue.Message == `missing required param "project_dir"` {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("issues missing window-env param error: %v", issues)
	}
}
