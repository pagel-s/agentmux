package template

import "testing"

func TestExpandString(t *testing.T) {
	got, err := ExpandString("cd {{project_dir}} && git checkout {{branch}}", map[string]string{
		"project_dir": "/tmp/app",
		"branch":      "feature-x",
	})
	if err != nil {
		t.Fatalf("ExpandString returned error: %v", err)
	}

	want := "cd /tmp/app && git checkout feature-x"
	if got != want {
		t.Fatalf("ExpandString = %q, want %q", got, want)
	}
}

func TestExpandStringMissingParam(t *testing.T) {
	if _, err := ExpandString("{{project_dir}}", map[string]string{}); err == nil {
		t.Fatal("ExpandString returned nil error, want missing param error")
	}
}
