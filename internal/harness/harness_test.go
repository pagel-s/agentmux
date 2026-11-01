package harness

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func helperRequest(name, scenario string) Request {
	return Request{Harness: name, Prompt: "literal $(touch should-not-exist); `echo nope`\nnew line \u2028 preserved", Command: []string{os.Args[0], "-test.run=TestHarnessProcess", "--"}, Env: map[string]string{"AGENTMUX_HARNESS_HELPER": "1", "AGENTMUX_HARNESS_SCENARIO": scenario}}
}

// A real child process exercises pipes, argv, process exit, and cancellation;
// the tests never need a provider account or a model service.
func TestHarnessProcess(t *testing.T) {
	if os.Getenv("AGENTMUX_HARNESS_HELPER") != "1" {
		return
	}
	scenario := os.Getenv("AGENTMUX_HARNESS_SCENARIO")
	write := func(line string) { fmt.Fprintln(os.Stdout, line) }
	switch scenario {
	case "protocol-fixture":
		if os.Getenv("AGENTMUX_PROTOCOL_NAME") == "pi" {
			reader := bufio.NewScanner(os.Stdin)
			if !reader.Scan() || !reader.Scan() {
				os.Exit(8)
			}
		} else {
			_, _ = io.ReadAll(os.Stdin)
		}
		fmt.Fprint(os.Stdout, os.Getenv("AGENTMUX_PROTOCOL_LINES"))
		if os.Getenv("AGENTMUX_PROTOCOL_FAIL_EXIT") == "1" {
			os.Exit(11)
		}
	case "early-success":
		// A zero-exit wrapper can report completion before accepting stdin.
		write(`{"type":"thread.started","thread_id":"fixture-session"}`)
		write(`{"type":"item.completed","item":{"type":"agent_message","text":"unearned answer"}}`)
		write(`{"type":"turn.completed"}`)
	case "conversation-hook":
		_, _ = io.ReadAll(os.Stdin)
		_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"type": "result", "subtype": "success", "session_id": conversationTestID, "result": "checkpoint-answer"})
		_ = os.WriteFile(os.Getenv("AGENTMUX_PROCESS_FINISHED"), []byte("finished"), 0600)
	case "conversation-claude", "conversation-codex":
		_, _ = io.ReadAll(os.Stdin)
		id := conversationTestID
		if scenario == "conversation-claude" {
			for i, arg := range os.Args {
				if (arg == "--session-id" || arg == "--resume") && i+1 < len(os.Args) {
					id = os.Args[i+1]
				}
			}
		}
		file := id + ".jsonl"
		if scenario == "conversation-codex" {
			file = "rollout-test-" + id + ".jsonl"
		}
		if err := os.WriteFile(filepath.Join(os.Getenv("AGENTMUX_TEST_CONVERSATION_DIR"), file), []byte("{}\n"), 0600); err != nil {
			os.Exit(7)
		}
		if scenario == "conversation-claude" {
			_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"type": "result", "subtype": "success", "session_id": id, "is_error": false, "result": "persistent-answer"})
		} else {
			_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"type": "thread.started", "thread_id": id})
			write(`{"type":"item.completed","item":{"type":"agent_message","text":"persistent-answer"}}`)
			write(`{"type":"turn.completed","usage":{"input_tokens":1,"output_tokens":1}}`)
		}
	case "echo":
		data, _ := io.ReadAll(os.Stdin)
		_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"stdin": string(data), "argv": os.Args[3:]})
	case "huge":
		for range 300 {
			_, _ = io.WriteString(os.Stdout, strings.Repeat("x", 64*1024))
		}
	case "wait":
		time.Sleep(10 * time.Second)
	case "descendant":
		cmd := exec.Command(os.Args[0], "-test.run=TestHarnessProcess", "--")
		cmd.Env = mergeEnv(os.Environ(), map[string]string{"AGENTMUX_HARNESS_SCENARIO": "sentinel"})
		if err := cmd.Start(); err != nil {
			os.Exit(3)
		}
		fmt.Fprintln(os.Stdout, "started")
		time.Sleep(10 * time.Second)
	case "sentinel":
		time.Sleep(500 * time.Millisecond)
		_ = os.WriteFile(os.Getenv("AGENTMUX_HARNESS_SENTINEL"), []byte("orphan"), 0600)
	case "claude", "claude-error", "claude-denied":
		var input object
		_ = json.NewDecoder(os.Stdin).Decode(&input)
		text := stringField(objectField(input, "message"), "content")
		_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"type": "assistant", "session_id": "claude-1", "message": map[string]any{"content": []any{map[string]string{"type": "text", "text": text}}}})
		result := map[string]any{"type": "result", "subtype": "success", "session_id": "claude-1", "is_error": false, "result": text, "usage": map[string]int{"input_tokens": 4, "output_tokens": 5}}
		if scenario == "claude-error" {
			result["subtype"], result["is_error"], result["result"] = "error_max_turns", true, "turn limit"
		}
		if scenario == "claude-denied" {
			result["permission_denials"] = []any{map[string]string{"tool_name": "Bash"}}
		}
		_ = json.NewEncoder(os.Stdout).Encode(result)
	case "codex", "codex-error":
		_, _ = io.ReadAll(os.Stdin)
		write(`{"type":"thread.started","thread_id":"codex-1"}`)
		write(`{"type":"item.completed","item":{"type":"agent_message","text":"answer"}}`)
		if scenario == "codex-error" {
			write(`{"type":"turn.failed","error":{"message":"quota exhausted"}}`)
		} else {
			write(`{"type":"turn.completed","usage":{"input_tokens":1,"output_tokens":2}}`)
		}
	case "agy", "agy-denied", "agy-error":
		var input object
		_ = json.NewDecoder(os.Stdin).Decode(&input)
		if stringField(input, "event") != "user" {
			os.Exit(2)
		}
		write(`{"event":"init","conversation_id":"agy-1"}`)
		if scenario == "agy-denied" {
			fmt.Fprintln(os.Stderr, "tool run_command was soft-denied; approval required")
		}
		status := "SUCCESS"
		if scenario == "agy-error" {
			status = "WAITING"
		}
		_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"event": "result", "result": map[string]any{"status": status, "response": "answer", "conversation_id": "agy-1", "usage": map[string]int{"total_tokens": 3}}})
	case "pi", "pi-error", "pi-incomplete", "pi-config":
		reader := bufio.NewScanner(os.Stdin)
		if !reader.Scan() {
			os.Exit(4)
		}
		write(`{"type":"response","command":"get_state","id":"agentmux-state","success":true,"data":{"sessionId":"pi-1"}}`)
		if !reader.Scan() {
			os.Exit(4)
		}
		var input object
		_ = json.Unmarshal(reader.Bytes(), &input)
		if stringField(input, "type") != "prompt" {
			os.Exit(5)
		}
		write(`{"type":"response","command":"prompt","id":"agentmux-prompt","success":true,"data":{"disposition":"started"}}`)
		// A low-level error run can recover. agent_end must not close stdin.
		write(`{"type":"message_end","message":{"role":"assistant","content":[],"stopReason":"error","errorMessage":"retry me","usage":{"input":2,"output":0,"totalTokens":2}}}`)
		write(`{"type":"agent_end","willRetry":true}`)
		if scenario == "pi-incomplete" {
			os.Exit(0)
		}
		text := "answer"
		if scenario == "pi-config" {
			dir := os.Getenv("PI_CODING_AGENT_DIR")
			data, err := os.ReadFile(filepath.Join(dir, "models.json"))
			if err != nil || strings.Contains(string(data), "private-test-key") || !strings.Contains(string(data), "AGENTMUX_PI_API_KEY") {
				os.Exit(6)
			}
			text = dir
		}
		stopReason := "stop"
		if scenario == "pi-error" {
			stopReason = "error"
		}
		_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"type": "message_end", "message": map[string]any{"role": "assistant", "content": []any{map[string]string{"type": "text", "text": text}}, "stopReason": stopReason, "errorMessage": "provider failed", "usage": map[string]int{"input": 3, "output": 1, "totalTokens": 4}}})
		write(`{"type":"agent_settled","aborted":false}`)
		// Run must close the pipe after settled so this long-lived RPC exits.
		for reader.Scan() {
		}
	case "invalid":
		write("not-json")
	case "incomplete":
		write(`{"type":"system","session_id":"not-finished"}`)
	}
	os.Exit(0)
}

