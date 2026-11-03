//go:build linux

package agentauth

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"agentmux/internal/managedconfig"
	"golang.org/x/sys/unix"
)

func TestAnonymousAuthenticationIsFilteredSealedAndHasNoDiskCopy(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "login.json")
	if err := os.WriteFile(source, []byte(`{"OPENAI_API_KEY":"selected-native-credential-fixture","unrelated_owner_data":"never-export"}`), 0600); err != nil {
		t.Fatal(err)
	}
	prepared, err := PrepareAnonymous("codex", managedconfig.NativeAuth{File: source})
	if err != nil {
		t.Fatal(err)
	}
	defer prepared.Close()
	if prepared.dir != "" || len(prepared.Files) != 1 || prepared.Files[0].Descriptor == nil {
		t.Fatal("anonymous authentication created a pathname-backed copy")
	}
	file := prepared.Files[0]
	data, err := os.ReadFile(file.Source)
	if err != nil || !bytes.Contains(data, []byte("selected-native-credential-fixture")) || bytes.Contains(data, []byte("never-export")) {
		t.Fatalf("filtered anonymous source unavailable or overbroad: %v", err)
	}
	seals, err := unix.FcntlInt(file.Descriptor.Fd(), unix.F_GET_SEALS, 0)
	if err != nil || seals&(unix.F_SEAL_WRITE|unix.F_SEAL_GROW|unix.F_SEAL_SHRINK|unix.F_SEAL_SEAL) != (unix.F_SEAL_WRITE|unix.F_SEAL_GROW|unix.F_SEAL_SHRINK|unix.F_SEAL_SEAL) {
		t.Fatal("credential is not permanently sealed")
	}
	if _, err := file.Descriptor.WriteAt([]byte("mutate"), 0); err == nil {
		t.Fatal("selected login memory remained writable")
	}
	prepared.Close()
	if _, err := os.Stat(file.Source); !os.IsNotExist(err) {
		t.Fatal("closed anonymous credential remained addressable")
	}
	files, err := os.ReadDir(root)
	if err != nil || len(files) != 1 || files[0].Name() != "login.json" {
		t.Fatal("anonymous credential left a file in its source directory")
	}
}
