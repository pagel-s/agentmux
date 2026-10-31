package managedcheck

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"agentmux/internal/managedconfig"
)

func TestManagedcheckProcess(t *testing.T) {
	marker := -1
	for i, arg := range os.Args {
		if arg == "--" && i+1 < len(os.Args) && strings.HasPrefix(os.Args[i+1], "managedcheck-") {
			marker = i + 1
			break
		}
	}
	if marker < 0 {
		return
	}
	scenario := os.Args[marker]
	args := strings.Join(os.Args[marker+1:], " ")
	if os.Getenv("OWNER_ONLY_SECRET") != "" {
		os.Exit(4)
	}
	if scenario == "managedcheck-slow" {
		time.Sleep(10 * time.Second)
		os.Exit(0)
	}
	switch {
	case strings.Contains(args, "auth status"):
		fmt.Println(`{"loggedIn":true,"email":"private-field-must-not-leak","token":"private-field-must-not-leak"}`)
	case strings.Contains(args, "--version"):
		if !strings.Contains(os.Getenv("HOME"), "agentmux-metadata-") {
			os.Exit(5)
		}
		fmt.Println("Native test CLI 2.1.316")
	case strings.Contains(args, "--help"):
		if !strings.Contains(os.Getenv("HOME"), "agentmux-metadata-") {
			os.Exit(5)
		}
		flags := requiredFlags("claude")
		if scenario == "managedcheck-interactive" {
			flags = requiredInteractiveFlags("claude")
		}
		if scenario == "managedcheck-pi" {
			flags = requiredFlags("pi")
		}
		if scenario == "managedcheck-missing" {
			flags = flags[1:]
		}
		if scenario == "managedcheck-stderr" {
			fmt.Fprintln(os.Stderr, strings.Join(flags, " "))
		} else {
			fmt.Println(strings.Join(flags, " "))
		}
	default:
		os.Exit(6) // A doctor must never send an actual prompt.
	}
	os.Exit(0)
}

func TestInteractiveDoctorChecksNativeInterfaceInsteadOfPrintProtocol(t *testing.T) {
	cfg := testConfig(t, "managedcheck-interactive")
	agent := cfg.Agents["worker"]
	agent.Interaction = "interactive"
	cfg.Agents["worker"] = agent
	report := Check(context.Background(), cfg, Options{Executable: os.Args[0]})
	if !report.OK {
		t.Fatalf("native UI was rejected for lacking headless flags: %+v", report)
	}
}

func testConfig(t *testing.T, scenario string) managedconfig.Config {
	t.Helper()
	return managedconfig.Config{Version: 2, Mode: "managed", Session: "doctor", Agents: map[string]managedconfig.Agent{
		"worker": {Harness: "claude", Model: "sonnet", Workdir: t.TempDir(), Command: []string{os.Args[0], "-test.run=TestManagedcheckProcess", "--", scenario}},
	}}
}

func findCheck(report Report, agent, name string) CheckResult {
	for _, item := range report.Checks {
		if item.Agent == agent && item.Name == name {
			return item
		}
	}
	return CheckResult{}
}

func TestCheckMetadataIsPrivateDeterministicAndNongenerative(t *testing.T) {
	t.Setenv("OWNER_ONLY_SECRET", "private-field-must-not-leak")
	cfg := testConfig(t, "managedcheck-good")
	first := Check(context.Background(), cfg, Options{Executable: os.Args[0], CheckAuth: true})
	second := Check(context.Background(), cfg, Options{Executable: os.Args[0], CheckAuth: true})
	if !first.OK || !reflect.DeepEqual(first, second) {
		t.Fatalf("unstable or failed report: %#v / %#v", first, second)
	}
	for _, name := range []string{"harness.binary", "harness.capabilities", "harness.version", "auth"} {
		if item := findCheck(first, "worker", name); item.Status != StatusOK {
			t.Fatalf("%s = %#v", name, item)
		}
	}
	encoded, _ := json.Marshal(first)
	if strings.Contains(string(encoded), "private-field-must-not-leak") || strings.Contains(string(encoded), "agentmux-metadata-") {
		t.Fatal("report leaked auth fields or temporary paths")
	}
}

