package managedlaunch

import (
	"os"
	"path/filepath"
	"testing"

	"agentmux/internal/managedconfig"
)

func TestControllerIsPinnedOutsideWritableProject(t *testing.T) {
	root := t.TempDir()
	project := filepath.Join(root, "project")
	if err := os.Mkdir(project, 0700); err != nil {
		t.Fatal(err)
	}
	executable := filepath.Join(project, "agentmux")
	if err := os.WriteFile(executable, []byte("original controller"), 0700); err != nil {
		t.Fatal(err)
	}
	cfg := managedconfig.Config{Agents: map[string]managedconfig.Agent{"alice": {Workdir: project}}}
	l := &Launcher{options: Options{Executable: executable, StateDir: filepath.Join(root, "state")}}
	if err := l.pinController(cfg); err != nil {
		t.Fatal(err)
	}
	pinned := l.options.Executable
	if pinned == executable {
		t.Fatal("controller remains writable by project agent")
	}
	if err := os.WriteFile(executable, []byte("agent replacement"), 0700); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(pinned)
	if err != nil || string(data) != "original controller" {
		t.Fatalf("pinned executable changed: %q %v", data, err)
	}
	// A saved copy with the right name but altered bytes must not be trusted.
	if err := os.WriteFile(executable, []byte("original controller"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pinned, []byte("tampered"), 0700); err != nil {
		t.Fatal(err)
	}
	l.options.Executable = executable
	if err := l.pinController(cfg); err == nil {
		t.Fatal("accepted a tampered pinned controller")
	}
	l.options.StateDir = filepath.Join(project, "state")
	if err := l.pinController(cfg); err == nil {
		t.Fatal("accepted a controller copy inside the writable project")
	}
}

func TestControllerIsPinnedBeforeUnrelatedWorkspaceCanGrowToExposeSource(t *testing.T) {
	root := t.TempDir()
	project := filepath.Join(root, "first-project")
	source := filepath.Join(root, "agentmux-source")
	for _, dir := range []string{project, source} {
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	executable := filepath.Join(source, "agentmux")
	const original = "controller before workspace expansion"
	if err := os.WriteFile(executable, []byte(original), 0700); err != nil {
		t.Fatal(err)
	}
	cfg := managedconfig.Config{Agents: map[string]managedconfig.Agent{"daily": {Workdir: project}}}
	l := &Launcher{options: Options{Executable: executable, StateDir: filepath.Join(root, "private-state")}}
	if err := l.pinController(cfg); err != nil {
		t.Fatal(err)
	}
	pinned := l.options.Executable
	if pinned == executable || filepath.Dir(pinned) != l.options.StateDir+".bin" {
		t.Fatalf("initially unrelated project left controller unpinned: %s", pinned)
	}
	// Adding Agentmux's own project later must not change the executable used
	// by the existing supervisor, even if a project worker replaces the build.
	cfg.Agents["agentmux-development"] = managedconfig.Agent{Workdir: source}
	if err := os.WriteFile(executable, []byte("replacement from later writable project"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := l.pinController(cfg); err != nil {
		t.Fatal(err)
	}
	if l.options.Executable != pinned {
		t.Fatal("workspace expansion replaced the pinned controller")
	}
	data, err := os.ReadFile(l.options.Executable)
	if err != nil || string(data) != original {
		t.Fatalf("later project influenced controller: %q %v", data, err)
	}
}
