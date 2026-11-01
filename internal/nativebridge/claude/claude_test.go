//go:build unix

package claude

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"agentmux/internal/nativebridge"
)

func TestOverlayPreservesCallerSettingsHooksAndArgumentBoundary(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "caller settings.json")
	initial := `{"model":"opus","permissions":{"deny":["Bash(rm *)"]},"hooks":{"SessionStart":[{"hooks":[{"type":"command","command":"existing hook"}]}],"Stop":[{"hooks":[{"type":"command","command":"existing stop"}]}]}}`
	if err := os.WriteFile(source, []byte(initial), 0600); err != nil {
		t.Fatal(err)
	}
	args, err := overlaySettings([]string{"--mcp-config", "original.json", "--settings", source, "--resume", "native-id", "--", "--settings=literal prompt"}, "/absolute/helper with spaces#", dir)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"--mcp-config", "original.json", "--resume", "native-id", "--settings", filepath.Join(dir, "settings.json"), "--", "--settings=literal prompt"}
	if !reflect.DeepEqual(args, want) {
		t.Fatalf("arguments changed: %#v", args)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "settings.json"))
	var settings struct {
		Model       string
		Permissions map[string][]string
		Hooks       map[string][]struct {
			Hooks []struct {
				Type, Command string
				Args          []string
				Async         bool
			}
		}
	}
	if err := json.Unmarshal(data, &settings); err != nil {
		t.Fatal(err)
	}
	if settings.Model != "opus" || !reflect.DeepEqual(settings.Permissions["deny"], []string{"Bash(rm *)"}) || len(settings.Hooks["SessionStart"]) != 2 || len(settings.Hooks["Stop"]) != 1 {
		t.Fatalf("settings lost: %s", data)
	}
	ours := settings.Hooks["SessionStart"][1].Hooks[0]
	if ours.Command != "/absolute/helper with spaces#" || !ours.Async || !reflect.DeepEqual(ours.Args, []string{"managed", "native-hook", "claude"}) {
		t.Fatalf("unsafe hook: %+v", ours)
	}
	st, _ := os.Stat(filepath.Join(dir, "settings.json"))
	if st.Mode().Perm() != 0600 {
		t.Fatalf("settings mode: %v", st.Mode())
	}
	unchanged, _ := os.ReadFile(source)
	if string(unchanged) != initial {
		t.Fatal("caller settings were modified")
	}
}

func TestRunKeepsNativeInputAndConfigurationWithoutPromptSubmission(t *testing.T) {
	dir := t.TempDir()
	provider := filepath.Join(dir, "fake claude")
	script := `#!/bin/sh
if [ "$1" = --version ]; then printf '2.1.316 (Claude Code)\n'; exit 0; fi
printf '%s\n' "$@" > "$FIXTURE_DIR/args"
printf '%s\n' "$AGENTMUX_CLAUDE_BRIDGE_DIR" > "$FIXTURE_DIR/dir"
printf '%s\n' "$UNRELATED_NATIVE_SETTING" > "$FIXTURE_DIR/inherited"
cat > "$FIXTURE_DIR/input"
`
	if err := os.WriteFile(provider, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AGENTMUX_CLI", provider)
	t.Setenv("FIXTURE_DIR", dir)
	t.Setenv("UNRELATED_NATIVE_SETTING", "native MCPs and settings")
	inPath := filepath.Join(dir, "draft")
	if err := os.WriteFile(inPath, []byte("human input unchanged\n"), 0600); err != nil {
		t.Fatal(err)
	}
	in, err := os.Open(inPath)
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()
	previous := os.Stdin
	os.Stdin = in
	defer func() { os.Stdin = previous }()
	if err := Run(context.Background(), []string{provider, "--mcp-config", "personal.json", "--resume", "keep-native-id"}, nil); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "input"))
	if string(data) != "human input unchanged\n" {
		t.Fatalf("native input changed: %q", data)
	}
	data, _ = os.ReadFile(filepath.Join(dir, "args"))
	if !strings.HasPrefix(string(data), "--mcp-config\npersonal.json\n--resume\nkeep-native-id\n--settings\n") || strings.Contains(string(data), "--strict-mcp-config") || strings.Contains(string(data), "--append-system-prompt") {
		t.Fatalf("native argv changed: %s", data)
	}
	data, _ = os.ReadFile(filepath.Join(dir, "inherited"))
	if string(data) != "native MCPs and settings\n" {
		t.Fatal("native environment not preserved")
	}
	data, _ = os.ReadFile(filepath.Join(dir, "dir"))
	if _, err := os.Stat(strings.TrimSpace(string(data))); !os.IsNotExist(err) {
		t.Fatal("bridge lifetime files outlived provider")
	}
}

