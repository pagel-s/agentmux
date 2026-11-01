package managed

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"agentmux/internal/managedconfig"
)

func TestSessionLabelIsOwnerOnlyVersionedCosmeticAndDurable(t *testing.T) {
	cfg := testConfig()
	a := cfg.Agents["alice"]
	a.Role = "Keep the original human role."
	cfg.Agents["alice"] = a
	s := testStore(t, cfg)
	before := s.Config()
	credentialBefore := credential(t, s, "alice")
	definitionBefore, err := s.Agent("alice")
	if err != nil {
		t.Fatal(err)
	}
	input := map[string]any{"id": "alice", "title": "Fix login redirect → 登录", "version": 0}
	expectFault(t, s, "alice", "agent.label", "forbidden", input)
	owner := credential(t, s, "")
	one, p := invoke(s, owner, "agent.label", input, "label-first")
	if p != nil {
		t.Fatal(p)
	}
	two, p := invoke(s, owner, "agent.label", input, "label-first")
	if p != nil || string(one) != string(two) {
		t.Fatalf("label receipt unstable: %s %s %v", one, two, p)
	}
	if strings.Contains(string(one), "secret-value") {
		t.Fatal("label response leaked environment")
	}
	var response struct {
		Agent AgentInfo `json:"agent"`
	}
	if err := json.Unmarshal(one, &response); err != nil || response.Agent.ID != "alice" || response.Agent.TitleVersion != 1 || response.Agent.Title != input["title"] {
		t.Fatalf("invalid label response: %s %v", one, err)
	}
	expectFault(t, s, "", "agent.label", "conflict", input)
	definitionAfter, err := s.Agent("alice")
	if err != nil || !reflect.DeepEqual(definitionBefore, definitionAfter) {
		t.Fatalf("cosmetic label changed native process configuration: %v", err)
	}
	if !reflect.DeepEqual(before, s.Config()) || credentialBefore != credential(t, s, "alice") {
		t.Fatal("title changed immutable policy or credentials")
	}
	// The ID remains the routing/authorization key after the display text changes.
	call(t, s, "alice", "message.send", map[string]any{"to": []string{"bob"}, "text": "stable ID still routes"})
	reopened, err := Open(s.dir)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	info, err := reopened.agent(context.Background(), reopened.db, "alice")
	if err != nil || info.Title != input["title"] || info.TitleVersion != 1 {
		t.Fatalf("label did not survive reopen: %+v %v", info, err)
	}
	call(t, reopened, "", "agent.label", map[string]any{"id": "alice", "title": "", "version": 1})
	info, err = reopened.agent(context.Background(), reopened.db, "alice")
	if err != nil || info.Title != "" || info.TitleVersion != 2 || info.ID != "alice" {
		t.Fatalf("clearing label changed identity or failed to advance version: %+v %v", info, err)
	}
}

func TestSessionTitleAdmissionIsAtomicForNativeAndExternalSessions(t *testing.T) {
	for _, external := range []bool{false, true} {
		s := testStore(t, testConfig())
		a := managedconfig.Agent{Harness: "command", Interaction: "interactive", Workdir: t.TempDir(), Command: []string{"/bin/cat"}, Role: "original role"}
		reference := ""
		if external {
			reference = "observed_123"
		}
		call(t, s, "", "agent.add", map[string]any{"id": "newsession", "config": a, "external": external, "external_ref": reference, "title": "Research comparison"})
		info, err := s.agent(context.Background(), s.db, "newsession")
		if err != nil || info.Title != "Research comparison" || info.TitleVersion != 1 || info.External != external {
			t.Fatalf("initial label not atomically admitted: %+v %v", info, err)
		}
		call(t, s, "", "agent.label", map[string]any{"id": "newsession", "title": "Second display title", "version": 1})
		info, err = s.agent(context.Background(), s.db, "newsession")
		if err != nil || info.Config.Role != a.Role || info.ExternalRef != reference || info.TitleVersion != 2 {
			t.Fatalf("label changed native role or external identity: %+v %v", info, err)
		}
		expectFault(t, s, "", "agent.add", "invalid", map[string]any{"id": "invalidtitle", "config": a, "title": "unsafe\nsecond line"})
		if s.knownAgent(context.Background(), s.db, "invalidtitle") {
			t.Fatal("invalid title left an admitted session")
		}
	}
}

