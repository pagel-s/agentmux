package egress

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"agentmux/internal/harness"
	"agentmux/internal/sandbox"
)

// An environment proxy alone is bypassable. This opt-in check proves the
// namespace has no direct host-loopback route while the explicit proxy works.
func TestRealNamespaceProxyCannotBypassAllowlist(t *testing.T) {
	binary := os.Getenv("AGENTMUX_INTEGRATION_BINARY")
	if binary == "" {
		t.Skip("set AGENTMUX_INTEGRATION_BINARY to a freshly built agentmux binary")
	}
	binary, err := filepath.Abs(binary)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	allowed := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "allowed-endpoint") }))
	defer allowed.Close()
	var leaked atomic.Bool
	denied := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { leaked.Store(true); fmt.Fprint(w, "denied-endpoint") }))
	defer denied.Close()
	state, err := os.MkdirTemp("", "amx-egress-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(state)
	workspace := filepath.Join(state, "workspace")
	os.Mkdir(workspace, 0700)
	credential := filepath.Join(state, "credential.json")
	os.WriteFile(credential, []byte("private"), 0600)
	broker := filepath.Join(state, "broker.sock")
	listener, err := net.Listen("unix", broker)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	proxyDir := filepath.Join(state, "egress")
	os.Mkdir(proxyDir, 0700)
	socket := filepath.Join(proxyDir, "proxy.sock")
	server, err := Start(ctx, socket, []Endpoint{{Origin: allowed.URL}})
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	_, port, _ := net.SplitHostPort(strings.TrimPrefix(allowed.URL, "http://"))
	script := fmt.Sprintf(`import os, socket, urllib.request, urllib.error
assert 'NO_PROXY' not in os.environ and 'no_proxy' not in os.environ
assert urllib.request.urlopen(%q, timeout=3).read() == b'allowed-endpoint'
try:
    urllib.request.urlopen(%q, timeout=3)
    raise AssertionError('denied origin reached')
except urllib.error.HTTPError as error:
    assert error.code == 403
try:
    socket.create_connection(('127.0.0.1', %s), timeout=1)
    raise AssertionError('direct network bypass reached host')
except OSError: pass
try:
    urllib.request.build_opener(urllib.request.ProxyHandler({})).open(%q, timeout=1)
    raise AssertionError('proxy opt-out reached host')
except (urllib.error.URLError, OSError): pass
print('EGRESS_NAMESPACE_OK')
`, allowed.URL, denied.URL, port, allowed.URL)
	wrapped, err := sandbox.Wrap(harness.Spec{Name: "command", Binary: "/usr/bin/python3", Args: []string{"-c", script}, Env: []string{"PATH=/usr/bin:/bin", "NO_PROXY=*", "http_proxy=http://evil.invalid"}, Workdir: workspace}, sandbox.Options{Mode: "bubblewrap", Network: "allowlist", Workspace: workspace, StateDir: state, CredentialFile: credential, SocketPath: broker, EgressSocket: socket, RelayExecutable: binary})
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(ctx, wrapped.Binary, wrapped.Args...)
	cmd.Env = wrapped.Env
	cmd.Dir = wrapped.Workdir
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("real proxy namespace test failed: %v: %s", err, output)
	}
	if !strings.Contains(string(output), "EGRESS_NAMESPACE_OK") || leaked.Load() {
		t.Fatal("allowlist was bypassed or native probe did not run")
	}
}