func TestMissingFlagsAndCredentialsFail(t *testing.T) {
	cfg := testConfig(t, "managedcheck-missing")
	report := Check(context.Background(), cfg, Options{Executable: os.Args[0]})
	if report.OK || findCheck(report, "worker", "harness.capabilities").Status != StatusError {
		t.Fatal("missing native flags passed preflight")
	}
	cfg = testConfig(t, "managedcheck-good")
	cfg.Connections = map[string]managedconfig.Connection{"provider": {Protocol: "anthropic", APIKeyEnv: "AGENTMUX_MISSING_DOCTOR_TEST_KEY"}}
	agent := cfg.Agents["worker"]
	agent.Connection = "provider"
	agent.Env = map[string]string{"AGENTMUX_MISSING_DOCTOR_TEST_KEY": ""}
	cfg.Agents["worker"] = agent
	report = Check(context.Background(), cfg, Options{Executable: os.Args[0]})
	if report.OK || findCheck(report, "worker", "credential.env").Status != StatusError {
		t.Fatal("missing named credential passed preflight")
	}
}

func TestHelpOnStderrIsAcceptedWithoutExposingIt(t *testing.T) {
	report := Check(context.Background(), testConfig(t, "managedcheck-stderr"), Options{Executable: os.Args[0]})
	if !report.OK || findCheck(report, "worker", "harness.capabilities").Status != StatusOK {
		t.Fatal("CLI help on stderr must be recognized without returning raw diagnostics")
	}
}

func TestMetadataDeadlineAndUnverifiedAuth(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	report := Check(ctx, testConfig(t, "managedcheck-slow"), Options{Executable: os.Args[0]})
	if report.OK || time.Since(start) > time.Second {
		t.Fatalf("metadata was not bounded: elapsed=%v, report=%#v", time.Since(start), report)
	}
	report = Check(context.Background(), testConfig(t, "managedcheck-good"), Options{Executable: os.Args[0]})
	if !report.OK || findCheck(report, "worker", "auth").Status != StatusWarning {
		t.Fatal("unchecked auth must remain an honest warning")
	}
}

func TestCommandIsNotExecutedAndStateIsNotCreated(t *testing.T) {
	cfg := testConfig(t, "managedcheck-good")
	workdir := filepath.Join(t.TempDir(), "not-created-workspace")
	state := filepath.Join(t.TempDir(), "not-created-state")
	cfg.Agents["worker"] = managedconfig.Agent{Harness: "command", Command: []string{os.Args[0], "-test.run=TestManagedcheckProcess", "--", "managedcheck-slow"}, Workdir: workdir}
	start := time.Now()
	report := Check(context.Background(), cfg, Options{Executable: os.Args[0], StateDir: state})
	if !report.OK || time.Since(start) > time.Second {
		t.Fatal("command was executed or a creatable workspace was incorrectly rejected")
	}
	for _, path := range []string{workdir, state} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("preflight created %s", path)
		}
	}
}

