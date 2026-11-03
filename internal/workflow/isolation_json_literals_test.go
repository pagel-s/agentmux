package workflow

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"agentmux/internal/harness"
)

func isolationJSONLiteralContext(values ...string) context.Context {
	return context.WithValue(context.Background(), isolationContextKey{}, &isolationRuntime{secrets: map[string][]string{"writer": values}})
}

// Decode strings as well as checking encoded bytes: JSON escaping is not an
// acceptable way to retain a selected credential.
func assertIsolationJSONExcludes(t *testing.T, data []byte, secrets ...string) any {
	t.Helper()
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		t.Fatalf("retained evidence is invalid JSON: %v", err)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		t.Fatal("retained evidence contains more than one JSON value")
	}
	check := func(text string) {
		t.Helper()
		for _, secret := range secrets {
			if strings.Contains(text, secret) {
				t.Fatal("retained evidence contains a selected credential")
			}
		}
	}
	check(string(data))
	var walk func(any)
	walk = func(current any) {
		switch current := current.(type) {
		case map[string]any:
			for key, child := range current {
				check(key)
				walk(child)
			}
		case []any:
			for _, child := range current {
				walk(child)
			}
		case string:
			check(current)
		case json.Number:
			check(current.String())
		}
	}
	walk(value)
	return value
}

func assertIsolationJSONNumber(t *testing.T, value any, key, want string) {
	t.Helper()
	object, ok := value.(map[string]any)
	if !ok {
		t.Fatal("expected an evidence object")
	}
	got, ok := object[key].(json.Number)
	if !ok || got.String() != want {
		t.Fatalf("unrelated numeric field %s changed type or value: %v", key, object[key])
	}
}

func TestIsolationRedactionJSONNumericLiterals(t *testing.T) {
	for _, fixture := range []struct {
		name, secret, literals string
	}{
		{"integer", "918273", `918273,-918273.5,19182734,1e918273`},
		{"decimal", "1.25", `1.25,-1.25,11.25`},
		{"exponent", "9e3", `9e3,-9e3,19e3`},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			ctx := isolationJSONLiteralContext(fixture.secret)
			raw := json.RawMessage(`{"selected":[` + fixture.literals + `],"input_tokens":23,"output_tokens":45}`)
			result, changed := RedactIsolationResult(ctx, "other-step", harness.Result{Usage: append(json.RawMessage(nil), raw...), Events: []json.RawMessage{append(json.RawMessage(nil), raw...)}})
			if !changed {
				t.Fatal("numeric selected credential was not marked redacted")
			}
			for _, data := range []json.RawMessage{result.Usage, result.Events[0]} {
				value := assertIsolationJSONExcludes(t, data, fixture.secret)
				assertIsolationJSONNumber(t, value, "input_tokens", "23")
				assertIsolationJSONNumber(t, value, "output_tokens", "45")
			}
		})
	}
}

func TestIsolationRedactionDecodesEscapedJSONStrings(t *testing.T) {
	const secret = "escaped-secret"
	raw := json.RawMessage(`{"selected":"\u0065scaped-secret","input_tokens":23}`)
	result, changed := RedactIsolationResult(isolationJSONLiteralContext(secret), "writer", harness.Result{Usage: raw, Events: []json.RawMessage{append(json.RawMessage(nil), raw...)}})
	if !changed {
		t.Fatal("escaped selected credential was not marked redacted")
	}
	for _, data := range []json.RawMessage{result.Usage, result.Events[0]} {
		value := assertIsolationJSONExcludes(t, data, secret)
		assertIsolationJSONNumber(t, value, "input_tokens", "23")
	}
}

func TestIsolationJSONEscapedSchemaKeyRefused(t *testing.T) {
	const secret = "escaped-secret"
	ctx := isolationJSONLiteralContext(secret)
	raw := json.RawMessage(`{"\u0065scaped-secret":23}`)
	clean, changed, err := redactIsolationNativeResult(ctx, harness.Result{Events: []json.RawMessage{raw}})
	if err == nil || !changed || strings.Contains(err.Error(), secret) || len(clean.Events) != 0 {
		t.Fatal("escaped credential schema key was not safely refused")
	}
	path := filepath.Join(t.TempDir(), "native-events.jsonl")
	if digest, err := writeIsolationNativeEvents(ctx, path, []json.RawMessage{raw}); err == nil || digest != "" || strings.Contains(err.Error(), secret) {
		t.Fatal("escaped credential schema key passed the native publication gate")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("escaped credential schema key was retained")
	}
}

