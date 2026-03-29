package remote

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestIsRemote(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		{"http://example.com/config.yaml", true},
		{"https://example.com/bundle.amx", true},
		{"./local.yaml", false},
		{"/abs/path/config.yaml", false},
		{"config.yaml", false},
	}

	for _, tt := range tests {
		if got := IsRemote(tt.path); got != tt.want {
			t.Errorf("IsRemote(%q) = %v, want %v", tt.path, got, tt.want)
		}
	}
}

func TestFetch(t *testing.T) {
	content := "version: 1\nsession: remote-test"
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/x-yaml")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(content))
	}))
	defer ts.Close()

	localPath, err := Fetch(ts.URL + "/config.yaml")
	if err != nil {
		t.Fatalf("Fetch failed: %v", err)
	}
	defer os.Remove(localPath)

	if !strings.HasSuffix(localPath, ".yaml") {
		t.Errorf("expected .yaml suffix, got %q", localPath)
	}

	data, err := os.ReadFile(localPath)
	if err != nil {
		t.Fatalf("read fetched file: %v", err)
	}

	if string(data) != content {
		t.Errorf("fetched content = %q, want %q", string(data), content)
	}
}
