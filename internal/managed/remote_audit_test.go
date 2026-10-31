package managed

import (
	"archive/tar"
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"agentmux/internal/managedconfig"
)

func remotePayload(t *testing.T, definitionBytes int) []byte {
	t.Helper()
	var out bytes.Buffer
	tw := tar.NewWriter(&out)
	for _, f := range []struct {
		name string
		data []byte
	}{{"agentmux", []byte("own-binary")}, {"worker.json", bytes.Repeat([]byte{'x'}, definitionBytes)}, {"credential.json", []byte(`{"agent_id":"alice","token":"own-test-token"}`)}} {
		if err := tw.WriteHeader(&tar.Header{Name: f.name, Size: int64(len(f.data)), Mode: 0600, Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(f.data); err != nil {
			t.Fatal(err)
		}
	}
	tw.Close()
	return out.Bytes()
}
func pythonInstall(t *testing.T, root, mode string, payload []byte) (string, error) {
	t.Helper()
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		t.Skip("remote deployment targets Linux amd64")
	}
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not installed")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, python, "-c", remoteInstall, root, "alice", mode)
	cmd.Stdin = bytes.NewReader(payload)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func TestRemoteInstallPerArtifactBoundsAndPrivateResumeLock(t *testing.T) {
	parent, err := os.MkdirTemp("", "amx-remote-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(parent) })
	root := filepath.Join(parent, "deployment")
	payload := remotePayload(t, 16)
	if out, err := pythonInstall(t, root, "new", payload); err != nil {
		t.Fatalf("private deployment failed at %s: %v: %s", root, err, out)
	}
	for _, name := range []string{"agentmux", "worker.json", "state/credentials/alice.json"} {
		info, err := os.Stat(filepath.Join(root, name))
		if err != nil || info.Mode().Perm()&0077 != 0 {
			t.Fatal("deployment artifact is missing or public")
		}
	}
	if _, err := os.Stat(filepath.Join(root, "state", "managed.sqlite")); !os.IsNotExist(err) {
		t.Fatal("deployment copied controller database")
	}
	unlock, err := acquireLock(filepath.Join(root, "state", "worker-alice.lock"))
	if err != nil {
		t.Fatal(err)
	}
	if out, err := pythonInstall(t, root, "resume", payload); err == nil || !strings.Contains(out, "active") {
		unlock()
		t.Fatal("active worker deployment resumed")
	}
	unlock()
	if out, err := pythonInstall(t, root, "resume", payload); err != nil {
		t.Fatalf("identical resume failed: %v: %s", err, out)
	}
	path := filepath.Join(root, "worker.json")
	os.WriteFile(path, []byte("changed-definition"), 0600)
	if _, err := pythonInstall(t, root, "resume", payload); err == nil {
		t.Fatal("changed artifact was overwritten")
	}
	if b, _ := os.ReadFile(path); string(b) != "changed-definition" {
		t.Fatal("resume overwrote changed definition")
	}
	oversized := filepath.Join(parent, "oversized")
	if _, err := pythonInstall(t, oversized, "new", remotePayload(t, (1<<20)+1)); err == nil {
		t.Fatal("oversized worker definition accepted")
	}
	if _, err := os.Stat(filepath.Join(oversized, "worker.json")); !os.IsNotExist(err) {
		t.Fatal("oversized definition written before rejection")
	}
}

func TestRemoteInstallRejectsUnsafeSharedAncestor(t *testing.T) {
	base, err := os.MkdirTemp("", "amx-remote-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(base) })
	parent := filepath.Join(base, "shared")
	os.Mkdir(parent, 0700)
	os.Chmod(parent, 0777)
	if out, err := pythonInstall(t, filepath.Join(parent, "deployment"), "new", remotePayload(t, 16)); err == nil || !strings.Contains(out, "ancestors") {
		t.Fatal("unsafe non-sticky shared parent accepted")
	}
}

func TestRemoteShellQuotingPreservesLiteralWords(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("POSIX shell not installed")
	}
	for _, word := range []string{"a'b", "$(printf injected)", "space ; & | literal", "line\nsecond"} {
		cmd := exec.Command(sh, "-c", "printf %s "+shellWord(word))
		out, err := cmd.Output()
		if err != nil || string(out) != word {
			t.Fatal("remote command word was evaluated or changed")
		}
	}
}

func TestRemoteDefinitionIncludesOnlyLiveDelegatedNamespaces(t *testing.T) {
	c := dynamicConfig()
	template := c.Spawn.Templates["echo"]
	template.Autonomy = managedconfig.Autonomy{Enabled: true, Boards: []string{"team", "*"}}
	c.Spawn.Templates["echo"] = template
	s := testStore(t, c)
	if err := s.startRuntime(context.Background()); err != nil {
		t.Fatal(err)
	}
	child := spawned(t, s, "alice", Capabilities{BoardsRead: []string{"team"}, MemoryRead: []string{"team"}})
	d, err := s.WorkerDefinition(child.ID, RemoteLayout{Workdir: "/tmp/remote/state/workspaces/" + child.ID})
	if err != nil {
		t.Fatal(err)
	}
	if !d.Config.CanReadBoard(child.ID, "team") || d.Config.CanWriteBoard(child.ID, "team") || !d.Config.CanReadMemory(child.ID, "team") {
		t.Fatal("delegated remote namespace snapshot wrong")
	}
	if len(d.Config.Boards) != 1 || len(d.Config.Agents) != 1 {
		t.Fatal("ambient peer/wildcard namespace leaked to child")
	}
	if len(d.Config.Agents[child.ID].Autonomy.Boards) != 2 {
		t.Fatal("owner observation policy changed during placement")
	}
}