func TestPrivateEndpointHeadersModelsAndRedirectSafety(t *testing.T) {
	connection := managedconfig.Connection{Protocol: "openai-compatible", BaseURL: "http://127.0.0.1:8000/v1"}
	called := 0
	do := func(req *http.Request) (*http.Response, error) {
		called++
		if req.Method != "GET" || req.URL.Path != "/v1/models" || req.Header.Get("Authorization") != "Bearer private-field-must-not-leak" {
			t.Fatal("metadata request path/auth incorrect")
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"data":[{"id":"local-qwen"}],"secret":"private-field-must-not-leak"}`))}, nil
	}
	status, message := probeEndpointUsing(context.Background(), connection, "local-qwen", "private-field-must-not-leak", do)
	if status != StatusOK || called != 1 || strings.Contains(message, "private-field-must-not-leak") {
		t.Fatalf("probe=%s %s, calls=%d", status, message, called)
	}
	status, _ = probeEndpointUsing(context.Background(), connection, "missing", "private-field-must-not-leak", do)
	if status != StatusError {
		t.Fatal("missing exact endpoint model was not rejected")
	}
	called = 0
	status, _ = probeEndpointUsing(context.Background(), connection, "local-qwen", "key", func(req *http.Request) (*http.Response, error) {
		called++
		return &http.Response{StatusCode: 307, Header: http.Header{"Location": []string{"https://external.example/key-collector"}}, Body: io.NopCloser(strings.NewReader(""))}, nil
	})
	if status != StatusWarning || called != 1 {
		t.Fatal("redirect must remain unverified without another credential-bearing request")
	}
	connection.BaseURL = "https://api.external.example/v1"
	status, _ = probeEndpointUsing(context.Background(), connection, "model", "key", func(*http.Request) (*http.Response, error) { t.Fatal("public endpoint was contacted"); return nil, nil })
	if status != StatusWarning {
		t.Fatal("external endpoint must remain unverified")
	}
}

func TestAnthropicMetadataUsesItsOwnProtocol(t *testing.T) {
	status, _ := probeEndpointUsing(context.Background(), managedconfig.Connection{Protocol: "anthropic", BaseURL: "http://127.0.0.1:8000"}, "sonnet", "private-key", func(req *http.Request) (*http.Response, error) {
		if req.URL.Path != "/v1/models" || req.Header.Get("x-api-key") != "private-key" || req.Header.Get("anthropic-version") == "" || req.Header.Get("Authorization") != "" {
			t.Fatal("Anthropic metadata used the wrong protocol/authentication")
		}
		return &http.Response{StatusCode: 401, Body: io.NopCloser(strings.NewReader("private-key"))}, nil
	})
	if status != StatusError {
		t.Fatal("metadata authentication rejection was not reported")
	}
}

func TestTemplatesAndReadonlyDependenciesAreCheckedBeforeState(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("bubblewrap templates require Linux")
	}
	tools := t.TempDir()
	if err := os.WriteFile(filepath.Join(tools, "bwrap"), []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", tools+":/usr/bin:/bin")
	cfg := testConfig(t, "managedcheck-good")
	cfg.Spawn = managedconfig.Spawn{Enabled: true, Templates: map[string]managedconfig.Agent{"broken": {Harness: "command", Command: []string{"/missing-template-tool"}, Sandbox: "bubblewrap"}}}
	report := Check(context.Background(), cfg, Options{Executable: os.Args[0]})
	if report.OK || findCheck(report, "template:broken", "harness.binary").Status != StatusError {
		t.Fatalf("enabled template binary was not checked: %#v", report)
	}
	if findCheck(report, "template:broken", "workspace").Status != StatusWarning {
		t.Fatal("uncreated template workspace must not be treated as an invalid configuration")
	}
	agent := cfg.Agents["worker"]
	agent.ReadOnlyPaths = []string{filepath.Join(t.TempDir(), "missing")}
	cfg.Agents["worker"] = agent
	report = Check(context.Background(), cfg, Options{Executable: os.Args[0]})
	if findCheck(report, "worker", "dependency.001").Status != StatusError {
		t.Fatal("missing declared read-only dependency passed preflight")
	}
}

func TestNodeScriptClosureRequiresRuntimeAndPackageMounts(t *testing.T) {
	root := t.TempDir()
	tools, packageDir := filepath.Join(root, "tools"), filepath.Join(root, "package")
	for _, path := range []string{tools, filepath.Join(packageDir, "dist")} {
		if err := os.MkdirAll(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	node, script := filepath.Join(tools, "node"), filepath.Join(packageDir, "dist", "cli.js")
	for path, text := range map[string]string{node: "#!/bin/sh\nexit 0\n", script: "#!/usr/bin/env node\n", filepath.Join(packageDir, "package.json"): `{"name":"doctor-test"}`} {
		if err := os.WriteFile(path, []byte(text), 0700); err != nil {
			t.Fatal(err)
		}
	}
	agent := managedconfig.Agent{Harness: "pi", Sandbox: "bubblewrap"}
	for _, test := range []struct {
		paths []string
		want  string
	}{{nil, StatusError}, {[]string{node}, StatusError}, {[]string{node, packageDir}, StatusOK}} {
		agent.ReadOnlyPaths = test.paths
		c := checker{ctx: context.Background(), report: Report{Checks: []CheckResult{}}}
		c.scriptClosure("worker", agent, script, filepath.Join(root, "workspace"), []string{"PATH=" + tools})
		if result := findCheck(c.report, "worker", "dependency.closure"); result.Status != test.want {
			t.Fatalf("mounts=%v, check=%#v", test.paths, result)
		}
	}
}
