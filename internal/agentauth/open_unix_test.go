//go:build unix

package agentauth

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestCredentialOpeningRefusesLinksAndCannotBlockOnFIFO(t *testing.T) {
	root := t.TempDir()
	regular := filepath.Join(root, "selected")
	if err := os.WriteFile(regular, []byte(`{"OPENAI_API_KEY":"fixture-only"}`), 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(regular, link); err != nil {
		t.Fatal(err)
	}
	fifo := filepath.Join(root, "fifo")
	if err := unix.Mkfifo(fifo, 0600); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{link, fifo} {
		done := make(chan error, 1)
		go func() {
			f, err := openCredentialFile(path)
			if f != nil {
				f.Close()
			}
			done <- err
		}()
		select {
		case err := <-done:
			if err == nil {
				t.Fatal("unsafe credential source was opened")
			}
		case <-time.After(time.Second):
			t.Fatal("credential selection blocked on a special file")
		}
	}
}
