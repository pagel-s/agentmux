package capsule

import (
	"archive/tar"
	"bytes"
	"context"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"agentmux/internal/harness"
	"agentmux/internal/sandbox"
)

func privateDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	return dir
}
func fixture(t *testing.T, ctx context.Context) (*Capsule, harness.Spec, sandbox.Options) {
	t.Helper()
	state, err := os.MkdirTemp("", "amx-cap-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(state) })
	workspace := filepath.Join(state, "workspaces", "agent")
	if err := os.MkdirAll(workspace, 0700); err != nil {
		t.Fatal(err)
	}
	credential := filepath.Join(state, "credential.json")
	if err := os.WriteFile(credential, []byte("private"), 0600); err != nil {
		t.Fatal(err)
	}
	socket := filepath.Join(state, "broker.sock")
	l, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	c, err := Prepare(ctx, Options{StateDir: state, AgentID: "agent", Workspace: workspace, Executable: "/usr/bin/true", WorkspaceBytes: 1 << 20, HistoryBytes: 1 << 20, TempBytes: 1 << 20, MaxEntries: 100})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c, harness.Spec{Name: "command", Binary: "/usr/bin/true", Workdir: workspace, Env: []string{"PATH=/usr/bin:/bin"}}, sandbox.Options{Mode: "bubblewrap", Network: "none", Workspace: workspace, StateDir: state, CredentialFile: credential, SocketPath: socket}
}

func sendCheckpoint(t *testing.T, c *Capsule, outputs []volume) error {
	t.Helper()
	conn, err := net.Dial("unix", filepath.Join(c.dir, "checkpoint.sock"))
	if err != nil {
		return err
	}
	defer conn.Close()
	if err = writeArchive(conn, outputs, c.maxEntries); err != nil {
		return err
	}
	if _, err = io.WriteString(conn, checkpointEnd); err != nil {
		return err
	}
	conn.(*net.UnixConn).CloseWrite()
	ack := make([]byte, 3)
	_, err = io.ReadFull(conn, ack)
	if err == nil && string(ack) != "OK\n" {
		return io.ErrUnexpectedEOF
	}
	return err
}

func TestCapsuleCappedMountsAndSuccessfulCheckpoint(t *testing.T) {
	c, spec, opts := fixture(t, context.Background())
	if err := os.WriteFile(filepath.Join(c.opts.Workspace, "before"), []byte("old"), 0600); err != nil {
		t.Fatal(err)
	}
	wrapped, err := c.Wrap(spec, opts)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(wrapped.Args, " ")
	if strings.Count(joined, "--size 1048576 --tmpfs") != 3 || strings.Contains(joined, "--bind "+c.opts.Workspace+" /workspace") || !strings.Contains(joined, "internal-capsule --manifest") {
		t.Fatalf("uncapped host storage in spec: %s", joined)
	}
	output := privateDir(t)
	os.WriteFile(filepath.Join(output, "after"), []byte("new"), 0600)
	if err = sendCheckpoint(t, c, []volume{{Name: "workspace", Source: output, Bytes: 1 << 20}}); err != nil {
		t.Fatal(err)
	}
	if err = c.Finish(); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(filepath.Join(c.opts.Workspace, "after")); err != nil || string(b) != "new" {
		t.Fatal("new snapshot unavailable")
	}
	if _, err = os.Stat(filepath.Join(c.opts.Workspace, "before")); !os.IsNotExist(err) {
		t.Fatal("old snapshot was merged instead of replaced")
	}
	if err = c.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(c.dir); !os.IsNotExist(err) {
		t.Fatal("successful capsule left staging")
	}
}

func TestCapsuleCASConflictPreservesOwnerEditsAndQuarantines(t *testing.T) {
	c, spec, opts := fixture(t, context.Background())
	os.WriteFile(filepath.Join(c.opts.Workspace, "file"), []byte("before"), 0600)
	if _, err := c.Wrap(spec, opts); err != nil {
		t.Fatal(err)
	}
	output := privateDir(t)
	os.WriteFile(filepath.Join(output, "file"), []byte("model"), 0600)
	if err := sendCheckpoint(t, c, []volume{{Name: "workspace", Source: output, Bytes: 1 << 20}}); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(c.opts.Workspace, "file"), []byte("owner edit"), 0600)
	if err := c.Finish(); err == nil {
		t.Fatal("external edit overwritten")
	}
	c.Close()
	if b, _ := os.ReadFile(filepath.Join(c.opts.Workspace, "file")); string(b) != "owner edit" {
		t.Fatal("owner edit clobbered")
	}
	if _, err := Prepare(context.Background(), c.opts); err == nil {
		t.Fatal("quarantine allowed repeated allocation")
	}
}

func TestCapsuleCancellationPreservesPriorSnapshot(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	c, spec, opts := fixture(t, ctx)
	os.WriteFile(filepath.Join(c.opts.Workspace, "old"), []byte("retained"), 0600)
	if _, err := c.Wrap(spec, opts); err != nil {
		t.Fatal(err)
	}
	cancel()
	if err := c.Finish(); err == nil {
		t.Fatal("cancelled run published")
	}
	c.Close()
	if b, _ := os.ReadFile(filepath.Join(c.opts.Workspace, "old")); string(b) != "retained" {
		t.Fatal("cancelled turn changed old workspace")
	}
}

