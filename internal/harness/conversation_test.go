package harness

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const conversationTestID = "d7f7211a-9471-43a5-841b-ae63320164a2"

func TestExplicitFreshConversationRejectsNativeHistorySelection(t *testing.T) {
	for _, name := range []string{"claude", "codex", "pi", "antigravity"} {
		for _, mode := range []string{"", "fresh"} {
			flags := []string{"--continue", "--resume=" + conversationTestID, "--conversation", "--conversation=" + conversationTestID, "--conversation-id", "--session", "--session-id", "--session-dir", "--fork", "--fork-session", "--from-pr=123", "--teleport", "--cloud", "--remote", "--attach", "--attach-session", "--bg", "--background", "--desktop", "--remote-control", "--environment", "--tmux", "--worktree", "--ide", "--chrome", "--cd=/tmp", "--cwd", "--directory", "--project", "--new-project", "-C", "-w"}
			if name == "codex" {
				flags = append(flags, "resume", "fork", "queue", "apply", "exec", "e", "app-server", "-C/tmp/wrong-project", "-C=/tmp/wrong-project", "-cmodel=other", "-vw/tmp/wrong-project")
			}
			if name == "claude" {
				flags = append(flags, "attach", "respawn", "logs", "agents", "ultrareview")
			}
			if name == "antigravity" {
				flags = append(flags, "agent", "remote-control", "--prompt-interactive", "-i")
			}
			if name == "pi" {
				flags = append(flags, "auth", "config")
			}
			if name != "codex" {
				flags = append(flags, "-c", "-r"+conversationTestID, "-pc", "-pr"+conversationTestID, "-hpc", "-ppc")
			}
			for _, flag := range flags {
				t.Run(name+"/"+mode+"/"+flag, func(t *testing.T) {
					_, err := Build(Request{Harness: name, Command: []string{"fixture-native", flag}, Conversation: &Conversation{Mode: mode}})
					if err == nil || !strings.Contains(err.Error(), "conversation selection") {
						t.Fatalf("fresh session admitted native history control: %v", err)
					}
				})
			}
		}
	}
	spec, err := Build(Request{Harness: "codex", Command: []string{"codex", "-c", "model_reasoning_effort=low"}, Conversation: &Conversation{Mode: "fresh"}})
	if err != nil || !strings.Contains(strings.Join(spec.Args, " "), "--ephemeral") {
		t.Fatalf("fresh Codex lost ephemeral execution or legitimate config: %+v %v", spec, err)
	}
	if _, err := Build(Request{Harness: "command", Command: []string{"python3", "-c", "print('review')"}, Conversation: &Conversation{Mode: "fresh"}}); err != nil {
		t.Fatal("fresh process fixture must retain its ordinary argv:", err)
	}
	if _, err := Build(Request{Harness: "antigravity", Command: []string{"agy", "--continue"}}); err != nil {
		t.Fatal("nil trusted-local policy compatibility changed:", err)
	}
}

func conversationRequest(t *testing.T, name, mode string) Request {
	t.Helper()
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	return Request{Harness: name, Conversation: &Conversation{Mode: mode, Dir: dir}, Wrap: func(spec Spec) (Spec, error) { return spec, nil }}
}

func TestPersistentConversationBuildUsesExactProviderControls(t *testing.T) {
	for _, name := range []string{"claude", "codex", "pi"} {
		t.Run(name, func(t *testing.T) {
			req := conversationRequest(t, name, "create")
			if name != "codex" {
				req.Conversation.ID = conversationTestID
			}
			spec, err := Build(req)
			if err != nil {
				t.Fatal(err)
			}
			if len(spec.SessionDirs) != 1 || spec.SessionDirs[0].Source != req.Conversation.Dir || spec.SessionDirs[0].Target != conversationTarget(name) {
				t.Fatal("missing narrowly selected private session directory")
			}
			args := strings.Join(spec.Args, " ")
			if strings.Contains(args, "--no-session") {
				t.Fatalf("persistence remained disabled: %s", args)
			}
			if name != "codex" && !strings.Contains(args, "--session-id "+conversationTestID) {
				t.Fatal("create did not bind an exact session ID")
			}
			filename := conversationTestID + ".jsonl"
			if name == "codex" {
				filename = "rollout-2026-10-31-" + conversationTestID + ".jsonl"
			}
			if name == "pi" {
				filename = "2026-10-31_" + conversationTestID + ".jsonl"
			}
			if err := os.WriteFile(filepath.Join(req.Conversation.Dir, filename), []byte("{}\n"), 0600); err != nil {
				t.Fatal(err)
			}
			req.Conversation.Mode, req.Conversation.ID = "resume", conversationTestID
			spec, err = Build(req)
			if err != nil {
				t.Fatal(err)
			}
			args = strings.Join(spec.Args, " ")
			if name == "codex" && !strings.Contains(args, "--sandbox workspace-write resume "+conversationTestID+" -") {
				t.Fatalf("Codex parent options were not retained before resume: %s", args)
			}
			if name == "claude" && !strings.Contains(args, "--resume "+conversationTestID) {
				t.Fatal("Claude exact resume is absent")
			}
			if name == "pi" && !strings.Contains(args, "--session "+filepath.Join(conversationTarget(name), filename)) {
				t.Fatal("Pi must resume an exact existing file")
			}
		})
	}
}

