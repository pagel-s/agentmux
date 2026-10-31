package managedsetup

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"agentmux/internal/egress"
	"agentmux/internal/managedconfig"
)

func fakeBinary(t *testing.T, name string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
		t.Fatal(err)
	}
	return path
}
func basicSpec(t *testing.T) Spec {
	return Spec{Session: "team", WorkspaceRoot: filepath.Join(t.TempDir(), "workspaces"), Sandbox: "bubblewrap", BrokerSandbox: "bubblewrap", SharedBoard: true, Agents: []Agent{{ID: "lead", Role: "developer", Harness: "claude", Binary: fakeBinary(t, "claude"), Auth: "file", AuthFile: filepath.Join(t.TempDir(), "selected-auth.json"), CodingTools: true}}}
}

func TestPlanExplicitCredentialsPrivateYAMLAndNoOverwrite(t *testing.T) {
	spec := basicSpec(t)
	plan, err := Build(spec)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(spec.WorkspaceRoot); !os.IsNotExist(err) {
		t.Fatal("Build created workspace")
	}
	if len(plan.Config.Policy.Grants) != 0 || len(plan.Config.Boards["team"].Writers) != 1 {
		t.Fatal("unexpected capabilities")
	}
	file := filepath.Join(t.TempDir(), "team.yaml")
	if err := Write(file, plan.Config); err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(file)
	if info.Mode().Perm() != 0600 {
		t.Fatalf("mode=%o", info.Mode().Perm())
	}
	if _, err := managedconfig.Load(file); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(file)
	if err := Write(file, plan.Config); err == nil {
		t.Fatal("overwrote existing output")
	}
	after, _ := os.ReadFile(file)
	if string(before) != string(after) {
		t.Fatal("existing output changed")
	}
	if !strings.Contains(string(before), "selected-auth.json") || strings.Contains(string(before), "accessToken") {
		t.Fatal("expected reference-only auth")
	}
}

func TestPlanRejectsImplicitOrUnsupportedAuth(t *testing.T) {
	for _, change := range []func(*Spec){func(s *Spec) { s.Agents[0].Auth = "" }, func(s *Spec) { s.Sandbox = "local"; s.Agents[0].Auth = "none"; s.Agents[0].AuthFile = "" }, func(s *Spec) {
		s.Agents[0].Harness = "antigravity"
		s.Agents[0].Auth = "none"
		s.Agents[0].AuthFile = ""
	}, func(s *Spec) { s.Agents[0].APIKeyEnv = "SECRET" }} {
		spec := basicSpec(t)
		change(&spec)
		if _, err := Build(spec); err == nil {
			t.Fatal("unsafe auth choice accepted")
		}
	}
}

func TestGitInitOnlyNewWorkspaceAndNoGlobalConfig(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("Git missing")
	}
	spec := basicSpec(t)
	plan, err := Build(spec)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := InitWorkspaces(context.Background(), plan, true); err != nil {
		t.Fatal(err)
	}
	config := filepath.Join(plan.Workspaces[0], ".git", "config")
	data, err := os.ReadFile(config)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "hooksPath") {
		t.Fatal("Git settings persisted")
	}
	if err := os.WriteFile(config, []byte("owner sentinel"), 0600); err != nil {
		t.Fatal(err)
	}
	notes, err := InitWorkspaces(context.Background(), plan, true)
	if err != nil || len(notes) != 1 {
		t.Fatalf("notes=%v err=%v", notes, err)
	}
	after, _ := os.ReadFile(config)
	if string(after) != "owner sentinel" {
		t.Fatal("existing Git repository changed")
	}
}

func TestPiCodeClosureUsesExplicitNodeAndPackage(t *testing.T) {
	root := t.TempDir()
	pkg := filepath.Join(root, "node_modules", "@test", "pi-coding-agent")
	entry := filepath.Join(pkg, "dist", "cli.js")
	if err := os.MkdirAll(filepath.Dir(entry), 0700); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(pkg, "package.json"), []byte(`{"name":"@test/pi-coding-agent","optionalDependencies":{"helper":"1"}}`), 0600)
	os.WriteFile(entry, []byte("#!/usr/bin/env node\n"), 0700)
	helper := filepath.Join(root, "node_modules", "helper")
	os.Mkdir(helper, 0700)
	node := fakeBinary(t, "node")
	t.Setenv("PATH", filepath.Dir(node))
	command, paths, err := Discover("pi", entry)
	if err != nil {
		t.Fatal(err)
	}
	if len(command) != 2 || command[0] != node || command[1] != entry {
		t.Fatalf("command=%q", command)
	}
	joined := strings.Join(paths, "\n")
	if !strings.Contains(joined, pkg) || !strings.Contains(joined, helper) {
		t.Fatalf("paths=%q", paths)
	}
}

func TestDecodeRejectsUnknownFieldsAndTrailingData(t *testing.T) {
	for _, value := range []string{`{"api_key":"secret"}`, `{} {}`} {
		if _, err := Decode(strings.NewReader(value)); err == nil {
			t.Fatal("invalid spec accepted")
		}
	}
}

func TestAntigravityBridgeSetupUsesIsolatedPiAndExplicitInferenceKey(t *testing.T) {
	spec := Spec{Session: "agy-team", WorkspaceRoot: filepath.Join(t.TempDir(), "work"), Sandbox: "bubblewrap", BrokerSandbox: "bubblewrap", Agents: []Agent{{ID: "coder", Harness: "antigravity-bridge", Model: "exact-flash", Binary: fakeBinary(t, "pi"), Auth: "api-env", APIKeyEnv: "PRIVATE_BRIDGE_KEY", BaseURL: "http://127.0.0.1:8097/v1", Conversation: "persistent", Network: "allowlist", Egress: managedconfig.Egress{Allow: []egress.Endpoint{{Origin: "http://127.0.0.1:8097", IPs: []string{"127.0.0.1"}}}}}}}
	plan, err := Build(spec)
	if err != nil {
		t.Fatal(err)
	}
	agent := plan.Config.Agents["coder"]
	connection := plan.Config.Connections[agent.Connection]
	if agent.Harness != "pi" || agent.Sandbox != "bubblewrap" || agent.Conversation != "persistent" || agent.Auth.File != "" || connection.APIKeyEnv != "PRIVATE_BRIDGE_KEY" || agent.Network != "allowlist" {
		t.Fatal("provider login crossed inference boundary or isolated Pi settings were lost")
	}
	spec.Agents[0].Auth = "native-local"
	if _, err := Build(spec); err == nil {
		t.Fatal("bridge accepted host native login in agent")
	}
}
