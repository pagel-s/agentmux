package managedconfig

import "testing"

func TestPersistentConversationRequiresPrivateSupportedRuntime(t *testing.T) {
	for _, test := range []struct {
		harness, sandbox, mode string
		valid                  bool
	}{
		{"claude", "bubblewrap", "persistent", true}, {"codex", "bubblewrap", "persistent", true},
		{"pi", "bubblewrap", "persistent", true}, {"antigravity", "bubblewrap", "persistent", false},
		{"command", "bubblewrap", "persistent", false}, {"claude", "local", "persistent", false},
		{"antigravity", "local", "fresh", true}, {"pi", "bubblewrap", "automatic", false},
	} {
		cfg := Config{Version: 2, Mode: "managed", Session: "test", Agents: map[string]Agent{"worker": {
			Harness: test.harness, Sandbox: test.sandbox, Conversation: test.mode, Command: []string{"/bin/true"},
		}}}
		if err := cfg.Validate(); (err == nil) != test.valid {
			t.Errorf("%+v: %v", test, err)
		}
	}
}