func TestVersionGateRejectsUnknownOrOldClaude(t *testing.T) {
	for _, version := range []string{"2.1.247 (Claude Code)", "1.99.999", "unrecognized"} {
		t.Run(version, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "claude")
			if err := os.WriteFile(path, []byte("#!/bin/sh\nprintf '%s\\n' "+shellQuote(version)+"\n"), 0700); err != nil {
				t.Fatal(err)
			}
			if checkVersion(context.Background(), path) == nil {
				t.Fatal("unsupported version accepted")
			}
		})
	}
}

type fixtureConn struct {
	mu               sync.Mutex
	registrations    []nativebridge.Registration
	delivery         *nativebridge.Delivery
	events           []nativebridge.Event
	status           nativebridge.Status
	cancelAfterClaim bool
	claimed          bool
}

func (f *fixtureConn) Register(_ context.Context, r nativebridge.Registration) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.registrations = append(f.registrations, r)
	return nil
}
func (f *fixtureConn) Poll(_ context.Context, r nativebridge.Readiness) (*nativebridge.Delivery, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r.State != "ready" {
		return nil, errors.New("unproven idle readiness")
	}
	if f.claimed || f.delivery == nil {
		return nil, nil
	}
	f.claimed = true
	d := *f.delivery
	f.status.ActiveID = d.ID
	if f.cancelAfterClaim {
		f.status.Cancel = true
	}
	return &d, nil
}
func (f *fixtureConn) Report(_ context.Context, e nativebridge.Event) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events = append(f.events, e)
	if e.Phase == "completed" || e.Phase == "failed" || e.Phase == "cancelled" {
		f.status.ActiveID = ""
	}
	if e.Phase == "uncertain" {
		f.status.State = "uncertain"
	}
	return nil
}
func (f *fixtureConn) Status(context.Context) (nativebridge.Status, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.status, nil
}
func (f *fixtureConn) snapshot() ([]nativebridge.Registration, []nativebridge.Event) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]nativebridge.Registration{}, f.registrations...), append([]nativebridge.Event{}, f.events...)
}

func hookFixture(t *testing.T) (hookEnvironment, net.Listener) {
	t.Helper()
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	env := hookEnvironment{dir: dir, nonce: strings.Repeat("a", 48), socket: filepath.Join(dir, "inbox.sock"), token: "child-secret-token"}
	if err := os.WriteFile(filepath.Join(dir, "alive"), []byte(env.nonce), 0600); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("unix", env.socket)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	if err := os.Chmod(env.socket, 0600); err != nil {
		t.Fatal(err)
	}
	return env, listener
}
func startHook(t *testing.T, ctx context.Context, env hookEnvironment, conn *fixtureConn, session string) <-chan error {
	t.Helper()
	done := make(chan error, 1)
	go func() {
		input := `{"session_id":"` + session + `","hook_event_name":"SessionStart"}`
		done <- runHook(ctx, strings.NewReader(input), conn, env, 5*time.Millisecond)
	}()
	return done
}
func await(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatal("fixture timed out")
		}
		time.Sleep(time.Millisecond)
	}
}
func finish(t *testing.T, done <-chan error) {
	t.Helper()
	select {
	case err := <-done:
		if err != nil && !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("hook did not stop")
	}
}

