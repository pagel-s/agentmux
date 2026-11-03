package sandbox

import (
	"context"
	"crypto/x509"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"agentmux/internal/egress"
	"agentmux/internal/harness"
)

func workflowBoundaryFixture(t *testing.T) (string, string, WorkflowOptions) {
	t.Helper()
	root := t.TempDir()
	state := filepath.Join(root, "run")
	workspace := filepath.Join(state, "worktree")
	for _, path := range []string{state, workspace} {
		if err := os.Mkdir(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	return root, workspace, WorkflowOptions{Workspace: workspace, StateDir: state, Network: "none"}
}

func TestWorkflowBoundaryRefusesHostNetworkAndBroadMounts(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux Bubblewrap boundary")
	}
	root, workspace, options := workflowBoundaryFixture(t)
	tools := filepath.Join(root, "tools")
	if err := os.Mkdir(tools, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tools, "bwrap"), []byte("#!/bin/sh\nexit 125\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", tools+":/usr/bin:/bin")
	spec := harness.Spec{Name: "command", Binary: "/usr/bin/true", Env: []string{"PATH=/usr/bin:/bin", "HOME=/owner", "CODEX_HOME=/owner/.codex", "AGENTMUX_CREDENTIAL_FILE=/owner/private", "SSH_AUTH_SOCK=/owner/keyring", "HTTPS_PROXY=http://owner.invalid", "AGENTMUX_PI_API_KEY=selected-fixture-only"}, Workdir: workspace}
	wrapped, err := WrapWorkflow(spec, options)
	if err != nil {
		t.Fatal(err)
	}
	if slices.Contains(wrapped.Args, "--share-net") || slices.Contains(wrapped.Args, "/owner") || slices.Contains(wrapped.Args, credentialTarget) || slices.Contains(wrapped.Args, socketTarget) {
		t.Fatal("brokerless workflow exposed host networking, home or broker state")
	}
	joined := strings.Join(wrapped.Env, "\n")
	for _, forbidden := range []string{"/owner", "SSH_AUTH_SOCK=", "HTTPS_PROXY=", "AGENTMUX_CREDENTIAL_FILE="} {
		if strings.Contains(joined, forbidden) {
			t.Fatalf("host configuration survived the boundary: %s", forbidden)
		}
	}
	if !strings.Contains(joined, "HOME=/home/agent") || !strings.Contains(joined, "AGENTMUX_PI_API_KEY=selected-fixture-only") || !strings.Contains(joined, "GIT_CONFIG_GLOBAL=/dev/null") {
		t.Fatal("private home, explicit provider selection or Git restrictions lost")
	}
	if !slices.Contains(wrapped.Args, "/home/agent/.codex") {
		t.Fatal("private Codex config directory is absent without a credential file")
	}
	for _, change := range []func(*WorkflowOptions){
		func(o *WorkflowOptions) { o.Network = "host" },
		func(o *WorkflowOptions) { o.ReadOnlyPaths = []string{root} },
		func(o *WorkflowOptions) { o.ReadOnlyPaths = []string{options.StateDir} },
		func(o *WorkflowOptions) { o.ReadOnlyPaths = []string{"/etc"} },
		func(o *WorkflowOptions) { o.HomeBytes = 1 },
		func(o *WorkflowOptions) { o.Workspace = root },
	} {
		bad := options
		change(&bad)
		if _, err := WrapWorkflow(spec, bad); err == nil {
			t.Fatalf("unsafe workflow boundary accepted: %+v", bad)
		}
	}
}

func requireWorkflowBoundary(t *testing.T) {
	t.Helper()
	if os.Getenv("AGENTMUX_SANDBOX_INTEGRATION") != "1" {
		t.Skip("set AGENTMUX_SANDBOX_INTEGRATION=1 for actual workflow filesystem/network checks")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := ProbeWorkflow(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestWorkflowBoundaryRealHidesHostStateAndNetwork(t *testing.T) {
	requireWorkflowBoundary(t)
	root, workspace, options := workflowBoundaryFixture(t)
	secret := filepath.Join(root, "owner-secret")
	if err := os.WriteFile(secret, []byte("must-not-read"), 0600); err != nil {
		t.Fatal(err)
	}
	unixPath := filepath.Join(root, "host-service.sock")
	listener, err := net.Listen("unix", unixPath)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	host := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "host-service") }))
	defer host.Close()
	_, port, _ := net.SplitHostPort(strings.TrimPrefix(host.URL, "http://"))
	script := fmt.Sprintf(`import os,pathlib,socket
for p in [%q, '/proc/1/root'+%q, %q]:
    assert not pathlib.Path(p).exists(), p
for p in [%q, '/usr/agentmux-isolation-must-not-write']:
    try:
        pathlib.Path(p).write_text('escape')
    except OSError: pass
try:
    socket.create_connection(('127.0.0.1',%s),timeout=0.3)
    raise AssertionError('host loopback reached')
except OSError: pass
try:
    c=socket.socket(socket.AF_UNIX);c.connect(%q)
    raise AssertionError('host Unix service reached')
except OSError: pass
assert os.environ['HOME']=='/home/agent'
assert 'SSH_AUTH_SOCK' not in os.environ
assert 'AGENTMUX_SOCKET' not in os.environ
assert 'UNSELECTED_WORKFLOW_SECRET' not in os.environ
pathlib.Path('result.txt').write_text('inside only')
print('WORKFLOW_BOUNDARY_OK')
`, secret, secret, unixPath, secret, port, unixPath)
	t.Setenv("UNSELECTED_WORKFLOW_SECRET", "owner-only")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	result, err := harness.Run(ctx, harness.Request{Harness: "command", Command: []string{"/usr/bin/python3", "-c", script}, Workdir: workspace, Wrap: func(spec harness.Spec) (harness.Spec, error) { return WrapWorkflow(spec, options) }, ReapDescendants: true})
	if err != nil || !strings.Contains(result.Text, "WORKFLOW_BOUNDARY_OK") {
		t.Fatalf("real workflow boundary failed: %v %s", err, result.Text)
	}
	data, err := os.ReadFile(secret)
	if err != nil || string(data) != "must-not-read" {
		t.Fatal("isolated process changed host secret")
	}
	if data, err = os.ReadFile(filepath.Join(workspace, "result.txt")); err != nil || string(data) != "inside only" {
		t.Fatal("isolated workspace output was not retained")
	}
}

