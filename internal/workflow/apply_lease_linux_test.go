//go:build linux

package workflow

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestPIDNamespaceWriterRetainsApplicationLeaseUntilItsBoundaryExits(t *testing.T) {
	if os.Getenv("AGENTMUX_SANDBOX_INTEGRATION") != "1" {
		t.Skip("set AGENTMUX_SANDBOX_INTEGRATION=1 for real inherited application lease checks")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ctx, err := PrepareProcessContainment(ctx, PIDNamespace)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	lockPath := filepath.Join(root, "application.lock")
	lease, err := lockApplication(lockPath)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close()
	ready := filepath.Join(root, "ready")
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(ctx, python, "-c", "import pathlib,sys,time; pathlib.Path(sys.argv[1]).write_text('ready'); time.sleep(60)", ready)
	cmd.Env = cleanGitEnvironment()
	cmd.ExtraFiles = []*os.File{lease}
	configureGitProcess(cmd)
	if err := wrapGitProcess(ctx, cmd); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer cmd.Process.Kill()
	for deadline := time.Now().Add(3 * time.Second); ; {
		if _, err := os.Stat(ready); err == nil {
			break
		}
		if time.Now().After(deadline) {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
			t.Fatal("contained writer did not start")
		}
		time.Sleep(5 * time.Millisecond)
	}
	// Closing the owner's descriptor models its process being killed. The
	// Bubblewrap monitor must retain the shared lease for the live boundary.
	lease.Close()
	if stolen, err := lockApplication(lockPath); err == nil {
		stolen.Close()
		t.Fatal("live contained writer lost its inherited integration lease")
	}
	_ = cmd.Process.Kill()
	_ = cmd.Wait()
	for deadline := time.Now().Add(3 * time.Second); ; {
		if recovered, err := lockApplication(lockPath); err == nil {
			recovered.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("integration lease survived its contained writer boundary")
		}
		time.Sleep(5 * time.Millisecond)
	}
}