func TestIsolationRedactionJSONBooleanAndNullLiterals(t *testing.T) {
	for _, secret := range []string{"true", "false", "null"} {
		t.Run(secret, func(t *testing.T) {
			raw := json.RawMessage(`{"selected":` + secret + `,"input_tokens":23,"output_tokens":45}`)
			result, changed := RedactIsolationResult(isolationJSONLiteralContext(secret), "writer", harness.Result{Usage: raw, Events: []json.RawMessage{append(json.RawMessage(nil), raw...)}})
			if !changed {
				t.Fatal("JSON literal selected credential was not marked redacted")
			}
			for _, data := range []json.RawMessage{result.Usage, result.Events[0]} {
				value := assertIsolationJSONExcludes(t, data, secret)
				assertIsolationJSONNumber(t, value, "input_tokens", "23")
				assertIsolationJSONNumber(t, value, "output_tokens", "45")
			}
		})
	}
}

func TestIsolationRedactionMarkerCannotRetainSelectedCredential(t *testing.T) {
	for _, secret := range []string{"r", "redacted", "credential", "[", "]"} {
		t.Run(fmt.Sprintf("%x", secret), func(t *testing.T) {
			raw, err := json.Marshal(map[string]string{"value": secret})
			if err != nil {
				t.Fatal(err)
			}
			result, changed := RedactIsolationResult(isolationJSONLiteralContext(secret), "writer", harness.Result{Text: secret, SessionID: secret, Usage: raw, Events: []json.RawMessage{append(json.RawMessage(nil), raw...)}})
			if !changed || strings.Contains(result.Text, secret) || strings.Contains(result.SessionID, secret) {
				t.Fatal("redaction marker retained a selected credential")
			}
			assertIsolationJSONExcludes(t, result.Usage, secret)
			assertIsolationJSONExcludes(t, result.Events[0], secret)
		})
	}
}

func TestIsolationJSONTypedCredentialRefusalPreservesFailedVerification(t *testing.T) {
	for _, secret := range []string{"918273", "false", "true", "null", "passed", "redacted", "e"} {
		t.Run(secret, func(t *testing.T) {
			ctx := isolationJSONLiteralContext(secret)
			verification := &Verification{Repository: "fixture", Tree: strings.Repeat("d", 40), ContentSHA256: strings.Repeat("d", 64), Passed: false, Checks: []CheckResult{{Name: "check", Command: []string{"fixture"}, Passed: false, ExitCode: 7, Output: secret}}}
			report := Report{State: "failed", Error: secret, Candidates: []CandidateResult{{ID: "writer", State: "failed", Error: secret, Verification: verification}}}
			if secret == "918273" {
				report.Candidates[0].DurationMillis = 918273
			}
			path := filepath.Join(t.TempDir(), "evidence.json")
			err := saveIsolationJSON(ctx, path, &report)
			if verification.Passed {
				t.Fatal("credential redaction promoted failed evidence to success")
			}
			for _, candidate := range report.Candidates {
				if candidate.Verification != nil {
					if candidate.Verification.Passed {
						t.Fatal("credential redaction promoted failed verification to success")
					}
					for _, check := range candidate.Verification.Checks {
						if check.Passed {
							t.Fatal("credential redaction promoted a failed check to success")
						}
					}
				}
			}
			if err != nil {
				if strings.Contains(err.Error(), secret) {
					t.Fatal("publication refusal diagnostic retained a selected credential")
				}
				if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
					t.Fatal("refused publication retained an evidence file")
				}
				return
			}
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			assertIsolationJSONExcludes(t, data, secret)
			var saved Report
			if err := json.Unmarshal(data, &saved); err != nil || saved.Candidates[0].State != "failed" || saved.Candidates[0].Verification != nil && saved.Candidates[0].Verification.Passed {
				t.Fatal("saved credential redaction promoted failed evidence to success")
			}
		})
	}
}

