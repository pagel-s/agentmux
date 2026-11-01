package memoryio

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"agentmux/internal/managed"
	"agentmux/internal/managedconfig"
)

type storeCaller struct {
	s     *managed.Store
	token string
}

func (c storeCaller) Call(ctx context.Context, r managed.Request) (json.RawMessage, *managed.Fault) {
	return c.s.Call(ctx, c.token, r)
}
func fixture(t *testing.T, session string) storeCaller {
	t.Helper()
	state := filepath.Join(t.TempDir(), "state")
	s, err := managed.Init(state, managedconfig.Config{Version: 2, Mode: "managed", Session: session, Agents: map[string]managedconfig.Agent{"alice": {Harness: "command", Command: []string{"/bin/cat"}}}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	credential, err := managed.LoadCredential(managed.CredentialPath(state, "alice"))
	if err != nil {
		t.Fatal(err)
	}
	return storeCaller{s, credential.Token}
}

func TestCrossRunTransferCASAndIdempotentResume(t *testing.T) {
	ctx := context.Background()
	source := fixture(t, "source")
	dest := fixture(t, "destination")
	if _, err := Call(ctx, source, "memory.put", map[string]any{"namespace": "private", "key": "design", "value": "a retained fact", "tags": []string{"design"}}, "first"); err != nil {
		t.Fatal(err)
	}
	a, err := Export(ctx, source, "private")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "archive.json")
	if err := Write(path, a); err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0600 {
		t.Fatal("archive not private")
	}
	loaded, err := Read(path)
	if err != nil {
		t.Fatal(err)
	}
	r, err := Import(ctx, dest, loaded, "private", false)
	if err != nil || r.Imported != 1 {
		t.Fatalf("import: %+v %v", r, err)
	}
	r, err = Import(ctx, dest, loaded, "private", false)
	if err != nil || r.Imported != 0 || r.Skipped != 1 {
		t.Fatalf("resume reimported data: %+v %v", r, err)
	}
	if _, err := Call(ctx, dest, "memory.put", map[string]any{"namespace": "private", "key": "design", "value": "owner revision", "version": 1}, "revision"); err != nil {
		t.Fatal(err)
	}
	if _, err := Import(ctx, dest, loaded, "private", false); err == nil {
		t.Fatal("import overwrote existing value without explicit replace")
	}
	r, err = Import(ctx, dest, loaded, "private", true)
	if err != nil || r.Imported != 1 {
		t.Fatalf("explicit CAS replace: %+v %v", r, err)
	}
	raw, err := Call(ctx, dest, "memory.get", map[string]any{"namespace": "private", "key": "design"}, "")
	if err != nil || !strings.Contains(string(raw), `"kind":"import"`) {
		t.Fatal("import provenance missing")
	}
	var imported struct {
		Entry managed.Memory `json:"entry"`
	}
	if err := json.Unmarshal(raw, &imported); err != nil {
		t.Fatal(err)
	}
	if len(imported.Entry.Sources) != 1 {
		t.Fatalf("source claims: %+v", imported.Entry.Sources)
	}
	claims := imported.Entry.Sources[0]
	if claims.SourceSession != loaded.SourceSession || claims.SourceAgent != loaded.SourceAgent || claims.SourceNamespace != loaded.Namespace || claims.SourceAuthor != loaded.Entries[0].Author || claims.SourceKey != "design" || claims.SourceVersion != loaded.Entries[0].Version || claims.ArchiveSHA256 != loaded.SHA256 || imported.Entry.Author != "alice" {
		t.Fatalf("imported claims incomplete or destination author changed: %+v", imported.Entry)
	}
}

func TestArchiveTamperingAndUnsafePermissionsRejected(t *testing.T) {
	c := fixture(t, "source")
	a, err := Export(context.Background(), c, "private")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "a.json")
	if err := Write(path, a); err != nil {
		t.Fatal(err)
	}
	if err := Write(path, a); err == nil {
		t.Fatal("overwrote archive")
	}
	if err := os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := Read(path); err == nil {
		t.Fatal("read public archive")
	}
	if err := os.Chmod(path, 0600); err != nil {
		t.Fatal(err)
	}
	a.SHA256 = strings.Repeat("0", 64)
	raw, _ := json.Marshal(a)
	os.WriteFile(path, raw, 0600)
	if _, err := Read(path); err == nil {
		t.Fatal("accepted content digest mismatch")
	}
}

func TestEmbeddingExplicitEndpointNoRedirectAndResponseBounds(t *testing.T) {
	var seen int
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen++
		if r.URL.Path != "/v1/embeddings" || r.Header.Get("Authorization") != "Bearer selected-secret" {
			t.Error("endpoint/auth not explicitly preserved")
		}
		json.NewEncoder(w).Encode(map[string]any{"data": []any{map[string]any{"embedding": []float64{1, 0.5}}}})
	}))
	defer provider.Close()
	v, err := Embed(context.Background(), provider.URL+"/v1", "local", "selected-secret", "authorized text")
	if err != nil || len(v) != 2 || seen != 1 {
		t.Fatalf("embed: %v %v", v, err)
	}
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, provider.URL+"/v1/embeddings", http.StatusTemporaryRedirect)
	}))
	defer redirect.Close()
	if _, err := Embed(context.Background(), redirect.URL, "local", "secret", "text"); err == nil {
		t.Fatal("followed embedding redirect")
	}
	if seen != 1 {
		t.Fatal("redirect leaked provider request")
	}
	if _, err := Embed(context.Background(), "https://user:password@example.test", "local", "", "x"); err == nil {
		t.Fatal("accepted URL credentials")
	}
}
