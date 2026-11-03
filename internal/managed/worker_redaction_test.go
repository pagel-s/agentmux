package managed

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"agentmux/internal/harness"
	"agentmux/internal/managedconfig"
)

func TestWorkerJSONRedactionDecodesEscapesAndPreservesAccounting(t *testing.T) {
	const secret = "escaped-native-secret-817425"
	for _, raw := range []string{
		`{"value":"\u0065scaped-native-secret-817425","input_tokens":9007199254740993,"fraction":1.2500e-9}`,
		`{"nested":[{"value":"prefix escaped-native-secret-817425 suffix"}],"input_tokens":9007199254740993,"fraction":1.2500e-9}`,
	} {
		clean, err := workerJSONResult(json.RawMessage(raw), harness.Request{}, "", 16384, secret)
		if err != nil {
			t.Fatal(err)
		}
		var decoded any
		if json.Unmarshal(clean, &decoded) != nil {
			t.Fatal("redacted native evidence is invalid JSON")
		}
		canonical, _ := json.Marshal(decoded)
		if strings.Contains(string(canonical), secret) || !bytes.Contains(clean, []byte("9007199254740993")) || !bytes.Contains(clean, []byte("1.2500e-9")) {
			t.Fatalf("credential leaked or accounting changed: %s", clean)
		}
	}
	const htmlSecret = "native<&>\"\\credential"
	raw, _ := json.Marshal(map[string]string{"value": htmlSecret})
	clean, err := workerJSONResult(raw, harness.Request{}, "", 16384, htmlSecret)
	var decoded map[string]string
	if err != nil || json.Unmarshal(clean, &decoded) != nil || strings.Contains(decoded["value"], htmlSecret) {
		t.Fatalf("JSON/HTML escaped credential was retained: %s %v", clean, err)
	}
	unchanged := json.RawMessage(` {"usage": [9007199254740993, 1.2500e-9],"value":"ordinary"} `)
	clean, err = workerJSONResult(unchanged, harness.Request{}, "", 16384, secret)
	if err != nil || !bytes.Equal(unchanged, clean) {
		t.Fatalf("unaffected evidence bytes changed: %s %v", clean, err)
	}
}

func TestWorkerJSONRedactionRefusesCredentialKeysAndCoversPrimitiveValues(t *testing.T) {
	const secret = "escaped-native-secret-817425"
	if clean, err := workerJSONResult(json.RawMessage(`{"\u0065scaped-native-secret-817425":"data"}`), harness.Request{}, "", 16384, secret); !errors.Is(err, errWorkerEvidenceRedaction) || len(clean) != 0 {
		t.Fatalf("credential-bearing schema key was retained: %s %v", clean, err)
	}
	for _, primitive := range []string{"918273", "true", "false", "null"} {
		raw := json.RawMessage(`{"value":` + primitive + `,"input_tokens":23}`)
		clean, err := workerJSONResult(raw, harness.Request{}, "", 16384, primitive)
		if err != nil || !json.Valid(clean) || strings.Contains(string(clean), primitive) || !bytes.Contains(clean, []byte(`"input_tokens":23`)) {
			t.Fatalf("primitive credential was retained or unrelated accounting changed: %s %v", clean, err)
		}
	}
	for _, secret := range []string{"r", "redacted", "[", "]"} {
		if clean := workerResult(secret, harness.Request{}, "", 1024, secret); strings.Contains(clean, secret) {
			t.Fatalf("redaction marker retained credential %q", secret)
		}
	}
	if clean := workerResult(strings.Repeat("ordinary ", 100), harness.Request{}, "", 32, "truncated"); strings.Contains(clean, "truncated") || len(clean) > 32 {
		t.Fatalf("truncation marker introduced a known credential: %q", clean)
	}
	if clean := workerResult(string([]byte{0xff}), harness.Request{}, "", 32, "\uFFFD"); strings.Contains(clean, "\uFFFD") {
		t.Fatalf("UTF-8 repair introduced a known credential: %q", clean)
	}
}

// A real native-protocol child exercises the OnEvent/usage/session publication
// boundaries without a provider account or network access.
func TestWorkerRedactionNativeHelper(t *testing.T) {
	if os.Getenv("AMX_REDACTION_NATIVE_HELPER") != "1" {
		return
	}
	_, _ = io.ReadAll(os.Stdin)
	fmt.Fprint(os.Stdout, os.Getenv("AMX_REDACTION_NATIVE_FRAMES"))
	os.Exit(0)
}

func TestWorkerNativeEvidenceRedactsEscapedSecretsAndRefusesUnsafeIdentity(t *testing.T) {
	const secret = "escaped-native-secret-817425"
	for _, scenario := range []string{"escaped value", "escaped key", "native identity"} {
		t.Run(scenario, func(t *testing.T) {
			store, owner, credential := workerTestStore(t, []string{"/bin/cat"}, "local")
			task, lease := workerTestTask(t, store, owner, credential)
			identity := "safe-native-id"
			usage := `{"input_tokens":23,"diagnostic":"\u0065scaped-native-secret-817425"}`
			if scenario == "escaped key" {
				usage = `{"\u0065scaped-native-secret-817425":"diagnostic"}`
			}
			if scenario == "native identity" {
				identity = secret
				usage = `{"input_tokens":23}`
			}
			frames := `{"type":"thread.started","thread_id":"` + identity + `"}` + "\n" +
				`{"type":"item.completed","item":{"type":"agent_message","text":"answer"}}` + "\n" +
				`{"type":"turn.completed","usage":` + usage + `}` + "\n"
			req := harness.Request{Harness: "codex", Command: []string{os.Args[0], "-test.run=TestWorkerRedactionNativeHelper", "--"}, Prompt: "fixture task", Workdir: store.cfg.Agents["worker"].Workdir, Env: map[string]string{"AMX_REDACTION_NATIVE_HELPER": "1", "AMX_REDACTION_NATIVE_FRAMES": frames}}
			trace, err := beginTrace(store.dir, "worker", task.ID, managedconfig.Trace{MaxTurnBytes: 16384, MaxTotalBytes: 32768}, map[string]string{"input": "fixture input"})
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := executeWorkerTaskTrace(ctx, workerTestCaller(store, credential), req, task, lease, credential.Token, 1024, trace, secret); err != nil {
				t.Fatal(err)
			}
			current, err := workerGetTask(ctx, workerTestCaller(store, credential), task.ID)
			if err != nil {
				t.Fatal(err)
			}
			expected := "failed"
			if scenario == "escaped value" {
				expected = "submitted"
			}
			if current.State != expected || current.Trace == nil {
				t.Fatalf("task state/evidence: %+v", current)
			}
			raw, err := os.ReadFile(filepath.Join(store.dir, "traces", "worker", current.Trace.ID+".jsonl"))
			if err != nil {
				t.Fatal(err)
			}
			for _, line := range bytes.Split(bytes.TrimSpace(raw), []byte("\n")) {
				var record TraceRecord
				if json.Unmarshal(line, &record) != nil {
					t.Fatal("invalid trace record")
				}
				var event any
				if json.Unmarshal(record.Data, &event) != nil {
					t.Fatal("invalid trace event")
				}
				decoded, _ := json.Marshal(event)
				if bytes.Contains(decoded, []byte(secret)) {
					t.Fatalf("known credential retained in native evidence: %s", decoded)
				}
			}
		})
	}
}
