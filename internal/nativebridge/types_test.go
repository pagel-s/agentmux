package nativebridge

import (
	"strings"
	"testing"
)

func TestPromptKeepsBrokerOriginsDistinct(t *testing.T) {
	peer := Prompt(Delivery{Kind: "instruction", From: "claude", Text: "An attributed task"})
	if !strings.Contains(peer, "not the human owner") || !strings.Contains(peer, "Agent-authored instruction") {
		t.Fatal("peer command lost its source")
	}
	owner := Prompt(Delivery{Kind: "instruction", From: "$owner", Text: "Owner request"})
	if strings.Contains(owner, "from another agent") || !strings.Contains(owner, "Owner-authored instruction") {
		t.Fatal("owner was mislabeled as a peer")
	}
	boundary := Prompt(Delivery{Kind: "boundaries", From: "$owner", Text: "Updated policy"})
	if !strings.Contains(boundary, "coordination service") || !strings.Contains(boundary, "not an instruction grant") {
		t.Fatal("boundary snapshot became peer authority")
	}
}
