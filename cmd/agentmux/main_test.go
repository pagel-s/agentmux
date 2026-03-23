package main

import "testing"

func TestVersionString(t *testing.T) {
	original := version
	version = "test"
	t.Cleanup(func() {
		version = original
	})

	if got, want := versionString(), "agentmux test"; got != want {
		t.Fatalf("versionString() = %q, want %q", got, want)
	}
}

func TestRunVersionReturnsSuccess(t *testing.T) {
	if got, want := run([]string{"version"}), exitSuccess; got != want {
		t.Fatalf("run(version) = %d, want %d", got, want)
	}
	if got, want := run([]string{"--version"}), exitSuccess; got != want {
		t.Fatalf("run(--version) = %d, want %d", got, want)
	}
}
