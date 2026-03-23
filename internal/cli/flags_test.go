package cli

import "testing"

func TestParseLoadAcceptsConfigBeforeFlags(t *testing.T) {
	opts, err := ParseLoad([]string{"dev.yaml", "--dry-run", "--param", "project_dir=~/code/app", "--no-attach"})
	if err != nil {
		t.Fatalf("ParseLoad returned error: %v", err)
	}

	if opts.ConfigPath != "dev.yaml" {
		t.Fatalf("ConfigPath = %q, want %q", opts.ConfigPath, "dev.yaml")
	}
	if !opts.DryRun {
		t.Fatalf("DryRun = false, want true")
	}
	if opts.Attach {
		t.Fatalf("Attach = true, want false for dry-run")
	}
	if got := opts.Params["project_dir"]; got != "~/code/app" {
		t.Fatalf("project_dir = %q, want %q", got, "~/code/app")
	}
}

func TestParseRenderAcceptsInlineParam(t *testing.T) {
	opts, err := ParseRender([]string{"--param=project_dir=.", "dev.yaml"})
	if err != nil {
		t.Fatalf("ParseRender returned error: %v", err)
	}

	if got := opts.Params["project_dir"]; got != "." {
		t.Fatalf("project_dir = %q, want %q", got, ".")
	}
}

func TestParseStatusRequiresSessionName(t *testing.T) {
	session, err := ParseStatus([]string{"dogfood"})
	if err != nil {
		t.Fatalf("ParseStatus returned error: %v", err)
	}
	if session != "dogfood" {
		t.Fatalf("session = %q, want %q", session, "dogfood")
	}

	if _, err := ParseStatus(nil); err == nil {
		t.Fatal("ParseStatus returned nil error for missing session")
	}
}

func TestParseFocusRequiresSessionAndPaneName(t *testing.T) {
	opts, err := ParseFocus([]string{"dogfood", "resolve"})
	if err != nil {
		t.Fatalf("ParseFocus returned error: %v", err)
	}
	if opts.Session != "dogfood" {
		t.Fatalf("Session = %q, want %q", opts.Session, "dogfood")
	}
	if opts.Pane != "resolve" {
		t.Fatalf("Pane = %q, want %q", opts.Pane, "resolve")
	}

	if _, err := ParseFocus([]string{"dogfood"}); err == nil {
		t.Fatal("ParseFocus returned nil error for missing pane name")
	}
}
