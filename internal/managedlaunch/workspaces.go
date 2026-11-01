package managedlaunch

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// RecordWorkspace keeps a bounded private inventory of custom state locations.
// Advisory locking plus atomic replacement preserves concurrent starts.
func RecordWorkspace(index, state string) error {
	if err := os.MkdirAll(filepath.Dir(index), 0700); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var unlock func()
	var err error
	for {
		unlock, err = lock(index + ".lock")
		if err == nil {
			break
		}
		select {
		case <-ctx.Done():
			return errors.New("workspace index is busy; use workspaces --register DIR to remember this path")
		case <-time.After(10 * time.Millisecond):
		}
	}
	defer unlock()
	var paths []string
	data, err := readPrivateFile(index)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if err == nil {
		if err := json.Unmarshal(data, &paths); err != nil {
			return err
		}
	}
	state, err = filepath.Abs(state)
	if err != nil {
		return err
	}
	if canonical, e := filepath.EvalSymlinks(state); e == nil {
		state = canonical
	}
	for _, p := range paths {
		if p == state {
			return nil
		}
	}
	if len(paths) >= 4096 {
		return errors.New("workspace index limit reached")
	}
	paths = append(paths, state)
	sort.Strings(paths)
	return writePrivateJSON(index, paths)
}
