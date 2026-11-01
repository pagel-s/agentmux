package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestIrrelevantFlagsCannotReachDestructiveWorkspaceActions(t *testing.T) {
	for _, tc := range []struct {
		op   string
		args []string
	}{{"down", []string{"--project", "/unrelated"}}, {"remove", []string{"claude", "--sandbox", "local"}}, {"attach", []string{"--project", "/wrong"}}, {"isolate", []string{"claude", "--config", "other.yaml"}}} {
		var out, diag bytes.Buffer
		if code := runWorkSessionInner(tc.op, tc.args, strings.NewReader(""), &out, &diag); code != exitValidation || !strings.Contains(diag.String(), "no action was taken") {
			t.Fatalf("%s: %d %s", tc.op, code, diag.String())
		}
	}
	if err := validateWorkFlags("send", []string{"claude", "--", "--project", "literal"}); err != nil {
		t.Fatal("literal payload reinterpreted:", err)
	}
	if err := validateWorkFlags("reply", []string{"--to", "--project", "--mode", "note"}); err != nil {
		t.Fatal("option value reinterpreted:", err)
	}
}
