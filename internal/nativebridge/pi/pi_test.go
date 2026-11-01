package pi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestSupportedPiVersions(t *testing.T) {
	for version, expected := range map[string]bool{
		"1.4.0": true, "v1.4.0+build.3": true, "Pi 1.4.2": true, "1.5.0": true, "2.0.0": true,
		"1.4.1-preview.1": true, "1.4.0-rc.1": false, "1.3.99": false, "0.99.0": false,
		"": false, "unknown": false, "1.4": false, "01.4.0": false, "1.4.1-..": false,
		"1.4.1-01": false, "1.4.1+..": false, "1.4.0\nunexpected output": false,
	} {
		if actual := supportedVersion(version); actual != expected {
			t.Errorf("supportedVersion(%q) = %v; want %v", version, actual, expected)
		}
	}
}

func TestRunChecksPiVersionAndPreservesUnsupportedTerminal(t *testing.T) {
	for _, scenario := range []string{"new", "old", "malformed", "failure", "timeout", "inherited-pipe", "oversized"} {
		t.Run(scenario, func(t *testing.T) {
			root := t.TempDir()
			binary := filepath.Join(root, "pi")
			record := filepath.Join(root, "native-argv.json")
			childPID := filepath.Join(root, "shim-child")
			script := `#!/usr/bin/python3
import json,os,subprocess,sys,time
if '--version' in sys.argv:
 scenario=SCENARIO
 if scenario=='new': print('1.4.0')
 elif scenario=='old': print('0.57.0')
 elif scenario=='malformed': print('NOT_A_PI_VERSION')
 elif scenario=='failure': print('PRIVATE_PROBE_DIAGNOSTIC',file=sys.stderr);sys.exit(3)
 elif scenario=='timeout': time.sleep(3)
 elif scenario=='oversized': print('1.4.0'+'x'*8192)
 elif scenario=='inherited-pipe':
  child=subprocess.Popen(['/bin/sleep','3'])
  open(CHILD,'w').write(str(child.pid))
  print('1.4.0')
 sys.exit(0)
json.dump(sys.argv[1:],open(RECORD,'w'))
`
			quoted := func(value string) string { raw, _ := json.Marshal(value); return string(raw) }
			script = strings.NewReplacer("SCENARIO", quoted(scenario), "RECORD", quoted(record), "CHILD", quoted(childPID)).Replace(script)
			if err := os.WriteFile(binary, []byte(script), 0700); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				data, err := os.ReadFile(childPID)
				if err == nil {
					var pid int
					_, _ = fmt.Sscanf(string(data), "%d", &pid)
					if process, err := os.FindProcess(pid); err == nil {
						_ = process.Kill()
					}
				}
			})
			input := []string{binary, "--model", "owner/model $(literal)", "--extension", "/owner/custom.ts"}
			before := append([]string(nil), input...)
			var warning bytes.Buffer
			started := time.Now()
			if err := run(context.Background(), input, &warning, 100*time.Millisecond); err != nil {
				t.Fatal(err)
			}
			if elapsed := time.Since(started); elapsed > time.Second {
				t.Fatalf("Pi version probe was not bounded: %v", elapsed)
			}
			var actual []string
			data, err := os.ReadFile(record)
			if err != nil || json.Unmarshal(data, &actual) != nil {
				t.Fatalf("original native terminal did not run: %v", err)
			}
			if !reflect.DeepEqual(input, before) {
				t.Fatal("owner invocation was mutated")
			}
			if scenario == "new" {
				if len(actual) != len(input)+1 || !reflect.DeepEqual(actual[:len(input)-1], input[1:]) || actual[len(actual)-2] != "--extension" {
					t.Fatalf("supported Pi lost native arguments or bridge: %q", actual)
				}
				if _, err := os.Stat(actual[len(actual)-1]); !os.IsNotExist(err) {
					t.Fatal("per-invocation extension asset was not cleaned up")
				}
				if warning.Len() != 0 {
					t.Fatalf("supported version warned: %s", warning.String())
				}
			} else {
				if !reflect.DeepEqual(actual, input[1:]) {
					t.Fatalf("fallback changed original native arguments: %q", actual)
				}
				if !strings.Contains(warning.String(), "manual coordination") || strings.Contains(warning.String(), "PRIVATE_PROBE_DIAGNOSTIC") {
					t.Fatalf("missing or unsafe compatibility warning: %q", warning.String())
				}
			}
		})
	}
}