func TestCommandPreservesArgvAndPrompt(t *testing.T) {
	req := helperRequest("command", "echo")
	req.Command = append(req.Command, "space separated", "$(false)", "; echo nope")
	result, err := Run(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	var output struct {
		Stdin string   `json:"stdin"`
		Argv  []string `json:"argv"`
	}
	if err := json.Unmarshal([]byte(result.Text), &output); err != nil {
		t.Fatal(err)
	}
	if output.Stdin != req.Prompt || !reflect.DeepEqual(output.Argv, []string{"space separated", "$(false)", "; echo nope"}) {
		t.Fatalf("literal input changed: %#v", output)
	}
}

func TestStructuredAdapters(t *testing.T) {
	for _, test := range []struct{ name, scenario, session string }{{"claude", "claude", "claude-1"}, {"codex", "codex", "codex-1"}, {"antigravity", "agy", "agy-1"}, {"pi", "pi", "pi-1"}} {
		t.Run(test.name, func(t *testing.T) {
			req := helperRequest(test.name, test.scenario)
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			result, err := Run(ctx, req)
			if err != nil {
				t.Fatal(err)
			}
			if result.SessionID != test.session || len(result.Events) == 0 || len(result.Usage) == 0 {
				t.Fatalf("missing metadata: %#v", result)
			}
			if test.name == "claude" && result.Text != req.Prompt {
				t.Fatal("Claude JSON input did not preserve prompt")
			}
			if test.name == "pi" {
				var usage map[string]int
				_ = json.Unmarshal(result.Usage, &usage)
				if usage["totalTokens"] != 6 {
					t.Fatalf("Pi usage across retries = %s, want totalTokens 6", result.Usage)
				}
			}
		})
	}
}

func TestTerminalErrorsAndIncompleteStreams(t *testing.T) {
	for _, test := range []struct{ name, scenario, want string }{{"claude", "claude-error", "turn limit"}, {"codex", "codex-error", "quota exhausted"}, {"antigravity", "agy-error", "WAITING"}, {"pi", "pi-error", "provider failed"}, {"pi", "pi-incomplete", ErrIncomplete.Error()}, {"claude", "invalid", "invalid claude JSON"}, {"claude", "incomplete", ErrIncomplete.Error()}} {
		t.Run(test.scenario, func(t *testing.T) {
			_, err := Run(context.Background(), helperRequest(test.name, test.scenario))
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestSoftDenialsReturnPartialResult(t *testing.T) {
	for _, test := range []struct{ name, scenario string }{{"claude", "claude-denied"}, {"antigravity", "agy-denied"}} {
		t.Run(test.name, func(t *testing.T) {
			result, err := Run(context.Background(), helperRequest(test.name, test.scenario))
			if !errors.Is(err, ErrToolDenied) || result.Text == "" {
				t.Fatalf("result=%#v, error=%v", result, err)
			}
		})
	}
}

func TestOutputIsBounded(t *testing.T) {
	result, err := Run(context.Background(), helperRequest("command", "huge"))
	if !errors.Is(err, ErrOutputLimit) || len(result.Text) != maxOutputBytes {
		t.Fatalf("bytes=%d, error=%v", len(result.Text), err)
	}
}

func TestCancellation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := Run(ctx, helperRequest("command", "wait"))
	if !errors.Is(err, context.DeadlineExceeded) || time.Since(start) > time.Second {
		t.Fatalf("elapsed=%s, error=%v", time.Since(start), err)
	}
}

func TestPrivatePiConfigVisibleToWrapperAndRemoved(t *testing.T) {
	req := helperRequest("pi", "pi-config")
	req.BaseURL, req.Model, req.APIKeyEnv = "http://127.0.0.1:11434/v1", "local/model", "LOCAL_MODEL_TEST_KEY"
	req.Env[req.APIKeyEnv] = "private-test-key"
	var privateDir string
	req.Wrap = func(spec Spec) (Spec, error) {
		if len(spec.HomeFiles) != 1 || spec.HomeFiles[0].Target != "/home/agent/.pi/agent/models.json" || len(spec.ReadOnlyPaths) != 0 {
			t.Fatalf("Pi configuration must expose a file rather than a read-only agent directory")
		}
		privateDir = filepath.Dir(spec.HomeFiles[0].Source)
		info, err := os.Stat(filepath.Join(privateDir, "models.json"))
		if err != nil || info.Mode().Perm() != 0600 {
			t.Fatalf("private file mode: %v, %v", info, err)
		}
		return spec, nil
	}
	result, err := Run(context.Background(), req)
	if err != nil || result.Text != privateDir {
		t.Fatalf("result=%s, error=%v", result.Text, err)
	}
	if _, err := os.Stat(privateDir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("private endpoint directory was not removed: %v", err)
	}
}

func TestBuildEnvironmentAndValidation(t *testing.T) {
	t.Setenv("UNRELATED_OWNER_SECRET", "do-not-inherit")
	t.Setenv("AGENTMUX_KEY_SOURCE", "private-test-key")
	spec, err := Build(Request{Harness: "claude", BaseURL: "http://localhost:9999/anthropic", APIKeyEnv: "AGENTMUX_KEY_SOURCE"})
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(spec.Env, "\n")
	if strings.Contains(joined, "UNRELATED_OWNER_SECRET") || !strings.Contains(joined, "ANTHROPIC_API_KEY=private-test-key") || !strings.Contains(joined, "ANTHROPIC_BASE_URL=http://localhost:9999/anthropic") {
		t.Fatalf("environment was not reduced or selected credentials were not mapped")
	}
	if strings.Contains(strings.Join(spec.Args, " "), "private-test-key") {
		t.Fatal("credential appears in process argv")
	}
	if !strings.Contains(strings.Join(spec.Args, " "), "--safe-mode") {
		t.Fatal("Claude managed turns must disable ambient hooks/plugins while preserving native authentication")
	}
	for _, req := range []Request{{Harness: "unknown"}, {Harness: "command"}, {Harness: "command", Command: []string{"echo"}, Model: "model"}, {Harness: "antigravity", BaseURL: "http://localhost:1234/v1"}, {Harness: "pi", BaseURL: "http://localhost:1234/v1"}, {Harness: "claude", APIKeyEnv: "not a name"}, {Harness: "claude", BaseURL: "https://user:secret@example.com"}, {Harness: "claude", Env: map[string]string{"BAD=KEY": "x"}}} {
		if _, err := Build(req); err == nil {
			t.Fatalf("accepted invalid request %#v", req)
		}
	}
}
