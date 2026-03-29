package validate

import (
	"agentmux/internal/config"
	"testing"
)

func TestValidateRequiresParams(t *testing.T) {
	doc := &config.Document{
		Config: config.Config{
			Version: 1,
			Session: "test",
			Requires: config.Requirements{
				Params: []string{"mandatory_param"},
			},
			Layout: []config.Window{
				{Window: "w1", Panes: []config.Pane{{Command: "sleep 1"}}},
			},
		},
	}

	// 1. Missing param
	issues := Document(doc, nil)
	found := false
	for _, issue := range issues {
		if issue.Message == "missing required param \"mandatory_param\"" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected error for missing required param")
	}

	// 2. Met param
	issues = Document(doc, map[string]string{"mandatory_param": "val"})
	for _, issue := range issues {
		if issue.Message == "missing required param \"mandatory_param\"" {
			t.Error("unexpected error for provided required param")
		}
	}
}
