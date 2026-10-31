package modelmeta

import "testing"

func TestOverridesPreserveExplicitFalseAndConservativeFallback(t *testing.T) {
	yes, no := true, false
	m := Overlay(Metadata{ContextWindow: 65536, Reasoning: &yes, Tools: &yes, Input: []string{"text", "image"}}, Metadata{ContextWindow: 4096, Reasoning: &no, Tools: &no, Input: []string{"text"}}).Effective()
	if m.ContextWindow != 4096 || m.MaxOutput != 2048 || *m.Reasoning || *m.Tools || len(m.Input) != 1 {
		t.Fatalf("metadata=%+v", m)
	}
	small := Metadata{ContextWindow: 512}.Effective()
	if small.MaxOutput >= small.ContextWindow {
		t.Fatal("fallback output exceeds context")
	}
	for _, invalid := range []Metadata{{ContextWindow: -1}, {ContextWindow: 999999999}, {MaxOutput: -1}, {MaxOutput: 16384}, {ContextWindow: 4096, MaxOutput: 8192}, {Input: []string{"video"}}, {Input: []string{"image"}}, {Input: []string{"text", "text"}}} {
		if invalid.Validate() == nil {
			t.Fatalf("accepted %+v", invalid)
		}
	}
}
