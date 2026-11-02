//go:build unix

package workflow

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestRegularFileOpenerDoesNotBlockOnFIFOOrFollowRacingLink(t *testing.T) {
	dir := t.TempDir()
	fifo := filepath.Join(dir, "fifo")
	if err := unix.Mkfifo(fifo, 0600); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		f, err := openRegularFile(fifo)
		if f != nil {
			f.Close()
		}
		done <- err
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("FIFO open blocked")
	}
	if _, err := ReadRegularFile(fifo, 1024); err == nil {
		t.Fatal("accepted FIFO")
	}
	regular := filepath.Join(dir, "regular")
	fixtureWrite(t, regular, []byte("private"))
	link := filepath.Join(dir, "link")
	if err := os.Symlink(regular, link); err != nil {
		t.Fatal(err)
	}
	if f, err := openRegularFile(link); err == nil {
		f.Close()
		t.Fatal("nofollow opener followed symlink")
	}
}

func TestSelectedRegularFileReplacementCannotBlockFingerprint(t *testing.T) {
	for _, replacement := range []string{"fifo", "symlink", "regular"} {
		t.Run(replacement, func(t *testing.T) {
			dir := t.TempDir()
			selected := filepath.Join(dir, "selected")
			fixtureWrite(t, selected, []byte("selected content"))
			before, err := os.Lstat(selected)
			if err != nil {
				t.Fatal(err)
			}
			// Retain the selected inode so a replacement cannot reuse it.
			if err = os.Rename(selected, filepath.Join(dir, "original")); err != nil {
				t.Fatal(err)
			}
			switch replacement {
			case "fifo":
				err = unix.Mkfifo(selected, 0600)
			case "symlink":
				err = os.Symlink(filepath.Join(dir, "original"), selected)
			case "regular":
				fixtureWrite(t, selected, []byte("different content"))
			}
			if err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() {
				f, openErr := openRegularFileMatching(selected, before)
				if f != nil {
					f.Close()
				}
				done <- openErr
			}()
			select {
			case err := <-done:
				if err == nil {
					t.Fatal("accepted replacement of the selected file")
				}
			case <-time.After(time.Second):
				t.Fatal("fingerprint file opener blocked on a replacement")
			}
		})
	}
}

func TestSelectedRegularFileRejectsInPlaceChangeBeforeOpen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "selected")
	fixtureWrite(t, path, []byte("before"))
	before, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	fixtureWrite(t, path, []byte("a changed size"))
	if f, err := openRegularFileMatching(path, before); err == nil {
		f.Close()
		t.Fatal("accepted an in-place change after selection")
	}
}
