package agentauth

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"agentmux/internal/managedconfig"
)

func TestPrivateFilteredSnapshotAndCleanup(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "credentials.json")
	original := `{"claudeAiOauth":{"accessToken":"test-access-value","refreshToken":"test-refresh-value"},"unrelated":{"secret":"must-not-mount"}}`
	if err := os.WriteFile(source, []byte(original), 0600); err != nil {
		t.Fatal(err)
	}
	p, err := Prepare("claude", managedconfig.NativeAuth{File: source})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if len(p.Files) != 1 || p.Files[0].Target != "/home/agent/.claude/.credentials.json" {
		t.Fatalf("unexpected files: %+v", p.Files)
	}
	copyPath := p.Files[0].Source
	content, err := os.ReadFile(copyPath)
	if err != nil || strings.Contains(string(content), "must-not-mount") || !strings.Contains(string(content), "test-access-value") {
		t.Fatal("credential selection failed")
	}
	info, _ := os.Stat(copyPath)
	if info.Mode().Perm() != 0600 {
		t.Fatal("snapshot is not private")
	}
	if strings.Join(p.Secrets, ",") == "" {
		t.Fatal("known values missing from result redaction")
	}
	p.Close()
	if _, err := os.Stat(copyPath); !os.IsNotExist(err) {
		t.Fatal("snapshot survived cleanup")
	}
	unchanged, _ := os.ReadFile(source)
	if string(unchanged) != original {
		t.Fatal("host authentication changed")
	}
}

func TestPiOnlySelectedProviderIsMounted(t *testing.T) {
	source := filepath.Join(t.TempDir(), "auth.json")
	_ = os.WriteFile(source, []byte(`{"anthropic":{"type":"oauth","access":"only-this-provider","refresh":"provider-refresh","expires":999},"other":{"type":"api_key","key":"peer-provider-secret"}}`), 0600)
	p, err := Prepare("pi", managedconfig.NativeAuth{File: source, Provider: "anthropic"})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	b, _ := os.ReadFile(p.Files[0].Source)
	var selected map[string]any
	_ = json.Unmarshal(b, &selected)
	if len(selected) != 1 || selected["anthropic"] == nil || strings.Contains(string(b), "peer-provider-secret") {
		t.Fatal("unselected provider leaked")
	}
}

func TestRejectUnsafeOrUnsupportedCredentialSources(t *testing.T) {
	dir := t.TempDir()
	valid := filepath.Join(dir, "valid.json")
	_ = os.WriteFile(valid, []byte(`{"tokens":{"access_token":"private-token-value"}}`), 0600)
	public := filepath.Join(dir, "public.json")
	_ = os.WriteFile(public, []byte(`{"tokens":{"access_token":"private-token-value"}}`), 0644)
	_ = os.Chmod(public, 0644)
	link := filepath.Join(dir, "link.json")
	_ = os.Symlink(valid, link)
	parentLink := filepath.Join(t.TempDir(), "linked-parent")
	_ = os.Symlink(dir, parentLink)
	invalid := filepath.Join(dir, "invalid.json")
	_ = os.WriteFile(invalid, []byte(`{"tokens":{"access_token":"private-token-value"}} {}`), 0600)
	oversized := filepath.Join(dir, "large.json")
	_ = os.WriteFile(oversized, []byte(strings.Repeat("x", maxFileBytes+1)), 0600)
	hardlink := filepath.Join(dir, "hardlink.json")
	hardSource := filepath.Join(dir, "hard-source.json")
	_ = os.WriteFile(hardSource, []byte(`{"tokens":{"access_token":"private-token-value"}}`), 0600)
	if err := os.Link(hardSource, hardlink); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ name, harness, path, provider string }{
		{"directory", "codex", dir, ""}, {"public", "codex", public, ""}, {"symlink", "codex", link, ""}, {"extra document", "codex", invalid, ""},
		{"parent symlink", "codex", filepath.Join(parentLink, "valid.json"), ""},
		{"hardlink", "codex", hardSource, ""},
		{"oversized", "codex", oversized, ""}, {"relative", "codex", "auth.json", ""}, {"unsupported", "antigravity", valid, ""}, {"wrong schema", "claude", valid, ""}, {"unselected Pi", "pi", valid, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, err := Prepare(tc.harness, managedconfig.NativeAuth{File: tc.path, Provider: tc.provider})
			if p != nil {
				p.Close()
			}
			if err == nil {
				t.Fatal("unsafe auth accepted")
			}
			if strings.Contains(err.Error(), "private-token-value") {
				t.Fatal("error exposed credential")
			}
		})
	}
}

func TestPiRejectsCommandCredentialsAndDropsEnvironment(t *testing.T) {
	values := secretStrings(map[string]any{"key": "xyz", "OPENAI_API_KEY": "abc", "type": "api_key"})
	if len(values) != 2 || !strings.Contains(strings.Join(values, ","), "xyz") || !strings.Contains(strings.Join(values, ","), "abc") {
		t.Fatal("short known secret was not selected for redaction")
	}
	for _, raw := range []string{
		`{"anthropic":{"type":"api_key","key":"!echo credential"}}`,
		`{"anthropic":{"type":"unknown","key":"private-value"}}`,
	} {
		file := filepath.Join(t.TempDir(), "auth.json")
		_ = os.WriteFile(file, []byte(raw), 0600)
		if p, err := Prepare("pi", managedconfig.NativeAuth{File: file, Provider: "anthropic"}); err == nil {
			p.Close()
			t.Fatal("executable or unknown credential accepted")
		}
	}
	file := filepath.Join(t.TempDir(), "auth.json")
	_ = os.WriteFile(file, []byte(`{"anthropic":{"type":"api_key","key":"literal-private-key","env":{"LD_PRELOAD":"bad-library"}}}`), 0600)
	p, err := Prepare("pi", managedconfig.NativeAuth{File: file, Provider: "anthropic"})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	data, _ := os.ReadFile(p.Files[0].Source)
	if strings.Contains(string(data), "LD_PRELOAD") {
		t.Fatal("noncredential environment retained")
	}
}

func TestOriginalAuthCannotBeExposedByAnyWorkspaceOrDependency(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "auth.json")
	_ = os.WriteFile(source, []byte(`{"tokens":{"access_token":"private-value"}}`), 0600)
	base := managedconfig.Config{Agents: map[string]managedconfig.Agent{"owner": {Auth: managedconfig.NativeAuth{File: source}}, "peer": {}}}
	if err := CheckBoundary(base); err != nil {
		t.Fatal(err)
	}
	for _, peer := range []managedconfig.Agent{{Workdir: dir}, {ReadOnlyPaths: []string{dir}}, {ReadOnlyPaths: []string{source}}} {
		base.Agents["peer"] = peer
		if CheckBoundary(base) == nil {
			t.Fatal("original credential exposed to peer")
		}
	}
}
