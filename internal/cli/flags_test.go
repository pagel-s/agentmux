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