func TestIsolationJSONPersistenceRedactsNumericUsage(t *testing.T) {
	const secret = "918273"
	usage := json.RawMessage(`{"input_tokens":918273,"output_tokens":23,"cached_tokens":45}`)
	candidate := CandidateResult{ID: "writer", State: "failed", Usage: usage, UsageRecords: []AttemptUsage{{Attempt: 1, Usage: append(json.RawMessage(nil), usage...)}}}
	path := filepath.Join(t.TempDir(), "evidence.json")
	if err := saveIsolationJSON(isolationJSONLiteralContext(secret), path, &candidate); err != nil {
		t.Fatal(err)
	}
	if !candidate.CredentialsRedacted {
		t.Fatal("final publication boundary did not label numeric usage redaction")
	}
	for _, raw := range []json.RawMessage{candidate.Usage, candidate.UsageRecords[0].Usage} {
		value := assertIsolationJSONExcludes(t, raw, secret)
		assertIsolationJSONNumber(t, value, "output_tokens", "23")
		assertIsolationJSONNumber(t, value, "cached_tokens", "45")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	assertIsolationJSONExcludes(t, data, secret)
}

// This is an actual child process speaking the native Codex JSONL protocol;
// it does not start an installed provider or contact a model service.
func TestIsolationJSONCredentialNativeHelperProcess(t *testing.T) {
	if os.Getenv("AGENTMUX_JSON_CREDENTIAL_HELPER") != "1" {
		return
	}
	if _, err := io.Copy(io.Discard, os.Stdin); err != nil {
		os.Exit(8)
	}
	fmt.Fprintln(os.Stdout, `{"type":"thread.started","thread_id":"fixture-session"}`)
	fmt.Fprintln(os.Stdout, `{"type":"item.completed","item":{"type":"agent_message","text":"fixture answer"}}`)
	fmt.Fprintln(os.Stdout, `{"type":"turn.completed","usage":{"input_tokens":918273,"output_tokens":23,"cached_tokens":45}}`)
	os.Exit(0)
}

func TestIsolationJSONNativeCredentialPersistence(t *testing.T) {
	const secret = "918273"
	ctx, cancel := context.WithTimeout(isolationJSONLiteralContext(secret), 10*time.Second)
	defer cancel()
	native, err := harness.Run(ctx, harness.Request{Harness: "codex", Model: "fixture-model", Prompt: "Return the fixture answer.", Workdir: t.TempDir(), Command: []string{os.Args[0], "-test.run=^TestIsolationJSONCredentialNativeHelperProcess$", "--"}, Env: map[string]string{"AGENTMUX_JSON_CREDENTIAL_HELPER": "1", "OPENAI_API_KEY": secret}})
	if err != nil || native.Text != "fixture answer" || len(native.Events) != 3 {
		t.Fatalf("native protocol fixture did not complete: events=%d err=%v", len(native.Events), err)
	}
	native, changed := RedactIsolationResult(ctx, "writer", native)
	if !changed {
		t.Fatal("native numeric usage credential was not marked redacted")
	}
	for _, raw := range native.Events {
		assertIsolationJSONExcludes(t, raw, secret)
	}
	usage := assertIsolationJSONExcludes(t, native.Usage, secret)
	assertIsolationJSONNumber(t, usage, "output_tokens", "23")
	assertIsolationJSONNumber(t, usage, "cached_tokens", "45")
	eventsPath := filepath.Join(t.TempDir(), "native-events.jsonl")
	digest, err := writeIsolationNativeEvents(ctx, eventsPath, native.Events)
	if err != nil {
		t.Fatal(err)
	}
	eventData, err := os.ReadFile(eventsPath)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(eventData)
	if digest != hex.EncodeToString(sum[:]) {
		t.Fatal("native JSONL digest does not identify retained bytes")
	}
	for _, line := range bytes.Split(bytes.TrimSpace(eventData), []byte{'\n'}) {
		assertIsolationJSONExcludes(t, line, secret)
	}
	if info, err := os.Stat(eventsPath); err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("native JSONL evidence was not retained privately")
	}
	report := Report{State: "completed", Candidates: []CandidateResult{{ID: "writer", State: "completed", CredentialsRedacted: changed, Result: native.Text, Usage: native.Usage, UsageRecords: []AttemptUsage{{Attempt: 1, Usage: native.Usage}}}}}
	path := filepath.Join(t.TempDir(), "report.json")
	if err := saveIsolationJSON(ctx, path, &report); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	assertIsolationJSONExcludes(t, data, secret)
}

func TestIsolationJSONNativePublicationGateRefusesResidualCredentials(t *testing.T) {
	const secret = "918273"
	for _, frame := range []json.RawMessage{
		json.RawMessage(`{"usage":{"input_tokens":918273}}`),
		json.RawMessage(`{"text":"\u003918273"}`),
		json.RawMessage(`{"usage":`),
	} {
		t.Run(fmt.Sprintf("%x", frame), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "native-events.jsonl")
			digest, err := writeIsolationNativeEvents(isolationJSONLiteralContext(secret), path, []json.RawMessage{frame})
			if err == nil || digest != "" || strings.Contains(err.Error(), secret) {
				t.Fatal("native JSONL publication gate did not safely refuse unsafe evidence")
			}
			if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Fatal("native JSONL publication gate retained refused evidence")
			}
		})
	}
}
