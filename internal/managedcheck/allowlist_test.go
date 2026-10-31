package managedcheck

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"testing"

	"agentmux/internal/egress"
	"agentmux/internal/managedconfig"
)

func TestAllowlistMountPreflightUsesOfflineBoundaryWithoutLiveRelay(t *testing.T) {
	root := t.TempDir()
	state, workspace := filepath.Join(root, "state"), filepath.Join(root, "work")
	for _, dir := range []string{state, workspace, filepath.Join(state, "credentials")} {
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(state, "credentials", "worker.json"), []byte(`{"token":"fixture"}`), 0600); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("unix", filepath.Join(state, "broker.sock"))
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	agent := managedconfig.Agent{Harness: "command", Command: []string{"/bin/cat"}, Sandbox: "bubblewrap", Network: "allowlist", Egress: managedconfig.Egress{Allow: []egress.Endpoint{{Origin: "http://127.0.0.1:8097", IPs: []string{"127.0.0.1"}}}}, Workdir: workspace}
	c := checker{ctx: context.Background(), cfg: managedconfig.Config{Agents: map[string]managedconfig.Agent{"worker": agent}}, opts: Options{StateDir: state}, initialized: true, executable: executable, metadata: map[string]metadataResult{}}
	c.agent("worker", agent)
	report := c.finish()
	sandboxOK, allowlistWarning := false, false
	for _, check := range report.Checks {
		if check.Name == "sandbox" && check.Status == StatusOK {
			sandboxOK = true
		}
		if check.Name == "network.allowlist" && check.Status == StatusWarning {
			allowlistWarning = true
		}
		if check.Status == StatusError {
			t.Fatalf("metadata preflight incorrectly required live egress: %+v", check)
		}
	}
	if !sandboxOK || !allowlistWarning {
		t.Fatalf("sandbox=%t honestallowlist=%t", sandboxOK, allowlistWarning)
	}
}
