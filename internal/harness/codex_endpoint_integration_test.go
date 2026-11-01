package harness

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// This exercises the installed native CLI's provider config parser and routing,
// not a substitute executable or a claim about arbitrary server/model quality.
func TestInstalledCodexCustomEndpointDoesNotFallBackToOwnerLogin(t *testing.T) {
	if os.Getenv("AGENTMUX_CODEX_INTEGRATION") != "1" {
		t.Skip("set AGENTMUX_CODEX_INTEGRATION=1 for installed Codex endpoint conformance")
	}
	binary, err := exec.LookPath("codex")
	if err != nil {
		t.Skip("Codex not installed")
	}
	type observed struct{ path, auth string }
	requests := make(chan observed, 16)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case requests <- observed{r.URL.Path, r.Header.Get("Authorization")}:
		default:
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"error":{"message":"intentional endpoint conformance rejection","type":"authentication_error"}}`))
	}))
	defer server.Close()
	work := t.TempDir()
	if err := exec.Command("git", "init", "--quiet", work).Run(); err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(t.TempDir(), "codex-config")
	if err := os.Mkdir(home, 0700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	_, runErr := Run(ctx, Request{Harness: "codex", Model: "fixture-model", BaseURL: server.URL + "/v1", APIKeyEnv: "SELECTED_ENDPOINT_KEY", Env: map[string]string{"SELECTED_ENDPOINT_KEY": "ephemeral-conformance-key", "CODEX_HOME": home}, Command: []string{binary, "--no-daemon"}, Prompt: "Endpoint conformance: do not use tools.", Workdir: work})
	if runErr == nil {
		t.Fatal("intentional unauthorized endpoint counted as successful turn")
	}
	select {
	case request := <-requests:
		if request.path != "/v1/responses" || request.auth != "Bearer ephemeral-conformance-key" {
			t.Fatalf("custom endpoint route/auth mismatch: path %q selected auth %t", request.path, request.auth == "Bearer ephemeral-conformance-key")
		}
	default:
		t.Fatal("installed Codex never reached explicit endpoint; provider configuration was rejected or ignored")
	}
}