func TestWhitespaceSessionTitleRestoresReadableFallback(t *testing.T) {
	s := testStore(t, testConfig())
	call(t, s, "", "agent.label", map[string]any{"id": "alice", "title": "   ", "version": 0})
	info, err := s.agent(context.Background(), s.db, "alice")
	if err != nil || info.Title != "" || info.TitleVersion != 1 {
		t.Fatalf("invisible title masked session fallback: %+v %v", info, err)
	}
}

func TestSessionTitlesRejectControlsAndBoundUnicode(t *testing.T) {
	for _, title := range []string{"\u200d", "\u200c", "\u200dhidden", "hidden\u200d", "space \u200d word"} {
		if err := ValidateSessionTitle(title); err == nil {
			t.Fatalf("standalone/invisible joiner accepted: %q", title)
		}
	}
	for _, title := range []string{"Engineer 👩🏽‍💻", "Family 👨‍👩‍👧‍👦", "می\u200cروم"} {
		if err := ValidateSessionTitle(title); err != nil {
			t.Fatalf("visible joined title rejected: %q %v", title, err)
		}
	}
	for _, title := range []string{"line\nbreak", "tab\tstop", "escape\x1b[31m", "bidi\u202e", "isolate\u2066", "mark\u061c", string([]byte{0xff}), strings.Repeat("a", 121), strings.Repeat("é", 121)} {
		if err := ValidateSessionTitle(title); err == nil {
			t.Fatalf("unsafe/unbounded title accepted: %q", title)
		}
	}
	for _, title := range []string{"", "Normal daily work", "研究 · 修复登录 🔧", strings.Repeat("界", 120)} {
		if err := ValidateSessionTitle(title); err != nil {
			t.Fatalf("valid title rejected: %q %v", title, err)
		}
	}
	s := testStore(t, testConfig())
	expectFault(t, s, "", "agent.label", "not_found", map[string]any{"id": "missing", "title": "name", "version": 0})
}

func TestStaticSessionCreationUsesDurableWorkspaceCreation(t *testing.T) {
	s := testStore(t, testConfig())
	one, err := s.agent(context.Background(), s.db, "alice")
	if err != nil || one.CreatedAt == "" {
		t.Fatalf("static session has no durable workspace timestamp: %+v %v", one, err)
	}
	if _, err = time.Parse(time.RFC3339Nano, one.CreatedAt); err != nil {
		t.Fatal(err)
	}
	call(t, s, "", "agent.label", map[string]any{"id": "alice", "title": "Cosmetic", "version": 0})
	if err := s.startRuntime(context.Background()); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(s.dir)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	two, err := reopened.agent(context.Background(), reopened.db, "alice")
	if err != nil || two.CreatedAt != one.CreatedAt {
		t.Fatalf("poll/restart/label changed static creation time: %+v %+v %v", one, two, err)
	}
}

func TestLegacyStaticSessionCreationRemainsUnknownAfterRestart(t *testing.T) {
	s := testStore(t, testConfig())
	if _, err := s.db.Exec("DELETE FROM metadata WHERE key='created_at'"); err != nil {
		t.Fatal(err)
	}
	// A runtime start is not evidence for when an existing session was added.
	if _, err := s.db.Exec("INSERT INTO metadata(key,value) VALUES('started_at',?)", now()); err != nil {
		t.Fatal(err)
	}
	if err := s.startRuntime(context.Background()); err != nil {
		t.Fatal(err)
	}
	call(t, s, "", "agent.label", map[string]any{"id": "alice", "title": "Legacy workspace", "version": 0})
	reopened, err := Open(s.dir)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	info, err := reopened.agent(context.Background(), reopened.db, "alice")
	if err != nil || info.CreatedAt != "" {
		t.Fatalf("restart or label manufactured a legacy creation time: %+v %v", info, err)
	}
}