func TestVersionProbeNeverIncludesOwnerPromptsOrCommands(t *testing.T) {
	for _, scenario := range []struct {
		name, binary string
		args         []string
		probe        bool
	}{
		{"separator", "pi", []string{"--", "PRIVATE_PROMPT", "--version"}, true},
		{"separator-as-option-value", "pi", []string{"--append-system-prompt", "--", "owner prompt"}, true},
		{"node-script", "node", []string{"cli.js", "--", "PRIVATE_PROMPT"}, true},
		{"node-runtime-separator", "node", []string{"--", "cli.js", "--", "PRIVATE_PROMPT"}, true},
		{"inline-shell", "sh", []string{"-c", "original user command"}, false},
		{"inline-node", "node", []string{"-e", "original user code"}, false},
		{"unknown-wrapper", "custom-wrapper", []string{"owner args"}, false},
		{"package-install", "pi", []string{"install", "owner package"}, false},
		{"config", "pi", []string{"config"}, false},
		{"auth", "pi", []string{"auth", "login"}, false},
		{"mcp", "pi", []string{"mcp", "add"}, false},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			root := t.TempDir()
			binary := filepath.Join(root, scenario.binary)
			record := filepath.Join(root, "calls.jsonl")
			encoded, _ := json.Marshal(record)
			script := "#!/usr/bin/python3\nimport json,sys\nargs=sys.argv[1:]\nwith open(" + string(encoded) + ",'a') as f:f.write(json.dumps(args)+'\\n')\nif args in [['--version'],['cli.js','--version'],['--','cli.js','--version']]:print('1.4.0')\n"
			if err := os.WriteFile(binary, []byte(script), 0700); err != nil {
				t.Fatal(err)
			}
			input := append([]string{binary}, scenario.args...)
			var warning bytes.Buffer
			if err := run(context.Background(), input, &warning, time.Second); err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(record)
			if err != nil {
				t.Fatal(err)
			}
			var calls [][]string
			for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
				var args []string
				if err := json.Unmarshal([]byte(line), &args); err != nil {
					t.Fatal(err)
				}
				calls = append(calls, args)
			}
			if !scenario.probe {
				if len(calls) != 1 || !reflect.DeepEqual(calls[0], scenario.args) || !strings.Contains(warning.String(), "manual coordination") {
					t.Fatalf("unsafe probe or changed fallback: %q; %s", calls, warning.String())
				}
				return
			}
			prefix, _ := piPrefix(input)
			wantProbe := append(append([]string(nil), input[1:prefix]...), "--version")
			if len(calls) != 2 || !reflect.DeepEqual(calls[0], wantProbe) || warning.Len() != 0 {
				t.Fatalf("version probe included native owner content: %q; %s", calls, warning.String())
			}
			actual := calls[1]
			at := prefix - 1
			if actual[at] != "--extension" {
				t.Fatalf("bridge flags entered the owner prompt: %q", actual)
			}
			actual = append(append([]string(nil), actual[:at]...), actual[at+2:]...)
			if !reflect.DeepEqual(actual, scenario.args) {
				t.Fatalf("supported invocation changed owner arguments: %q", calls[1])
			}
		})
	}
}

func TestRunPreservesNativeTerminalWhenBridgeAssetCannotBePrepared(t *testing.T) {
	root := t.TempDir()
	binary := filepath.Join(root, "pi")
	record := filepath.Join(root, "native-argv.json")
	encoded, _ := json.Marshal(record)
	script := "#!/usr/bin/python3\nimport json,sys\nif sys.argv[1:]==['--version']:print('1.4.0')\nelse:json.dump(sys.argv[1:],open(" + string(encoded) + ",'w'))\n"
	if err := os.WriteFile(binary, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	blocked := filepath.Join(root, "not-a-directory")
	if err := os.WriteFile(blocked, []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMPDIR", blocked)
	var warning bytes.Buffer
	if err := run(context.Background(), []string{binary, "--model", "owner/model"}, &warning, time.Second); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(record)
	if err != nil {
		t.Fatal(err)
	}
	var actual []string
	if err = json.Unmarshal(data, &actual); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(actual, []string{"--model", "owner/model"}) || !strings.Contains(warning.String(), "manual coordination") {
		t.Fatalf("bridge setup failure prevented the native terminal: %s %s", data, warning.String())
	}
}

func TestPreparePreservesNativeArgumentsAndPrivateAsset(t *testing.T) {
	input := []string{"/selected/pi", "--model", "local-model", "--extension", "/owner/custom.ts"}
	before := append([]string(nil), input...)
	argv, cleanup, err := Prepare(input)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if !reflect.DeepEqual(input, before) || !reflect.DeepEqual(argv[:len(input)], before) {
		t.Fatalf("native owner arguments changed: %q", argv)
	}
	if argv[len(input)] != "--extension" {
		t.Fatalf("missing explicit extension: %q", argv)
	}
	asset := argv[len(input)+1]
	info, err := os.Stat(asset)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("extension mode %o", info.Mode().Perm())
	}
	body, err := os.ReadFile(asset)
	if err != nil || !reflect.DeepEqual(body, extension) {
		t.Fatalf("extension asset mismatch: %v", err)
	}
	cleanup()
	if _, err := os.Stat(asset); !os.IsNotExist(err) {
		t.Fatalf("private asset leaked: %v", err)
	}
}

func TestPrepareRequiresExecutable(t *testing.T) {
	if _, _, err := Prepare(nil); err == nil {
		t.Fatal("accepted empty invocation")
	}
}

func TestPiExtensionNativeDeliveryContract(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is required for Pi's native extension contract")
	}
	cmd := exec.Command(node, "--test", "extension.test.mjs")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("Pi extension contract: %v\n%s", err, output)
	}
}

func TestInstalledPiNativeTUIBridge(t *testing.T) {
	if os.Getenv("AGENTMUX_PI_NATIVE_LIVE") != "1" {
		t.Skip("set AGENTMUX_PI_NATIVE_LIVE=1 for real Pi TUI with a fake provider and isolated profile")
	}
	binary, err := exec.LookPath("pi")
	if err != nil {
		t.Fatal(err)
	}
	if !probeVersion(context.Background(), []string{binary}, 2*time.Second) {
		t.Fatal("installed Pi does not satisfy the bounded native-delivery version probe")
	}
	resolved, err := filepath.EvalSymlinks(binary)
	if err != nil {
		t.Fatal(err)
	}
	packageRoot := filepath.Dir(filepath.Dir(filepath.Dir(resolved)))
	if override := os.Getenv("AGENTMUX_PI_PACKAGE"); override != "" {
		packageRoot = override
	}
	asset, err := filepath.Abs("extension.mjs")
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("python3", "testdata/native_tui.py", binary, packageRoot, asset, t.TempDir())
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("real Pi TUI: %v\n%s", err, output)
	} else {
		t.Logf("%s", output)
	}
}
