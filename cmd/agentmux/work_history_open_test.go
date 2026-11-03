package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"agentmux/internal/workflow"
)

func TestGuidedHistoryOpenRejectsChangedRunIdentityBeforeExport(t *testing.T) {
	_, _, reports := guidedHistoryRuns(t, "Selected task")
	calls := 0
	captureGuidedReportOpener(t, func(context.Context, string) error { calls++; return nil })
	var out bytes.Buffer
	err := openGuidedHistoryReport(context.Background(), reports[0].Output, "different-selected-id", &out)
	if err == nil || !strings.Contains(err.Error(), "identity changed") || calls != 0 || out.Len() != 0 {
		t.Fatalf("identity mismatch reached publication: %v %d %s", err, calls, out.String())
	}
	paths, err := filepath.Glob(filepath.Join(reports[0].Output, ".report-open-*"))
	if err != nil || len(paths) != 0 {
		t.Fatalf("identity refusal left an export: %v %v", paths, err)
	}
}

func TestGuidedHistoryShowCommandOmitsPathsWhoseDisplayChanges(t *testing.T) {
	for _, field := range []string{"directory", "repository"} {
		for _, control := range []string{"\x1b[31m", "\u202e", "\n"} {
			summary := workflow.WorkSummary{Directory: "/private/run", Repository: "/project"}
			if field == "directory" {
				summary.Directory += control
			} else {
				summary.Repository += control
			}
			if command := guidedHistoryShowCommand(summary); command != "" {
				t.Fatalf("offered inaccurate command for %s: %q", field, command)
			}
		}
	}
}

func captureGuidedReportOpener(t *testing.T, open func(context.Context, string) error) {
	t.Helper()
	old := guidedHistoryReportOpener
	guidedHistoryReportOpener = open
	t.Cleanup(func() { guidedHistoryReportOpener = old })
}

func TestGuidedHistoryOpenRendersFreshReportForExactOlderRun(t *testing.T) {
	repo, catalog, reports := guidedHistoryRuns(t, "First task", "Second task")
	older, newer := reports[0], reports[1]
	original := filepath.Join(older.Output, "report.html")
	untrusted := []byte("<script>arbitrary saved HTML</script>")
	if err := os.WriteFile(original, untrusted, 0600); err != nil {
		t.Fatal(err)
	}
	reportPath := filepath.Join(older.Output, "report.json")
	before, err := os.ReadFile(reportPath)
	if err != nil {
		t.Fatal(err)
	}
	opened := ""
	captureGuidedReportOpener(t, func(_ context.Context, value string) error {
		opened = value
		return nil
	})
	out, _ := invokeGuidedCLI(t, exitSuccess, "show", older.ID, "--repo", repo, "--catalog", catalog, "--open")
	file, err := url.Parse(opened)
	if err != nil || file.Scheme != "file" || file.Host != "" || file.RawQuery != "" || file.Fragment != "" {
		t.Fatalf("expected one file URL, got %q: %v", opened, err)
	}
	path := filepath.FromSlash(file.Path)
	if filepath.Dir(filepath.Dir(path)) != older.Output || filepath.Base(path) != "report.html" || path == original {
		t.Fatalf("opened file was not a fresh report inside the exact older run: %s", path)
	}
	page, err := os.ReadFile(path)
	if err != nil || !bytes.Contains(page, []byte(older.Goal)) || bytes.Contains(page, untrusted) || bytes.Contains(page, []byte("<script")) {
		t.Fatalf("fresh report was not safely rendered from selected JSON: %v\n%s", err, page)
	}
	for _, item := range []struct {
		path string
		mode os.FileMode
	}{{path, 0600}, {filepath.Dir(path), 0700}} {
		info, err := os.Stat(item.path)
		if err != nil || info.Mode().Perm() != item.mode {
			t.Fatalf("report export is not private: %s: %v", item.path, err)
		}
	}
	if !strings.Contains(out, "Fresh report for run "+older.ID) || !strings.Contains(out, path) || strings.Contains(out, newer.ID) {
		t.Fatalf("output omitted exact selected run or fresh report path: %s", out)
	}
	after, err := os.ReadFile(reportPath)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("opening changed the saved run record")
	}
	saved, err := os.ReadFile(original)
	if err != nil || !bytes.Equal(saved, untrusted) {
		t.Fatal("opening changed the preexisting HTML")
	}
}

func TestGuidedHistoryOpenRejectsJSONAndAmbiguousArgumentsBeforeOpening(t *testing.T) {
	calls := 0
	captureGuidedReportOpener(t, func(context.Context, string) error { calls++; return nil })
	for _, args := range [][]string{
		{"show", "missing", "--open", "--json"},
		{"show", "--open"},
		{"show", "", "--open"},
		{"show", "first", "second", "--open"},
		{"show", "first", "--open", "--open"},
		{"status", "first", "--open"},
		{"list", "--open"},
		{"apply", "first", "--open"},
		{"recover", "first", "--open"},
	} {
		out, _ := invokeGuidedCLI(t, exitValidation, args...)
		if strings.Contains(strings.Join(args, " "), "--json") && !json.Valid([]byte(out)) {
			t.Fatalf("--json rejection did not retain structured errors: %s", out)
		}
	}
	if calls != 0 {
		t.Fatalf("invalid options opened a browser %d times", calls)
	}
}

func TestGuidedHistoryOpenFailureRetainsFreshReportAndExactRun(t *testing.T) {
	repo, catalog, reports := guidedHistoryRuns(t, "Retain report")
	opened := ""
	captureGuidedReportOpener(t, func(_ context.Context, value string) error {
		opened = value
		return errors.New("no desktop available")
	})
	out, diagnostics := invokeGuidedCLI(t, exitRuntime, "show", reports[0].ID, "--repo", repo, "--catalog", catalog, "--open")
	file, err := url.Parse(opened)
	if err != nil || file.Scheme != "file" {
		t.Fatalf("no generated report was offered to the opener: %q", opened)
	}
	if info, err := os.Lstat(filepath.FromSlash(file.Path)); err != nil || !info.Mode().IsRegular() {
		t.Fatalf("opener failure removed the generated report: %v", err)
	}
	if !strings.Contains(out, reports[0].ID) || !strings.Contains(out, file.Path) || !strings.Contains(diagnostics, "fresh report is retained") {
		t.Fatalf("opener failure omitted selected run and retained report: %s\n%s", out, diagnostics)
	}
}

func TestGuidedHistoryReportURLRequiresRegularFileInsideSelectedRun(t *testing.T) {
	root := t.TempDir()
	run := filepath.Join(root, "run ' # with spaces")
	if err := os.Mkdir(run, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(run, "report '#.html")
	if err := os.WriteFile(path, []byte("report"), 0600); err != nil {
		t.Fatal(err)
	}
	value, err := guidedHistoryReportURL(run, path)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(value)
	if err != nil || filepath.FromSlash(parsed.Path) != path || parsed.Fragment != "" || parsed.RawQuery != "" || !strings.Contains(value, "%23") {
		t.Fatalf("file path did not survive URL escaping: %q: %v", value, err)
	}
	outside := filepath.Join(root, "outside.html")
	if err := os.WriteFile(outside, []byte("outside"), 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(run, "linked.html")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	for _, invalid := range []string{outside, link, run, filepath.Join(run, "missing.html")} {
		if _, err := guidedHistoryReportURL(run, invalid); err == nil {
			t.Fatalf("accepted report outside run or nonregular file: %s", invalid)
		}
	}
}
