package managedsetup

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"agentmux/internal/managedconfig"
)

func TestLocalInteractiveLauncherFollowsNativeUpdateWithoutStaleDependencies(t *testing.T) {
	for _, harness := range []string{"claude", "codex", "pi"} {
		t.Run(harness, func(t *testing.T) {
			root := t.TempDir()
			bin := filepath.Join(root, "bin")
			if err := os.Mkdir(bin, 0700); err != nil {
				t.Fatal(err)
			}
			old := filepath.Join(root, "version-one")
			updated := filepath.Join(root, "version-two")
			for path, value := range map[string]string{old: "version-one", updated: "version-two"} {
				if err := os.WriteFile(path, []byte("#!/bin/sh\nprintf '%s\\n' "+value+"\n"), 0700); err != nil {
					t.Fatal(err)
				}
			}
			launcher := filepath.Join(bin, harness)
			if err := os.Symlink(old, launcher); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", bin)
			var native managedconfig.Config
			var frozen []managedconfig.Agent
			for _, profile := range []struct{ sandbox, interaction string }{
				{"local", "interactive"}, {"local", "managed"},
				{"bubblewrap", "interactive"}, {"bubblewrap", "managed"},
			} {
				spec := Spec{Session: "update-test", WorkspaceRoot: filepath.Join(root, "work"), Sandbox: profile.sandbox, BrokerSandbox: "local", Agents: []Agent{{ID: "native", Harness: harness, Interaction: profile.interaction, Auth: "none"}}}
				if profile.sandbox == "local" {
					spec.Agents[0].Auth = "native-local"
				}
				plan, err := Build(spec)
				if err != nil {
					t.Fatalf("%+v: %v", profile, err)
				}
				agent := plan.Config.Agents["native"]
				if profile.sandbox == "local" && profile.interaction == "interactive" {
					if !slices.Equal(agent.Command, []string{launcher}) || len(agent.ReadOnlyPaths) != 0 {
						t.Fatalf("native selection lost its public launcher or retained versioned mounts: %+v", agent)
					}
					native = plan.Config
				} else {
					if agent.Command[0] != old || !slices.Contains(agent.ReadOnlyPaths, old) {
						t.Fatalf("%+v no longer pins the inspected executable and dependencies: %+v", profile, agent)
					}
					frozen = append(frozen, agent)
				}
			}
			// Persist before the provider updates: subsequent native launches use
			// this exact configuration, without rebuilding or changing PATH.
			configPath := filepath.Join(root, "native.yaml")
			if err := Write(configPath, native); err != nil {
				t.Fatal(err)
			}
			if err := os.Remove(launcher); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(updated, launcher); err != nil {
				t.Fatal(err)
			}
			if err := os.Remove(old); err != nil {
				t.Fatal(err)
			}
			loaded, err := managedconfig.Load(configPath)
			if err != nil {
				t.Fatal(err)
			}
			agent := loaded.Agents["native"]
			output, err := exec.Command(agent.Command[0], agent.Command[1:]...).CombinedOutput()
			if err != nil || string(output) != "version-two\n" {
				t.Fatalf("saved native launcher did not follow update: output=%q err=%v", output, err)
			}
			if len(agent.ReadOnlyPaths) != 0 {
				t.Fatalf("removed native version can still participate in dependency validation: %q", agent.ReadOnlyPaths)
			}
			for _, agent := range frozen {
				if agent.Command[0] != old || !slices.Contains(agent.ReadOnlyPaths, old) {
					t.Fatalf("update silently changed a managed/isolated snapshot: %+v", agent)
				}
				if _, err := exec.Command(agent.Command[0], agent.Command[1:]...).Output(); err == nil {
					t.Fatal("managed/isolated selection silently followed the new native version")
				}
			}
		})
	}
}

func TestLocalInteractivePreservesNodeShebangAndPublicWrapper(t *testing.T) {
	root := t.TempDir()
	bin := filepath.Join(root, "bin")
	pkg := filepath.Join(root, "node_modules", "@test", "pi-coding-agent")
	if err := os.MkdirAll(bin, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(pkg, 0700); err != nil {
		t.Fatal(err)
	}
	entry := filepath.Join(pkg, "cli.js")
	if err := os.WriteFile(entry, []byte("#!/usr/bin/env node\n"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pkg, "package.json"), []byte(`{"name":"@test/pi-coding-agent"}`), 0600); err != nil {
		t.Fatal(err)
	}
	node := filepath.Join(bin, "node")
	if err := os.WriteFile(node, []byte("#!/bin/sh\nprintf '%s\\n' \"$@\"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	launcher := filepath.Join(bin, "pi")
	if err := os.Symlink(entry, launcher); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	spec := Spec{Session: "native-node", WorkspaceRoot: filepath.Join(root, "work"), Sandbox: "local", BrokerSandbox: "local", Agents: []Agent{{ID: "pi", Harness: "pi", Auth: "native-local", Interaction: "interactive"}}}
	plan, err := Build(spec)
	if err != nil {
		t.Fatal(err)
	}
	command := plan.Config.Agents["pi"].Command
	output, err := exec.Command(command[0], command[1:]...).CombinedOutput()
	if err != nil || string(output) != launcher+"\n" {
		t.Fatalf("native shebang no longer receives public launcher path: command=%q output=%q err=%v", command, output, err)
	}
	managedCommand, _, err := Discover("pi", "")
	if err != nil || !slices.Equal(managedCommand, []string{node, entry}) {
		t.Fatalf("managed Node/package snapshot changed: command=%q err=%v", managedCommand, err)
	}
	// A normal public wrapper need not expose package metadata. Native mode
	// must run it unchanged rather than infer or replace its chosen runtime.
	wrapper := filepath.Join(bin, "wrapped-pi")
	if err := os.WriteFile(wrapper, []byte("#!/usr/bin/env node\n"), 0700); err != nil {
		t.Fatal(err)
	}
	spec.Agents[0].Binary = wrapper
	plan, err = Build(spec)
	if err != nil || !slices.Equal(plan.Config.Agents["pi"].Command, []string{wrapper}) {
		t.Fatalf("native wrapper required a recognized package layout: plan=%+v err=%v", plan, err)
	}
	if _, _, err := Discover("pi", wrapper); err == nil || !strings.Contains(err.Error(), "package") {
		t.Fatalf("managed execution accepted an unknown dependency closure: %v", err)
	}
}