func TestWorkflowBoundaryRealAllowlistCannotBypassProxy(t *testing.T) {
	requireWorkflowBoundary(t)
	binary := os.Getenv("AGENTMUX_INTEGRATION_BINARY")
	if binary == "" {
		t.Skip("AGENTMUX_INTEGRATION_BINARY is required for the actual egress relay")
	}
	binary, err := filepath.Abs(binary)
	if err != nil {
		t.Fatal(err)
	}
	root, workspace, options := workflowBoundaryFixture(t)
	allowed := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "allowed") }))
	defer allowed.Close()
	var leaked atomic.Bool
	denied := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { leaked.Store(true); fmt.Fprint(w, "denied") }))
	defer denied.Close()
	proxyDir := filepath.Join(root, "proxy")
	if err := os.Mkdir(proxyDir, 0700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	socket := filepath.Join(proxyDir, "egress.sock")
	proxy, err := egress.StartStrict(ctx, socket, []egress.Endpoint{{Origin: allowed.URL, IPs: []string{"127.0.0.1"}}}, egress.StrictOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer proxy.Close()
	options.Network, options.EgressSocket, options.RelayExecutable = "allowlist", socket, binary
	certificate := filepath.Join(proxyDir, "public-ca.pem")
	if err := os.WriteFile(certificate, proxy.PublicCertificate(), 0600); err != nil {
		t.Fatal(err)
	}
	options.EgressCA = certificate
	_, port, _ := net.SplitHostPort(strings.TrimPrefix(allowed.URL, "http://"))
	script := fmt.Sprintf(`import socket,urllib.request,urllib.error
assert urllib.request.urlopen(%q,timeout=3).read()==b'allowed'
try:
    urllib.request.urlopen(%q,timeout=3)
    raise AssertionError('unlisted origin reached')
except urllib.error.HTTPError as e: assert e.code==403
try:
    socket.create_connection(('127.0.0.1',%s),timeout=0.3)
    raise AssertionError('proxy bypass reached host loopback')
except OSError: pass
print('WORKFLOW_EGRESS_OK')
`, allowed.URL, denied.URL, port)
	result, err := harness.Run(ctx, harness.Request{Harness: "command", Command: []string{"/usr/bin/python3", "-c", script}, Workdir: workspace, Wrap: func(spec harness.Spec) (harness.Spec, error) { return WrapWorkflow(spec, options) }, ReapDescendants: true})
	if err != nil || !strings.Contains(result.Text, "WORKFLOW_EGRESS_OK") || leaked.Load() {
		t.Fatalf("isolated proxy failed: %v %s leaked=%t", err, result.Text, leaked.Load())
	}
}

func TestWorkflowBoundaryRealInspectedHTTPSAndStreaming(t *testing.T) {
	requireWorkflowBoundary(t)
	binary := os.Getenv("AGENTMUX_INTEGRATION_BINARY")
	if binary == "" {
		t.Skip("AGENTMUX_INTEGRATION_BINARY required for real inspected TLS relay")
	}
	root, workspace, options := workflowBoundaryFixture(t)
	var reached atomic.Int64
	release := make(chan struct{})
	var released atomic.Bool
	up := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached.Add(1)
		if r.URL.Path == "/release" {
			if released.CompareAndSwap(false, true) {
				close(release)
			}
			fmt.Fprint(w, "released")
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: first\n\n")
		w.(http.Flusher).Flush()
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		fmt.Fprint(w, "data: final\n\n")
	}))
	defer up.Close()
	roots := x509.NewCertPool()
	roots.AddCert(up.Certificate())
	proxyDir := filepath.Join(root, "tls-proxy")
	if err := os.Mkdir(proxyDir, 0700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	socket := filepath.Join(proxyDir, "egress.sock")
	proxy, err := egress.StartStrict(ctx, socket, []egress.Endpoint{{Origin: up.URL, IPs: []string{"127.0.0.1"}}}, egress.StrictOptions{UpstreamRoots: roots})
	if err != nil {
		t.Fatal(err)
	}
	defer proxy.Close()
	certificate := filepath.Join(proxyDir, "public-ca.pem")
	if err := os.WriteFile(certificate, proxy.PublicCertificate(), 0600); err != nil {
		t.Fatal(err)
	}
	options.Network, options.EgressSocket, options.RelayExecutable, options.EgressCA = "allowlist", socket, binary, certificate
	script := fmt.Sprintf(`import os,pathlib,urllib.request,urllib.error
trust=pathlib.Path('/run/agentmux/egress-ca.pem')
assert os.environ['SSL_CERT_FILE']==str(trust)
assert os.environ['CODEX_CA_CERTIFICATE']==str(trust)
assert os.environ['NODE_EXTRA_CA_CERTS']==str(trust)
assert b'PRIVATE KEY' not in trust.read_bytes()
assert not pathlib.Path(%q).exists()
try:
    trust.write_text('mutated')
    raise AssertionError('public TLS trust mount was writable')
except OSError: pass
req=urllib.request.Request(%q,headers={'Host':'denied.invalid'})
try:
    urllib.request.urlopen(req,timeout=5)
    raise AssertionError('HTTPS Host escaped CONNECT policy')
except urllib.error.HTTPError as e: assert e.code==403
stream=urllib.request.urlopen(%q,timeout=5)
assert stream.readline()==b'data: first\n'
assert urllib.request.urlopen(%q,timeout=5).read()==b'released'
assert b'data: final' in stream.read()
print('INSPECTED_TLS_STREAM_OK')
`, certificate, up.URL+"/stream", up.URL+"/stream", up.URL+"/release")
	result, err := harness.Run(ctx, harness.Request{Harness: "command", Command: []string{"/usr/bin/python3", "-c", script}, Workdir: workspace, Wrap: func(spec harness.Spec) (harness.Spec, error) { return WrapWorkflow(spec, options) }, ReapDescendants: true})
	if err != nil || !strings.Contains(result.Text, "INSPECTED_TLS_STREAM_OK") || reached.Load() != 2 {
		t.Fatalf("real inspected TLS failed: %v %s upstream=%d", err, result.Text, reached.Load())
	}
}