func TestMissingAndUnsupportedConversationsNeverSilentlyStartFresh(t *testing.T) {
	for _, name := range []string{"claude", "codex", "pi"} {
		req := conversationRequest(t, name, "resume")
		req.Conversation.ID = conversationTestID
		if _, err := Build(req); !errors.Is(err, ErrConversationMissing) {
			t.Fatalf("%s missing resume did not fail: %v", name, err)
		}
	}
	for _, name := range []string{"antigravity", "command"} {
		req := conversationRequest(t, name, "create")
		req.Command = []string{"/bin/cat"}
		if _, err := Build(req); !errors.Is(err, ErrConversationUnsupported) {
			t.Fatalf("%s fabricated persistence support: %v", name, err)
		}
	}
	req := conversationRequest(t, "pi", "create")
	req.Command = []string{"pi", "--no-session"}
	if _, err := Build(req); err == nil {
		t.Fatal("owner prefix silently disabled managed persistence")
	}
}

func TestConversationBuildIsNonmutatingAndNativeRefsAreVerified(t *testing.T) {
	req := conversationRequest(t, "claude", "create")
	if _, err := Build(req); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(req.Conversation.Dir)
	if err != nil || len(entries) != 0 || req.Conversation.ID != "" {
		t.Fatal("preflight created native session data or changed the caller's request")
	}
	c := &Conversation{Mode: "resume", Dir: req.Conversation.Dir, ID: conversationTestID}
	if err := verifyConversation("claude", c, "12345678-1234-1234-1234-123456789abc"); !errors.Is(err, ErrConversationMismatch) {
		t.Fatal("wrong native ID accepted")
	}
	if err := verifyConversation("claude", c, conversationTestID); !errors.Is(err, ErrConversationMissing) {
		t.Fatal("missing completed transcript accepted")
	}
	if err := os.WriteFile(filepath.Join(c.Dir, c.ID+".jsonl"), []byte("{}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := verifyConversation("claude", c, conversationTestID); err != nil {
		t.Fatal(err)
	}
}

func TestPersistentConversationRunLearnsNativeIDAndResumesExplicitly(t *testing.T) {
	for _, name := range []string{"claude", "codex"} {
		t.Run(name, func(t *testing.T) {
			req := helperRequest(name, "conversation-"+name)
			dir := t.TempDir()
			if err := os.Chmod(dir, 0700); err != nil {
				t.Fatal(err)
			}
			req.Conversation = &Conversation{Mode: "create", Dir: dir}
			req.Env["AGENTMUX_TEST_CONVERSATION_DIR"] = dir
			req.Wrap = func(spec Spec) (Spec, error) { return spec, nil }
			first, err := Run(context.Background(), req)
			if err != nil || !validConversationID(name, first.SessionID) || first.Text != "persistent-answer" {
				t.Fatalf("create failed: %v %+v", err, first)
			}
			if req.Conversation.ID != "" {
				t.Fatal("Run mutated the caller's session reference")
			}
			req.Conversation = &Conversation{Mode: "resume", Dir: dir, ID: first.SessionID}
			second, err := Run(context.Background(), req)
			if err != nil || second.SessionID != first.SessionID {
				t.Fatalf("exact native resume failed: %v", err)
			}
		})
	}
}

func TestTrustedLocalConversationKeepsNativeStorageWithoutPrivateMounts(t *testing.T) {
	for _, name := range []string{"claude", "codex"} {
		t.Run(name, func(t *testing.T) {
			home := t.TempDir()
			data := filepath.Join(home, "."+name, "projects")
			filename := conversationTestID + ".jsonl"
			if name == "codex" {
				data = filepath.Join(home, ".codex", "sessions")
				filename = "rollout-local-" + conversationTestID + ".jsonl"
			}
			if err := os.MkdirAll(data, 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(data, filename), []byte("{}\n"), 0600); err != nil {
				t.Fatal(err)
			}
			spec, err := Build(Request{Harness: name, Env: map[string]string{"HOME": home}, Conversation: &Conversation{Mode: "resume", ID: conversationTestID}})
			if err != nil || len(spec.SessionDirs) != 0 || spec.conversation.Dir != data {
				t.Fatalf("trusted native storage was redirected or could not resume: %v", err)
			}
		})
	}
}