func TestCapsuleRejectsExternalSharedAndLinkedInputs(t *testing.T) {
	c, _, _ := fixture(t, context.Background())
	bad := c.opts
	bad.Workspace = privateDir(t)
	if Validate(bad) == nil {
		t.Fatal("external workdir accepted")
	}
	outside := filepath.Join(privateDir(t), "outside")
	os.WriteFile(outside, []byte("owner private"), 0600)
	os.Symlink(outside, filepath.Join(c.opts.Workspace, "link"))
	if _, err := inspect(c.opts.Workspace, 1<<20, 100); err == nil {
		t.Fatal("symlink input accepted")
	}
	os.Remove(filepath.Join(c.opts.Workspace, "link"))
	if err := os.Link(outside, filepath.Join(c.opts.Workspace, "hardlink")); err != nil {
		t.Fatal(err)
	}
	if _, err := inspect(c.opts.Workspace, 1<<20, 100); err == nil {
		t.Fatal("hardlink input accepted")
	}
}

func archiveBytes(t *testing.T, headers []*tar.Header) []byte {
	t.Helper()
	var out bytes.Buffer
	tw := tar.NewWriter(&out)
	for _, h := range headers {
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if h.Size > 0 {
			tw.Write(bytes.Repeat([]byte{'x'}, int(h.Size)))
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}
func TestArchiveAdversarialPathsLinksDuplicatesAndQuotas(t *testing.T) {
	for name, headers := range map[string][]*tar.Header{
		"traversal":       {{Name: "workspace/../../outside", Typeflag: tar.TypeReg}},
		"absolute":        {{Name: "/etc/stolen", Typeflag: tar.TypeReg}},
		"backslash":       {{Name: "workspace/..\\outside", Typeflag: tar.TypeReg}},
		"symlink":         {{Name: "workspace/link", Typeflag: tar.TypeSymlink, Linkname: "/etc"}},
		"hardlink":        {{Name: "workspace/link", Typeflag: tar.TypeLink, Linkname: "workspace/file"}},
		"fifo":            {{Name: "workspace/fifo", Typeflag: tar.TypeFifo}},
		"duplicate":       {{Name: "workspace/file", Typeflag: tar.TypeReg}, {Name: "workspace/file", Typeflag: tar.TypeReg}},
		"implicit parent": {{Name: "workspace/dir/file", Typeflag: tar.TypeReg}},
		"wrong volume":    {{Name: "history/0/file", Typeflag: tar.TypeReg}},
		"byte overflow":   {{Name: "workspace/file", Typeflag: tar.TypeReg, Size: 11}},
		"entry overflow":  {{Name: "workspace/a", Typeflag: tar.TypeReg}, {Name: "workspace/b", Typeflag: tar.TypeReg}},
		"pax":             {{Name: "workspace/file", Typeflag: tar.TypeReg, PAXRecords: map[string]string{"comment": "untrusted"}, Format: tar.FormatPAX}},
	} {
		t.Run(name, func(t *testing.T) {
			data := archiveBytes(t, headers)
			if err := receiveArchive(bytes.NewReader(data), privateDir(t), []volume{{Name: "workspace", Bytes: 10}}, 1); err == nil {
				t.Fatal("malicious checkpoint accepted")
			}
		})
	}
}

func TestHistoryQuotaIncludesPreviousGenerations(t *testing.T) {
	c, spec, opts := fixture(t, context.Background())
	root := filepath.Join(c.opts.StateDir, "sessions", "agent")
	old := filepath.Join(root, "g-old")
	current := filepath.Join(root, "g-current")
	os.MkdirAll(old, 0700)
	os.MkdirAll(current, 0700)
	os.WriteFile(filepath.Join(old, "old"), make([]byte, (1<<20)-4), 0600)
	os.WriteFile(filepath.Join(current, "session"), []byte("old!"), 0600)
	spec.SessionDirs = []harness.SessionDir{{Source: current, Target: "/home/agent/.codex/sessions"}}
	if _, err := c.Wrap(spec, opts); err != nil {
		t.Fatal(err)
	}
	if len(c.volumes) != 2 || c.volumes[1].Bytes != 4 {
		t.Fatal("old generations not charged")
	}
	// A validly framed malicious exporter tries to exceed the remaining quota.
	data := archiveBytes(t, []*tar.Header{{Name: "history/0/session", Typeflag: tar.TypeReg, Size: 5}})
	conn, err := net.Dial("unix", filepath.Join(c.dir, "checkpoint.sock"))
	if err != nil {
		t.Fatal(err)
	}
	conn.Write(append(data, []byte(checkpointEnd)...))
	conn.(*net.UnixConn).CloseWrite()
	io.Copy(io.Discard, conn)
	conn.Close()
	if err = c.Finish(); err == nil {
		t.Fatal("previous generations escaped history cap")
	}
	if b, _ := os.ReadFile(filepath.Join(current, "session")); string(b) != "old!" {
		t.Fatal("over-quota checkpoint replaced old history")
	}
}

func TestCopyInRegularFiles(t *testing.T) {
	source := privateDir(t)
	target := privateDir(t)
	os.Mkdir(filepath.Join(source, "dir"), 0700)
	os.WriteFile(filepath.Join(source, "dir", "file"), []byte("seed"), 0700)
	if err := copyTree(source, target, 1024, 10); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(target, "dir", "file")); string(b) != "seed" {
		t.Fatal("copy-in lost data")
	}
}