func TestHookNativeSubmissionIsNotAcknowledgementOrCompletion(t *testing.T) {
	env, listener := hookFixture(t)
	conn := &fixtureConn{delivery: &nativebridge.Delivery{ID: "delivery-one", TaskID: "task-one", From: "codex", Kind: "instruction", Text: "Write a stanza"}, status: nativebridge.Status{Mode: "automatic"}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	frames := make(chan []map[string]any, 1)
	go func() {
		socket, err := listener.Accept()
		if err != nil {
			return
		}
		defer socket.Close()
		scanner := bufio.NewScanner(socket)
		var messages []map[string]any
		for scanner.Scan() {
			var frame map[string]any
			if json.Unmarshal(scanner.Bytes(), &frame) == nil {
				messages = append(messages, frame)
			}
		}
		frames <- messages
	}()
	done := startHook(t, ctx, env, conn, "native-session")
	await(t, func() bool { _, events := conn.snapshot(); return len(events) > 0 })
	var messages []map[string]any
	select {
	case messages = <-frames:
	case <-time.After(time.Second):
		t.Fatal("no native frames")
	}
	if len(messages) != 2 || messages[0]["type"] != "auth" || messages[0]["token"] != env.token || messages[1]["priority"] != "later" || messages[1]["session_id"] != "native-session" || messages[1]["uuid"] != nativeMessageID("native-session", "delivery-one") {
		t.Fatalf("unsafe native frames: %#v", messages)
	}
	content := messages[1]["message"].(map[string]any)["content"].(string)
	for _, want := range []string{"from \"codex\"", "delivery-one", "native-session", "native.event", "\"phase\":\"running\"", "\"phase\":\"completed\"", "Proceed only if"} {
		if !strings.Contains(content, want) {
			t.Fatalf("missing correlation instruction %q", want)
		}
	}
	registrations, events := conn.snapshot()
	if len(registrations) != 1 || registrations[0].Capabilities.PreservesDraft || registrations[0].Capabilities.Cancellation || !registrations[0].Capabilities.NativeQueue {
		t.Fatalf("unproven capabilities: %+v", registrations)
	}
	if len(events) != 1 || events[0].Phase != "submitted" || strings.Contains(events[0].Reason, env.token) {
		t.Fatalf("false delivery acknowledgement: %+v", events)
	}
	// Only an explicit broker lifecycle result ends the active delivery. The
	// adapter observes that result; it never invents running/completed itself.
	conn.mu.Lock()
	conn.status.ActiveID = ""
	conn.mu.Unlock()
	time.Sleep(15 * time.Millisecond)
	if err := os.Remove(filepath.Join(env.dir, "alive")); err != nil {
		t.Fatal(err)
	}
	finish(t, done)
	_, events = conn.snapshot()
	if len(events) != 1 {
		t.Fatalf("adapter invented completion: %+v", events)
	}
}

func TestQueuedCancellationIsUncertainAndNeverReplayed(t *testing.T) {
	env, listener := hookFixture(t)
	conn := &fixtureConn{delivery: &nativebridge.Delivery{ID: "queued-once"}, status: nativebridge.Status{Mode: "automatic"}}
	accepted := make(chan struct{}, 2)
	go func() {
		for {
			socket, err := listener.Accept()
			if err != nil {
				return
			}
			_, _ = io.Copy(io.Discard, socket)
			_ = socket.Close()
			accepted <- struct{}{}
		}
	}()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := startHook(t, ctx, env, conn, "native-session")
	await(t, func() bool { _, events := conn.snapshot(); return len(events) == 1 })
	conn.mu.Lock()
	conn.status.Cancel = true
	conn.mu.Unlock()
	await(t, func() bool { _, events := conn.snapshot(); return len(events) == 2 })
	_, events := conn.snapshot()
	if events[0].Phase != "submitted" || events[1].Phase != "uncertain" {
		t.Fatalf("false cancellation guarantee: %+v", events)
	}
	cancel()
	finish(t, done)
	select {
	case <-accepted:
	case <-time.After(time.Second):
		t.Fatal("no native submission")
	}
	select {
	case <-accepted:
		t.Fatal("submitted delivery replayed")
	default:
	}
}

func TestMissingAcknowledgementOnBridgeExitIsUncertain(t *testing.T) {
	env, listener := hookFixture(t)
	conn := &fixtureConn{delivery: &nativebridge.Delivery{ID: "unconfirmed"}, status: nativebridge.Status{Mode: "automatic"}}
	go func() {
		socket, err := listener.Accept()
		if err == nil {
			_, _ = io.Copy(io.Discard, socket)
			_ = socket.Close()
		}
	}()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := startHook(t, ctx, env, conn, "native-session")
	await(t, func() bool { _, events := conn.snapshot(); return len(events) == 1 })
	_ = os.Remove(filepath.Join(env.dir, "alive"))
	finish(t, done)
	_, events := conn.snapshot()
	if len(events) != 2 || events[1].Phase != "uncertain" {
		t.Fatalf("lost unconfirmed delivery: %+v", events)
	}
}

func TestCancellationBeforeSubmissionNeverTouchesNativeInbox(t *testing.T) {
	env, listener := hookFixture(t)
	conn := &fixtureConn{delivery: &nativebridge.Delivery{ID: "cancel-me"}, status: nativebridge.Status{Mode: "automatic"}, cancelAfterClaim: true}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := startHook(t, ctx, env, conn, "native-session")
	await(t, func() bool { _, events := conn.snapshot(); return len(events) > 0 })
	_, events := conn.snapshot()
	if len(events) != 1 || events[0].Phase != "cancelled" {
		t.Fatalf("not cancelled: %+v", events)
	}
	_ = listener.(*net.UnixListener).SetDeadline(time.Now().Add(20 * time.Millisecond))
	socket, err := listener.Accept()
	if err == nil {
		socket.Close()
		t.Fatal("cancelled instruction reached native session")
	}
	cancel()
	finish(t, done)
}

func TestDuplicateHooksDoNotCreateCompetingConsumers(t *testing.T) {
	env, _ := hookFixture(t)
	conn := &fixtureConn{status: nativebridge.Status{Mode: "manual"}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	first := startHook(t, ctx, env, conn, "same-native-session")
	await(t, func() bool { r, _ := conn.snapshot(); return len(r) == 1 })
	second := startHook(t, ctx, env, conn, "same-native-session")
	finish(t, second)
	r, _ := conn.snapshot()
	if len(r) != 1 {
		t.Fatalf("duplicate consumer: %+v", r)
	}
	cancel()
	finish(t, first)
}

func TestNewNativeSessionRetiresPreviousHook(t *testing.T) {
	env, _ := hookFixture(t)
	old := &fixtureConn{status: nativebridge.Status{Mode: "manual"}}
	next := &fixtureConn{status: nativebridge.Status{Mode: "manual"}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	first := startHook(t, ctx, env, old, "old-native-session")
	await(t, func() bool { r, _ := old.snapshot(); return len(r) == 1 })
	second := startHook(t, ctx, env, next, "new-native-session")
	finish(t, first)
	await(t, func() bool { r, _ := next.snapshot(); return len(r) == 1 })
	cancel()
	finish(t, second)
}

func TestHookRejectsMissingOrUntrustedNativeSocket(t *testing.T) {
	env, listener := hookFixture(t)
	conn := &fixtureConn{}
	if err := os.Chmod(env.socket, 0666); err != nil {
		t.Fatal(err)
	}
	input := `{"session_id":"native-session","hook_event_name":"SessionStart"}`
	if err := runHook(context.Background(), strings.NewReader(input), conn, env, time.Millisecond); err == nil {
		t.Fatal("public inbox accepted")
	}
	if r, _ := conn.snapshot(); len(r) != 0 {
		t.Fatal("unsafe socket registered")
	}
	_ = listener.Close()
	env.socket = "relative.sock"
	if err := runHook(context.Background(), strings.NewReader(input), conn, env, time.Millisecond); err == nil {
		t.Fatal("relative inbox accepted")
	}
}
