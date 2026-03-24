package cli

import "testing"

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

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

func TestParseReloadAcceptsConfigBeforeFlags(t *testing.T) {
	opts, err := ParseReload([]string{"dev.yaml", "--dry-run", "--param", "project_dir=~/code/app", "--no-attach"})
	if err != nil {
		t.Fatalf("ParseReload returned error: %v", err)
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
	if opts.Replace {
		t.Fatalf("Replace = true, want false because reload is implicit replace")
	}
	if got := opts.Params["project_dir"]; got != "~/code/app" {
		t.Fatalf("project_dir = %q, want %q", got, "~/code/app")
	}
}

func TestParseReloadRejectsExplicitReplace(t *testing.T) {
	if _, err := ParseReload([]string{"dev.yaml", "--replace"}); err == nil {
		t.Fatal("ParseReload returned nil error for explicit --replace")
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

func TestParseInitRequiresTemplateAndTarget(t *testing.T) {
	opts, err := ParseInit([]string{"minimal", "my-workflow.yaml"})
	if err != nil {
		t.Fatalf("ParseInit returned error: %v", err)
	}
	if opts.Template != "minimal" {
		t.Fatalf("Template = %q, want %q", opts.Template, "minimal")
	}
	if opts.Path != "my-workflow.yaml" {
		t.Fatalf("Path = %q, want %q", opts.Path, "my-workflow.yaml")
	}
	if opts.Force {
		t.Fatal("Force = true, want false")
	}

	if _, err := ParseInit([]string{"minimal"}); err == nil {
		t.Fatal("ParseInit returned nil error for missing target path")
	}
}

func TestParseInitAcceptsForce(t *testing.T) {
	opts, err := ParseInit([]string{"--force", "minimal", "my-workflow.yaml"})
	if err != nil {
		t.Fatalf("ParseInit returned error: %v", err)
	}
	if !opts.Force {
		t.Fatal("Force = false, want true")
	}
}

func TestParseEditRequiresExactlyOneTarget(t *testing.T) {
	opts, err := ParseEdit([]string{"dogfood"})
	if err != nil {
		t.Fatalf("ParseEdit returned error: %v", err)
	}
	if opts.Target != "dogfood" {
		t.Fatalf("Target = %q, want %q", opts.Target, "dogfood")
	}

	if _, err := ParseEdit(nil); err == nil {
		t.Fatal("ParseEdit returned nil error for missing target")
	}
}

func TestParseRememberRequiresSessionAndConfigPath(t *testing.T) {
	opts, err := ParseRemember([]string{"dogfood", "dogfood.yaml"})
	if err != nil {
		t.Fatalf("ParseRemember returned error: %v", err)
	}
	if opts.Session != "dogfood" {
		t.Fatalf("Session = %q, want %q", opts.Session, "dogfood")
	}
	if opts.Path != "dogfood.yaml" {
		t.Fatalf("Path = %q, want %q", opts.Path, "dogfood.yaml")
	}

	if _, err := ParseRemember([]string{"dogfood"}); err == nil {
		t.Fatal("ParseRemember returned nil error for missing config path")
	}
}

func TestParseStatusRequiresSessionName(t *testing.T) {
	opts, err := ParseStatus([]string{"dogfood"})
	if err != nil {
		t.Fatalf("ParseStatus returned error: %v", err)
	}
	if opts.Session != "dogfood" {
		t.Fatalf("session = %q, want %q", opts.Session, "dogfood")
	}
	if opts.JSON {
		t.Fatal("JSON = true, want false")
	}
	if opts.Pane != "" {
		t.Fatalf("Pane = %q, want empty", opts.Pane)
	}

	if _, err := ParseStatus(nil); err == nil {
		t.Fatal("ParseStatus returned nil error for missing session")
	}
}

func TestParseStatusAcceptsJSONAndPaneFlagsBeforeOrAfterSession(t *testing.T) {
	tests := [][]string{
		{"--json", "dogfood"},
		{"dogfood", "--json"},
		{"--pane", "review", "dogfood"},
		{"dogfood", "--pane", "review"},
		{"--json", "dogfood", "--pane", "review"},
		{"--pane", "review", "--json", "dogfood"},
	}

	for _, args := range tests {
		opts, err := ParseStatus(args)
		if err != nil {
			t.Fatalf("ParseStatus(%v) returned error: %v", args, err)
		}
		if opts.Session != "dogfood" {
			t.Fatalf("Session = %q, want %q", opts.Session, "dogfood")
		}
		if contains(args, "--json") && !opts.JSON {
			t.Fatalf("JSON = false, want true for args %v", args)
		}
		if contains(args, "--pane") && opts.Pane != "review" {
			t.Fatalf("Pane = %q, want %q for args %v", opts.Pane, "review", args)
		}
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
	if opts.JSON {
		t.Fatal("JSON = true, want false")
	}

	if _, err := ParseFocus([]string{"dogfood"}); err == nil {
		t.Fatal("ParseFocus returned nil error for missing pane name")
	}
}

func TestParseFocusAcceptsJSONFlagBeforeOrAfterTarget(t *testing.T) {
	tests := [][]string{
		{"--json", "dogfood", "resolve"},
		{"dogfood", "resolve", "--json"},
		{"dogfood", "--json", "resolve"},
	}

	for _, args := range tests {
		opts, err := ParseFocus(args)
		if err != nil {
			t.Fatalf("ParseFocus(%v) returned error: %v", args, err)
		}
		if !opts.JSON {
			t.Fatalf("JSON = false, want true for args %v", args)
		}
	}
}

func TestParseRestartRequiresSessionAndPaneName(t *testing.T) {
	opts, err := ParseRestart([]string{"dogfood", "resolve"})
	if err != nil {
		t.Fatalf("ParseRestart returned error: %v", err)
	}
	if opts.Session != "dogfood" {
		t.Fatalf("Session = %q, want %q", opts.Session, "dogfood")
	}
	if opts.Pane != "resolve" {
		t.Fatalf("Pane = %q, want %q", opts.Pane, "resolve")
	}

	if _, err := ParseRestart([]string{"dogfood"}); err == nil {
		t.Fatal("ParseRestart returned nil error for missing pane name")
	}
}

func TestParseRerunRequiresSessionAndPaneName(t *testing.T) {
	opts, err := ParseRerun([]string{"dogfood", "resolve"})
	if err != nil {
		t.Fatalf("ParseRerun returned error: %v", err)
	}
	if opts.Session != "dogfood" {
		t.Fatalf("Session = %q, want %q", opts.Session, "dogfood")
	}
	if opts.Pane != "resolve" {
		t.Fatalf("Pane = %q, want %q", opts.Pane, "resolve")
	}

	if _, err := ParseRerun([]string{"dogfood"}); err == nil {
		t.Fatal("ParseRerun returned nil error for missing pane name")
	}
}

func TestParseStopRequiresSessionAndPaneName(t *testing.T) {
	opts, err := ParseStop([]string{"dogfood", "resolve"})
	if err != nil {
		t.Fatalf("ParseStop returned error: %v", err)
	}
	if opts.Session != "dogfood" {
		t.Fatalf("Session = %q, want %q", opts.Session, "dogfood")
	}
	if opts.Pane != "resolve" {
		t.Fatalf("Pane = %q, want %q", opts.Pane, "resolve")
	}

	if _, err := ParseStop([]string{"dogfood"}); err == nil {
		t.Fatal("ParseStop returned nil error for missing pane name")
	}
}

func TestParseKillRequiresSessionAndPaneName(t *testing.T) {
	opts, err := ParseKill([]string{"dogfood", "resolve"})
	if err != nil {
		t.Fatalf("ParseKill returned error: %v", err)
	}
	if opts.Session != "dogfood" {
		t.Fatalf("Session = %q, want %q", opts.Session, "dogfood")
	}
	if opts.Pane != "resolve" {
		t.Fatalf("Pane = %q, want %q", opts.Pane, "resolve")
	}

	if _, err := ParseKill([]string{"dogfood"}); err == nil {
		t.Fatal("ParseKill returned nil error for missing pane name")
	}
}

func TestParseCaptureRequiresSessionAndPaneName(t *testing.T) {
	opts, err := ParseCapture([]string{"dogfood", "resolve"})
	if err != nil {
		t.Fatalf("ParseCapture returned error: %v", err)
	}
	if opts.Session != "dogfood" {
		t.Fatalf("Session = %q, want %q", opts.Session, "dogfood")
	}
	if opts.Pane != "resolve" {
		t.Fatalf("Pane = %q, want %q", opts.Pane, "resolve")
	}
	if opts.JSON {
		t.Fatal("JSON = true, want false")
	}
	if opts.Tail != 0 {
		t.Fatalf("Tail = %d, want 0", opts.Tail)
	}
	if opts.StripEmpty {
		t.Fatal("StripEmpty = true, want false")
	}

	if _, err := ParseCapture([]string{"dogfood"}); err == nil {
		t.Fatal("ParseCapture returned nil error for missing pane name")
	}
}

func TestParseCaptureAcceptsJSONFlagBeforeOrAfterTarget(t *testing.T) {
	tests := [][]string{
		{"--json", "dogfood", "resolve"},
		{"dogfood", "resolve", "--json"},
		{"dogfood", "--json", "resolve"},
	}

	for _, args := range tests {
		opts, err := ParseCapture(args)
		if err != nil {
			t.Fatalf("ParseCapture(%v) returned error: %v", args, err)
		}
		if opts.Session != "dogfood" {
			t.Fatalf("Session = %q, want %q", opts.Session, "dogfood")
		}
		if opts.Pane != "resolve" {
			t.Fatalf("Pane = %q, want %q", opts.Pane, "resolve")
		}
		if !opts.JSON {
			t.Fatalf("JSON = false, want true for args %v", args)
		}
	}
}

func TestParseCaptureAcceptsTailFlagBeforeOrAfterTarget(t *testing.T) {
	tests := [][]string{
		{"--tail", "10", "dogfood", "resolve"},
		{"dogfood", "resolve", "--tail", "10"},
		{"dogfood", "--tail", "10", "resolve"},
		{"--json", "dogfood", "--tail", "10", "resolve"},
	}

	for _, args := range tests {
		opts, err := ParseCapture(args)
		if err != nil {
			t.Fatalf("ParseCapture(%v) returned error: %v", args, err)
		}
		if opts.Tail != 10 {
			t.Fatalf("Tail = %d, want 10 for args %v", opts.Tail, args)
		}
	}
}

func TestParseCaptureAcceptsStripEmptyFlagBeforeOrAfterTarget(t *testing.T) {
	tests := [][]string{
		{"--strip-empty", "dogfood", "resolve"},
		{"dogfood", "resolve", "--strip-empty"},
		{"dogfood", "--strip-empty", "resolve"},
		{"--json", "dogfood", "--strip-empty", "resolve"},
		{"dogfood", "--tail", "10", "--strip-empty", "resolve"},
	}

	for _, args := range tests {
		opts, err := ParseCapture(args)
		if err != nil {
			t.Fatalf("ParseCapture(%v) returned error: %v", args, err)
		}
		if !opts.StripEmpty {
			t.Fatalf("StripEmpty = false, want true for args %v", args)
		}
	}
}

func TestParseCaptureRejectsInvalidTailValue(t *testing.T) {
	for _, args := range [][]string{
		{"dogfood", "resolve", "--tail", "0"},
		{"dogfood", "resolve", "--tail", "-1"},
		{"dogfood", "resolve", "--tail", "abc"},
	} {
		if _, err := ParseCapture(args); err == nil {
			t.Fatalf("ParseCapture(%v) returned nil error for invalid --tail", args)
		}
	}
}

func TestParseSendRequiresSessionPaneAndCommand(t *testing.T) {
	opts, err := ParseSend([]string{"dogfood", "resolve", "go", "test", "./..."})
	if err != nil {
		t.Fatalf("ParseSend returned error: %v", err)
	}
	if opts.Session != "dogfood" {
		t.Fatalf("Session = %q, want %q", opts.Session, "dogfood")
	}
	if opts.Pane != "resolve" {
		t.Fatalf("Pane = %q, want %q", opts.Pane, "resolve")
	}
	if opts.Command != "go test ./..." {
		t.Fatalf("Command = %q, want %q", opts.Command, "go test ./...")
	}
	if opts.NoEnter {
		t.Fatal("NoEnter = true, want false")
	}
	if opts.JSON {
		t.Fatal("JSON = true, want false")
	}

	if _, err := ParseSend([]string{"dogfood", "resolve"}); err == nil {
		t.Fatal("ParseSend returned nil error for missing command")
	}
}

func TestParseSendAcceptsNoEnterFlag(t *testing.T) {
	opts, err := ParseSend([]string{"dogfood", "resolve", "--no-enter", "printf", "'hi'"})
	if err != nil {
		t.Fatalf("ParseSend returned error: %v", err)
	}
	if !opts.NoEnter {
		t.Fatal("NoEnter = false, want true")
	}
	if opts.Command != "printf 'hi'" {
		t.Fatalf("Command = %q, want %q", opts.Command, "printf 'hi'")
	}
}

func TestParseSendAcceptsJSONAndNoEnterFlags(t *testing.T) {
	opts, err := ParseSend([]string{"dogfood", "resolve", "--json", "--no-enter", "printf", "'hi'"})
	if err != nil {
		t.Fatalf("ParseSend returned error: %v", err)
	}
	if !opts.JSON {
		t.Fatal("JSON = false, want true")
	}
	if !opts.NoEnter {
		t.Fatal("NoEnter = false, want true")
	}
}

func TestParseSendAllowsDashedCommandArgsAfterDoubleDash(t *testing.T) {
	opts, err := ParseSend([]string{"dogfood", "resolve", "--no-enter", "--", "--no-enter"})
	if err != nil {
		t.Fatalf("ParseSend returned error: %v", err)
	}
	if !opts.NoEnter {
		t.Fatal("NoEnter = false, want true")
	}
	if opts.Command != "--no-enter" {
		t.Fatalf("Command = %q, want %q", opts.Command, "--no-enter")
	}
}
