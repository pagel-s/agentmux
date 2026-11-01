package managedlaunch

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestWorkspaceIndexPreservesConcurrentCustomLocations(t *testing.T) {
	index := filepath.Join(t.TempDir(), "registry.json")
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if err := RecordWorkspace(index, filepath.Join("/tmp", fmt.Sprint("workspace-", i))); err != nil {
				t.Error(err)
			}
		}(i)
	}
	wg.Wait()
	data, err := os.ReadFile(index)
	if err != nil {
		t.Fatal(err)
	}
	var paths []string
	if err := json.Unmarshal(data, &paths); err != nil {
		t.Fatal(err)
	}
	if len(paths) != 12 {
		t.Fatalf("lost workspace registrations: %s", data)
	}
	if err := RecordWorkspace(index, paths[0]); err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(index)
	if info.Mode().Perm()&0077 != 0 {
		t.Fatal("index exposed")
	}
	link := filepath.Join(filepath.Dir(index), "linked.json")
	if err := os.Symlink(index, link); err != nil {
		t.Fatal(err)
	}
	if err := RecordWorkspace(link, "/tmp/another"); err == nil {
		t.Fatal("followed index symlink")
	}
}
