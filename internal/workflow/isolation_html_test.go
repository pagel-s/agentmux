package workflow

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestIsolationHTMLPublicationGuardsExactFinalBytes(t *testing.T) {
	for _, fixture := range []struct{ name, secret, goal string }{
		{"static-css", "font-size:1.3rem", "Safe report goal"},
		{"data", "selected-export-collision-9382", "selected-export-collision-9382"},
		{"escaped-data", "<selected-export-collision-9382>", "<selected-export-collision-9382>"},
		{"html-entity", "amp;entity-collision-9382", "&entity-collision-9382"},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			dir, report := evidenceRun(t)
			report.Goal = fixture.goal
			if fixture.name == "html-entity" && strings.Contains(report.Goal, fixture.secret) {
				t.Fatal("encoded collision fixture already contains selected bytes")
			}
			if err := saveJSON(filepath.Join(dir, "report.json"), report); err != nil {
				t.Fatal(err)
			}
			destination := t.TempDir()
			output := filepath.Join(destination, "refused.html")
			path, err := RenderReportToContext(isolationJSONLiteralContext(fixture.secret), dir, output)
			if err == nil || path != "" || strings.Contains(err.Error(), fixture.secret) {
				t.Fatal("HTML publication did not safely refuse selected final bytes")
			}
			entries, readErr := os.ReadDir(destination)
			if readErr != nil || len(entries) != 0 {
				t.Fatalf("refused HTML publication created files: %v", readErr)
			}
		})
	}
}

func TestIsolationHTMLPublicationGuardsOutputPathAndErrors(t *testing.T) {
	const secret = "selected-export-path-9382"
	dir, _ := evidenceRun(t)
	destination := t.TempDir()
	path, err := RenderReportToContext(isolationJSONLiteralContext(secret), dir, filepath.Join(destination, secret+".html"))
	if err == nil || path != "" || strings.Contains(err.Error(), secret) {
		t.Fatal("HTML output path was not safely refused")
	}
	entries, readErr := os.ReadDir(destination)
	if readErr != nil || len(entries) != 0 {
		t.Fatal("refused HTML output path created files")
	}
	if _, err := RenderReportToContext(isolationJSONLiteralContext(secret), filepath.Join(destination, secret), filepath.Join(destination, "unused.html")); err == nil || strings.Contains(err.Error(), secret) {
		t.Fatal("HTML loading diagnostic retained selected path bytes")
	}
}

func TestLegacyHTMLExportDoesNotDiscoverAmbientCredentials(t *testing.T) {
	const secret = "font-size:1.3rem"
	dir, _ := evidenceRun(t)
	t.Setenv("OPENAI_API_KEY", secret)
	output := filepath.Join(t.TempDir(), "standalone.html")
	if _, err := RenderReportTo(dir, output); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(output)
	if err != nil || !bytes.Contains(data, []byte(secret)) {
		t.Fatal("legacy export implicitly selected ambient credential material")
	}
}

func TestWorkflowIsolationRealHTMLStaticCredentialRefusal(t *testing.T) {
	requireFullWorkflowIsolation(t)
	testWorkflowIsolationRealHTMLCredentialRefusal(t, "font-size:1.3rem", "print('safe fixture result')")
}

func TestWorkflowIsolationRealHTMLEntityCredentialRefusal(t *testing.T) {
	requireFullWorkflowIsolation(t)
	testWorkflowIsolationRealHTMLCredentialRefusal(t, "amp;entity-collision-9382", "print('&entity-collision-9382')")
}

func testWorkflowIsolationRealHTMLCredentialRefusal(t *testing.T, secret, script string) {
	t.Helper()
	for _, mode := range []string{"run", "watch", "lab"} {
		t.Run(mode, func(t *testing.T) {
			repo, root := fixtureRepo(t)
			p := credentialGuardPlan(repo, secret)
			p.Candidates[0].Env = map[string]string{"OPENAI_API_KEY": secret}
			p.Candidates[0].Command = []string{"/usr/bin/python3", "-c", script}
			p.Checks = []Check{{Name: "acceptance", Command: []string{"/usr/bin/true"}}}
			output := filepath.Join(root, mode)
			runOutput := output
			var err error
			switch mode {
			case "run":
				report, runErr := Run(context.Background(), p, output, false)
				err = runErr
				if report.State != "failed" || strings.Contains(report.Error, secret) {
					t.Fatal("HTML publication refusal returned unsafe or completed run")
				}
			case "watch":
				report, runErr := Watch(context.Background(), p, output, false, WatchOptions{IntervalSeconds: 1, MaxRuns: 1, TimeoutSeconds: 20})
				err = runErr
				if report.State != "failed" || len(report.Runs) != 1 || report.Runs[0].State != "failed" || strings.Contains(report.Error, secret) {
					t.Fatal("HTML publication refusal returned unsafe or completed watch")
				}
				runOutput = report.Runs[0].Output
			case "lab":
				report, runErr := Lab(context.Background(), p, output, false, 1)
				err = runErr
				if report.State != "failed" || len(report.Runs) != 1 || report.Runs[0].State != "failed" || strings.Contains(report.Error, secret) {
					t.Fatal("HTML publication refusal returned unsafe or completed lab")
				}
				runOutput = report.Runs[0].Output
			}
			if err == nil || strings.Contains(err.Error(), secret) {
				t.Fatal("required HTML refusal was not returned safely")
			}
			report, loadErr := LoadReport(runOutput)
			if loadErr != nil || report.State != "failed" || strings.Contains(report.Error, secret) {
				t.Fatalf("failed HTML publication lost valid failed JSON evidence: %v", loadErr)
			}
			if _, statErr := os.Stat(filepath.Join(runOutput, "report.html")); !os.IsNotExist(statErr) {
				t.Fatal("selected template bytes reached automatic HTML artifact")
			}
			assertRetainedFilesExclude(t, output, secret, "")
		})
	}
}
