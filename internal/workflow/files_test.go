package workflow

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadRegularFileRejectsSymlinksAndOversize(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "input")
	fixtureWrite(t, file, []byte("input"))
	for _, limit := range []int64{0, -1, 33 << 20} {
		if _, err := ReadRegularFile(file, limit); err == nil {
			t.Fatalf("accepted invalid bound %d", limit)
		}
	}
	if _, err := ReadRegularFile(file, 4); err == nil {
		t.Fatal("accepted oversize")
	}
	if got, err := ReadRegularFile(file, 5); err != nil || string(got) != "input" {
		t.Fatalf("valid file: %q %v", got, err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(file, link); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadRegularFile(link, 5); err == nil {
		t.Fatal("followed symlink")
	}
	if _, err := ReadRegularFile(dir, 100); err == nil {
		t.Fatal("accepted directory")
	}
}
