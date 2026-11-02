//go:build !unix

package harness

import "testing"

func TestBoundedTurnRejectsUnsupportedProcessGroupCleanup(t *testing.T) {
	if _, err := Build(Request{Harness: "command", Command: []string{"must-not-start"}, ReapDescendants: true}); err == nil {
		t.Fatal("unsupported descendant cleanup did not fail closed")
	}
}
