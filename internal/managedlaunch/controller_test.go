package managedlaunch

import (
	"bytes"
	"os"
	"path/filepath"
	"sync"
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

func TestConcurrentObserverPinsPublishCompleteImmutableCopy(t *testing.T) {
	root := t.TempDir()
	executable := filepath.Join(root, "current-observer")
	data := bytes.Repeat([]byte("current owner observer binary\n"), 65536)
	if err := os.WriteFile(executable, data, 0700); err != nil {
		t.Fatal(err)
	}
	state := filepath.Join(root, "state")
	var workers sync.WaitGroup
	results := make(chan string, 12)
	errors := make(chan error, 12)
	start := make(chan struct{})
	for i := 0; i < cap(results); i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			observer := &Launcher{options: Options{Executable: executable, StateDir: state}}
			if err := observer.pinController(managedconfig.Config{}); err != nil {
				errors <- err
				return
			}
			results <- observer.options.Executable
		}()
	}
	close(start)
	workers.Wait()
	close(errors)
	close(results)
	for err := range errors {
		t.Fatalf("simultaneous attachment saw an incomplete or conflicting UI copy: %v", err)
	}
	pinned := ""
	for path := range results {
		if pinned != "" && pinned != path {
			t.Fatalf("same observer produced distinct identities: %q and %q", pinned, path)
		}
		pinned = path
	}
	if err := os.WriteFile(executable, []byte("replacement in writable checkout"), 0700); err != nil {
		t.Fatal(err)
	}
	saved, err := os.ReadFile(pinned)
	if err != nil || !bytes.Equal(saved, data) {
		t.Fatalf("pinned observer changed with its writable source: length=%d err=%v", len(saved), err)
	}
	entries, err := os.ReadDir(state + ".bin")
	if err != nil || len(entries) != 1 || entries[0].Name() != filepath.Base(pinned) {
		t.Fatalf("partial pin files or extra publications remain: %+v err=%v", entries, err)
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
