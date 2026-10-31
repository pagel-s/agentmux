//go:build unix

package harness

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCancellationKillsDescendants(t *testing.T) {
	sentinel := filepath.Join(t.TempDir(), "orphan-wrote-this")
	req := helperRequest("command", "descendant")
	req.Env["AGENTMUX_HARNESS_SENTINEL"] = sentinel
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	result, err := Run(ctx, req)
	if !errors.Is(err, context.DeadlineExceeded) || !strings.Contains(result.Text, "started") {
		t.Fatalf("descendant was not started before cancellation: text=%q, error=%v", result.Text, err)
	}
	time.Sleep(600 * time.Millisecond)
	if _, err := os.Stat(sentinel); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("descendant survived cancellation: %v", err)
	}
}
