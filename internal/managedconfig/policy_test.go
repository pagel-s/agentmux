package managedconfig

import "testing"

func TestAgentGrantsAreExplicitAndDirectional(t *testing.T) {
	cfg := validConfig()
	if cfg.Can("lead", "instruct", "worker") {
		t.Fatal("coordinator role must not confer authority")
	}
	cfg.Policy.Grants = []Grant{{From: []string{"lead"}, To: []string{"worker"}, Actions: []string{"message.send", "instruct"}}}
	for _, tc := range []struct {
		actor, action, target string
		want                  bool
	}{
		{"lead", "message.send", "worker", true},
		{"lead", "instruct", "worker", true},
		{"worker", "instruct", "lead", false},
		{"lead", "ping.send", "worker", false},
		{"lead", "instruct", "lead", false},
		{"absent", "instruct", "worker", false},
		{"lead", "instruct", "absent", false},
	} {
		if got := cfg.Can(tc.actor, tc.action, tc.target); got != tc.want {
			t.Errorf("Can(%q,%q,%q) = %v, want %v", tc.actor, tc.action, tc.target, got, tc.want)
		}
	}
}

func TestWildcardDoesNotAuthorizeUnknownAgentsOrFutureActions(t *testing.T) {
	cfg := validConfig()
	cfg.Policy.Grants = []Grant{{From: []string{"*"}, To: []string{"*"}, Actions: []string{"*"}}}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, action := range []string{"message.send", "ping.send", "instruct"} {
		if !cfg.Can("worker", action, "lead") {
			t.Errorf("wildcard must grant implemented action %q", action)
		}
	}
	for _, action := range []string{"", "*", "spawn", "interrupt", "steer", "message.send.all"} {
		if cfg.Can("worker", action, "lead") {
			t.Errorf("wildcard granted unsupported action %q", action)
		}
	}
	if cfg.Can("intruder", "instruct", "lead") || cfg.Can("lead", "instruct", "intruder") || cfg.Can("*", "instruct", "lead") {
		t.Fatal("wildcards must not authenticate arbitrary names")
	}
	cfg.Policy.Default = "allow"
	if cfg.Can("worker", "instruct", "lead") {
		t.Fatal("invalid policy must fail closed")
	}
}

func TestNamespaceAccessIsExactAndReadWriteAreIndependent(t *testing.T) {
	cfg := validConfig()
	cfg.Boards = map[string]Access{
		"public":  {Readers: []string{"*"}},
		"private": {Readers: []string{"lead"}, Writers: []string{"worker"}},
	}
	cfg.Memory = map[string]Access{"project": {Readers: []string{"worker"}, Writers: []string{"lead"}}}
	if !cfg.CanReadBoard("worker", "public") || cfg.CanWriteBoard("worker", "public") {
		t.Fatal("reading a public board must not confer write access")
	}
	if cfg.CanReadBoard("worker", "private") || !cfg.CanWriteBoard("worker", "private") {
		t.Fatal("writing must not confer read access")
	}
	if cfg.CanReadBoard("intruder", "public") || cfg.CanReadBoard("worker", "public-extra") || cfg.CanReadBoard("worker", "public/child") {
		t.Fatal("namespace authorization must require a known agent and exact namespace")
	}
	if !cfg.CanReadMemory("worker", "project") || cfg.CanWriteMemory("worker", "project") || !cfg.CanWriteMemory("lead", "project") || cfg.CanReadMemory("lead", "project") {
		t.Fatal("memory read/write boundaries were not preserved")
	}
	if cfg.CanReadMemory("worker", "project/child") || cfg.CanReadMemory("worker", "public") || cfg.CanWriteMemory("lead", "private") {
		t.Fatal("namespace names must not cross board/memory boundaries")
	}
	cfg.Memory["shared"] = Access{Readers: []string{"*"}, Writers: []string{"*"}}
	if cfg.CanReadMemory("intruder", "shared") || cfg.CanWriteMemory("intruder", "shared") {
		t.Fatal("shared memory wildcard must not grant unknown agents access")
	}
}
