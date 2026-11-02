//go:build unix

package workflow

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestSyncDirectoryRefusesReplacedDirectoryWithoutBlocking(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "selected-directory")
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	if err := syncDirectory(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := unix.Mkfifo(path, 0600); err != nil {
		t.Fatal(err)
	}
	finished := make(chan error, 1)
	go func() { finished <- syncDirectory(path) }()
	select {
	case err := <-finished:
		if err == nil {
			t.Fatal("replacement FIFO accepted as a durable directory")
		}
	case <-time.After(time.Second):
		// Release an accidentally blocking implementation before failing so
		// the regression does not leave a leaked test goroutine.
		if fd, err := unix.Open(path, unix.O_WRONLY|unix.O_NONBLOCK, 0); err == nil {
			unix.Close(fd)
		}
		t.Fatal("directory sync blocked opening a replacement FIFO")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(root, path); err != nil {
		t.Fatal(err)
	}
	if err := syncDirectory(path); err == nil {
		t.Fatal("directory sync followed a replacement symlink")
	}
}

func TestDurableMkdirRefusesSymlinkAndRelativeTarget(t *testing.T) {
	root := t.TempDir()
	link := filepath.Join(root, "link")
	if err := os.Symlink(root, link); err != nil {
		t.Fatal(err)
	}
	if err := mkdirAllDurable(filepath.Join(link, "new"), 0700); err == nil {
		t.Fatal("directory creation followed a symlink ancestor")
	}
	if err := mkdirAllDurable("relative-new-directory", 0700); err == nil {
		t.Fatal("relative durable directory target accepted")
	}
	path := filepath.Join(root, "nested", "private", "catalog")
	if err := mkdirAllDurable(path, 0700); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(path); err != nil || !info.IsDir() || info.Mode().Perm() != 0700 {
		t.Fatalf("private durable hierarchy: %v %v", info, err)
	}
}
