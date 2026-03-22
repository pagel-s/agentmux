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
