package harness

import (
	"reflect"
	"strings"
	"testing"
)

func TestInteractiveUsesNativeUIWithoutSubmittingInitialPrompt(t *testing.T) {
	for _, name := range []string{"claude", "codex", "pi"} {
		t.Run(name, func(t *testing.T) {
			spec, err := BuildInteractive(Request{Harness: name, Prompt: "Role and live broker boundaries", Model: "chosen-model"})
			if err != nil {
				t.Fatal(err)
			}
			joined := " " + strings.Join(spec.Args, " ") + " "
			for _, bad := range []string{" --print ", " exec ", " --mode rpc ", " --permission-mode dontAsk ", " --no-session ", " --dangerously-bypass-approvals-and-sandbox "} {
				if strings.Contains(joined, bad) {
					t.Fatalf("headless/bypass flag in native UI: %s", joined)
				}
			}
			if !strings.Contains(joined, "Role and live broker boundaries") || !strings.Contains(joined, "--model chosen-model") {
				t.Fatalf("role/model missing: %s", joined)
			}
			if spec.Args[len(spec.Args)-1] == "Role and live broker boundaries" {
				t.Fatal("role became an initial user prompt")
			}
		})
	}
}

func TestInteractivePreservesNativeConfigurationAndOwnerArguments(t *testing.T) {
	t.Setenv("AGENTMUX_NATIVE_PLUGIN_HOME", "/owner/plugins")
	t.Setenv("AGENTMUX_NATIVE_SETTING", "inherited")
	for _, name := range []string{"claude", "codex", "pi"} {
		t.Run(name, func(t *testing.T) {
			prefix := []string{"--owner-profile", "daily-work"}
			req := Request{Harness: name, Prompt: "Use the broker only within granted boundaries", Command: append([]string{"/selected/native-cli"}, prefix...), Workdir: "/selected/project", InheritEnvironment: true, Env: map[string]string{"AGENTMUX_NATIVE_SETTING": "explicit"}}
			spec, err := BuildInteractive(req)
			if err != nil {
				t.Fatal(err)
			}
			if spec.Binary != req.Command[0] || spec.Workdir != req.Workdir || len(spec.Args) < len(prefix) || !reflect.DeepEqual(spec.Args[:len(prefix)], prefix) {
				t.Fatalf("native executable, project, or owner arguments changed: %+v", spec)
			}
			for _, denied := range []string{"--safe-mode", "--no-extensions", "--no-skills", "--no-prompt-templates", "--no-context-files", "--disable-slash-commands", "--no-approve", "--no-session-persistence", "--permission-mode"} {
				for _, arg := range spec.Args {
					if arg == denied {
						t.Fatalf("native tools/configuration disabled by %s: %q", denied, spec.Args)
					}
				}
			}
			for _, entry := range []string{"AGENTMUX_NATIVE_PLUGIN_HOME=/owner/plugins", "AGENTMUX_NATIVE_SETTING=explicit"} {
				found := false
				for _, got := range spec.Env {
					found = found || got == entry
				}
				if !found {
					t.Fatalf("trusted native environment lost %q", entry)
				}
			}
			if name == "codex" {
				for _, arg := range spec.Args {
					if arg == "--sandbox" {
						t.Fatal("trusted local native session overrode the owner's Codex sandbox configuration")
					}
				}
			}
		})
	}
}

func TestInteractiveEnvironmentInheritanceIsExplicitAndDoesNotChangeManagedTurns(t *testing.T) {
	t.Setenv("AGENTMUX_UNSELECTED_OWNER_SECRET", "owner-only")
	for _, interactive := range []bool{false, true} {
		req := Request{Harness: "claude", Prompt: "role", InheritEnvironment: !interactive}
		build := Build
		if interactive {
			build = BuildInteractive
		}
		spec, err := build(req)
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range spec.Env {
			if strings.HasPrefix(entry, "AGENTMUX_UNSELECTED_OWNER_SECRET=") {
				t.Fatalf("unselected environment leaked (interactive=%t)", interactive)
			}
		}
	}
}
func TestInteractivePiEndpointRetainsToolsOffAndSelectedProvider(t *testing.T) {
	off := false
	req := Request{Harness: "pi", BaseURL: "http://127.0.0.1:11434/v1", Model: "exact-id"}
	req.ModelMetadata.Tools = &off
	spec, err := BuildInteractive(req)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(spec.Args, " ")
	if !strings.Contains(joined, "--provider agentmux") || !strings.Contains(joined, "--no-tools") {
		t.Fatal(joined)
	}
}
