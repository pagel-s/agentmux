package resolve

import (
	"agentmux/internal/config"
	"path/filepath"
	"testing"
)

func TestProjectResolvesConfigDir(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "my-session.yaml")
	
	cfg := &config.Config{
		Version: 1,
		Session: "test",
		Layout: []config.Window{
			{
				Window: "main",
				Panes: []config.Pane{
					{
						Command: "{{config_dir}}/myscript.sh",
						Cwd:     "{{config_dir}}/data",
					},
				},
			},
		},
	}

	doc := &config.Document{
		Path:   configPath,
		Config: *cfg,
	}

	resolved, err := Project(doc, nil)
	if err != nil {
		t.Fatalf("Project returned error: %v", err)
	}

	pane := resolved.Windows[0].Panes[0]
	expectedCmd := tmpDir + "/myscript.sh"
	expectedCwd := tmpDir + "/data"

	if pane.Command != expectedCmd {
		t.Errorf("pane.Command = %q, want %q", pane.Command, expectedCmd)
	}
	if pane.Cwd != expectedCwd {
		t.Errorf("pane.Cwd = %q, want %q", pane.Cwd, expectedCwd)
	}
}
