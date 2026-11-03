package workflow

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"html/template"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"
)

func writePrivateJSON(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return writeExclusive(path, append(data, '\n'))
}

func writeExclusive(path string, data []byte) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	_, writeErr := f.Write(data)
	if writeErr == nil {
		writeErr = f.Sync()
	}
	closeErr := f.Close()
	if writeErr != nil {
		return writeErr
	}
	if closeErr != nil {
		return closeErr
	}
	return syncParent(path)
}

func RenderReport(directory string) (string, error) {
	return RenderReportContext(context.Background(), directory)
}

// RenderReportContext retains the active workflow's credential guard through
// the final HTML publication boundary. Standalone exports do not reload auth.
func RenderReportContext(ctx context.Context, directory string) (string, error) {
	return RenderReportToContext(ctx, directory, filepath.Join(directory, "report.html"))
}

// RenderReportTo creates a standalone, escaped, script-free result view. Its
// freshness statements describe generation time; `flow explain` rechecks them.
func RenderReportTo(directory, path string) (string, error) {
	return RenderReportToContext(context.Background(), directory, path)
}

// RenderReportToContext checks the exact escaped page, including fixed template
// bytes and output paths, before creating an export. Rendering can introduce a
// selected literal through HTML entities even when the input JSON was safe.
func RenderReportToContext(ctx context.Context, directory, path string) (output string, renderErr error) {
	defer func() { renderErr = redactEarlyIsolationError(ctx, renderErr) }()
	r, err := LoadReport(directory)
	if err != nil {
		return "", err
	}
	e, err := Explain(directory)
	if err != nil {
		return "", err
	}
	if err = validateReportOutput(r, path); err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
	defer cancel()
	workSummary := summarizeReport(ctx, r)
	// A report written into a checked tree would immediately stale that tree.
	type row struct {
		EvidenceItem
		FreshLabel, ApplyCommand string
		DurationMillis           int64
	}
	rows := []row{}
	for _, v := range e.Items {
		label := "Not checked"
		if v.Verification != nil && !v.Verification.Passed {
			label = "Checks did not pass"
		} else if v.Fresh != nil {
			label = "Changed since checks"
			if *v.Fresh {
				label = "Matches checked content"
			}
		} else if v.FreshnessError != "" {
			label = "Current freshness unknown"
		}
		duration := int64(0)
		for _, c := range r.Candidates {
			if c.ID == v.ID {
				duration = c.DurationMillis
			}
		}
		command := ""
		if r.State == "completed" && v.Patch != "" && v.Verification != nil && v.Verification.Passed && v.Fresh != nil && *v.Fresh && v.PatchIntact && workSummary.SourceFresh != nil && *workSummary.SourceFresh && (r.Kind != "work" || workSummary.CanApply) && (r.Mode != "mission" || v.ID == "mission-result") {
			command = "amx flow apply " + shellWord(directory) + " --candidate " + shellWord(v.ID)
		}
		if !safeCommandText(command) {
			command = ""
		}
		rows = append(rows, row{EvidenceItem: v, FreshLabel: label, ApplyCommand: command, DurationMillis: duration})
	}
	view := struct {
		Report         Report
		Explanation    Explanation
		Rows           []row
		ExplainCommand string
		Summary        WorkSummary
		Headline       string
		FullGoal       string
		ReviewLabel    string
		SourceLabel    string
		ResultLabel    string
		PatchHeading   string
	}{Report: r, Explanation: e, Rows: rows, ExplainCommand: "amx flow explain " + shellWord(directory), Headline: shortGoal(r.Goal, 200)}
	view.Summary = workSummary
	view.SourceLabel = "Source freshness unknown"
	if workSummary.SourceFresh != nil {
		view.SourceLabel = "Source changed"
		if *workSummary.SourceFresh {
			view.SourceLabel = "Source unchanged"
		} else if workSummary.Outcome == "Applied to source" {
			view.SourceLabel = "Source matches applied result"
		}
	}
	view.ResultLabel = "Result not checked yet"
	if workSummary.ResultFreshnessError != "" {
		view.ResultLabel = "Result freshness unknown"
	} else if workSummary.ResultFresh != nil {
		view.ResultLabel = "Result changed or checks did not pass"
		if *workSummary.ResultFresh {
			view.ResultLabel = "Result matches checks"
		}
	} else if r.Mode == "compare" && len(r.Candidates) > 1 {
		view.ResultLabel = "See candidate freshness below"
	}
	view.PatchHeading = "Review retained patch"
	if workSummary.ChecksTotal > 0 && workSummary.ChecksPassed == workSummary.ChecksTotal && workSummary.ResultFresh != nil && *workSummary.ResultFresh {
		view.PatchHeading = "Review checked patch"
	}
	if !safeCommandText(view.ExplainCommand) {
		view.ExplainCommand = ""
	}
	if view.Headline != r.Goal {
		view.FullGoal = r.Goal
	}
	switch view.Summary.ReviewState {
	case "approve":
		view.ReviewLabel = "Approved review"
	case "request_changes":
		view.ReviewLabel = "Changes requested"
	default:
		view.ReviewLabel = "No structured review"
	}
	// Freshness diagnostics and other generated view data can introduce values
	// absent from report.json. Refuse those decoded values before HTML escaping
	// can conceal a selected credential from the final literal-byte check.
	if len(selectedIsolationCredentials(ctx)) > 0 {
		encoded, err := json.Marshal(view)
		if err != nil {
			return "", isolationEvidenceRefusal(ctx)
		}
		if _, changed, err := redactIsolationJSON(ctx, encoded); changed || err != nil {
			return "", isolationEvidenceRefusal(ctx)
		}
	}
	var b bytes.Buffer
	if err := resultPage.Execute(&b, view); err != nil {
		return "", err
	}
	if containsIsolationCredential(ctx, []byte(path)) || containsIsolationCredential(ctx, b.Bytes()) {
		return "", isolationEvidenceRefusal(ctx)
	}
	if err := writeExclusive(path, b.Bytes()); err != nil {
		return "", err
	}
	return path, nil
}

// ValidateReportOutput refuses exports inside any source or checked worktree,
// so exporting evidence cannot immediately invalidate the evidence it describes.
func ValidateReportOutput(directory, path string) error {
	r, err := LoadReport(directory)
	if err != nil {
		return err
	}
	return validateReportOutput(r, path)
}

func validateReportOutput(r Report, path string) error {
	protected := []string{r.Repository, r.Snapshot.Repository, r.Worktree}
	for _, c := range r.Candidates {
		protected = append(protected, c.Worktree)
	}
	for _, repo := range protected {
		if repo == "" {
			continue
		}
		if _, err := newOutsidePath(repo, path); err != nil {
			return fmt.Errorf("evidence output must be outside source and candidate worktrees: %w", err)
		}
	}
	return nil
}

func shellWord(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'" }

func displayReviewText(value string) string {
	var b strings.Builder
	for _, r := range value {
		if unicode.IsControl(r) && r != '\n' && r != '\t' || r == 0x061c || r == 0x200e || r == 0x200f || r >= 0x202a && r <= 0x202e || r >= 0x2066 && r <= 0x2069 {
			fmt.Fprintf(&b, "\\u%04x", r)
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}

var resultPage = template.Must(template.New("result").Funcs(template.FuncMap{"display": displayReviewText}).Parse(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta http-equiv="Content-Security-Policy" content="default-src 'none'; style-src 'unsafe-inline'; base-uri 'none'; form-action 'none'"><meta name="viewport" content="width=device-width,initial-scale=1"><meta name="color-scheme" content="dark light"><title>Agentmux · {{.Report.Goal}}</title>
<style>
:root{font:16px/1.6 system-ui,sans-serif;color:#eee8f8;background:#000}*{box-sizing:border-box}body{max-width:1100px;margin:auto;padding:32px 24px 80px}header{border-bottom:1px solid #574565;padding-bottom:24px}h1{font-size:2rem;line-height:1.25;margin:16px 0}h2{font-size:1.3rem;margin:0 0 12px}h3{font-size:1rem}p{max-width:85ch}.eyebrow{color:#c4a8ff;letter-spacing:.12em;font-size:.8rem;text-transform:uppercase}.meta{color:#c0b5ce;font-size:.9rem}.state{display:inline-block;border:1px solid #8d70b5;border-radius:5px;padding:2px 10px}section{margin-top:24px;border:1px solid #574565;border-radius:8px;padding:24px}code,pre{font-family:ui-monospace,monospace;font-size:.85rem}code{overflow-wrap:anywhere}pre{white-space:pre-wrap;overflow-wrap:anywhere;background:#1d1728;padding:14px;border-radius:5px;max-height:420px;overflow:auto}.facts{display:grid;grid-template-columns:repeat(auto-fit,minmax(220px,1fr));gap:16px}.facts strong{display:block;font-size:.8rem;font-weight:500;color:#c0b5ce}summary{cursor:pointer;padding:10px 0;color:#d1baff}summary:focus-visible{outline:2px solid #c4a8ff}.good{color:#9ce4b0}.bad{color:#ffb8b8}footer{border-top:1px solid #574565;margin-top:32px;padding-top:20px;color:#c0b5ce}@media(prefers-color-scheme:light){:root{color:#23172e;background:#faf7ff}.meta,.facts strong,footer{color:#5f506d}.eyebrow,summary{color:#69428c}pre{background:#eee7f5}.good{color:#17652b}.bad{color:#9c2525}}
.task{font-size:1.15rem;color:#d8cbe8}.badges{display:flex;flex-wrap:wrap;gap:8px}.next{border-left:4px solid #bc8dff;background:#100b19}.next pre{margin-bottom:0}.phases{display:flex;flex-wrap:wrap;gap:12px;margin:24px 0}.phases div{flex:1;min-width:135px;border-bottom:2px solid #644b80;padding:8px 0}.phases strong,.phases span{display:block}.phases span{font-size:.8rem;color:#c0b5ce}.files{list-style:none;padding:0}.files li{display:flex;gap:12px;align-items:baseline;padding:8px 0;border-top:1px solid #30243d}.execution{margin-top:28px}.evidence-heading{margin:36px 0 -8px}@media(prefers-color-scheme:light){.task{color:#50365e}.next{background:#f2eafc}.phases span{color:#5f506d}}
</style></head><body><header><div class="eyebrow">Agentmux · work review</div><h1>{{display .Summary.Outcome}}</h1><p class="task">{{display .Headline}}</p><div class="badges"><span class="state">{{.Summary.ChecksPassed}} / {{.Summary.ChecksTotal}} checks passed</span><span class="state">{{.ReviewLabel}}</span><span class="state">{{.SourceLabel}}</span><span class="state">{{.ResultLabel}}</span><span class="state">Recorded: {{.Report.State}}</span>{{if .Report.ReviewContext}}<span class="state">Session policy: {{.Report.ReviewContext}}</span>{{end}}</div>{{if .Report.Error}}<p class="bad">{{display .Report.Error}}</p>{{end}}</header>
<p class="meta">Saved report. Source and result freshness describe the moment this page was generated; use the CLI for current evidence.</p>
<section class="next" aria-label="Next step"><h2>Next step</h2><p>{{display .Summary.NextAction}}</p>{{if .Summary.NextCommand}}<pre>{{.Summary.NextCommand}}</pre>{{end}}{{if .Summary.SourceFreshnessError}}<p class="bad">Source freshness: {{display .Summary.SourceFreshnessError}}</p>{{end}}{{if .Summary.ResultFreshnessError}}<p class="bad">Result freshness: {{display .Summary.ResultFreshnessError}}</p>{{end}}</section>
{{if .Summary.Phases}}<nav class="phases" aria-label="Recorded workflow phases">{{range .Summary.Phases}}<div><strong>{{.ID}}</strong><span>{{.State}}</span></div>{{end}}</nav>{{end}}
{{if .Summary.Review}}<section aria-label="Review conclusion"><h2>{{.ReviewLabel}}</h2><p>{{display .Summary.Review}}</p>{{if .Summary.Findings}}<ol>{{range .Summary.Findings}}<li><code>{{display .File}}:{{.Line}}</code> — {{display .Message}}</li>{{end}}</ol>{{end}}<p class="meta">This is the reviewing agent's assessment. Recorded acceptance checks are separate evidence.</p></section>{{end}}
{{if .Summary.Files}}<section aria-label="Changed files"><h2>Changed files{{if .Summary.FilesTruncated}} (first 100){{end}}</h2><ul class="files">{{range .Summary.Files}}<li><span class="state">{{.Status}}</span><code>{{display .Path}}</code></li>{{end}}</ul>{{if .Summary.FilesError}}<p class="bad">{{display .Summary.FilesError}}</p>{{end}}</section>{{end}}
{{if or .Summary.PatchPreview .Summary.PatchPreviewError}}<section aria-label="Saved patch"><h2>{{.PatchHeading}}</h2>{{if .Summary.PatchPreviewError}}<p class="bad">The retained patch could not be verified: {{display .Summary.PatchPreviewError}}. No preview is shown.</p>{{else}}<details><summary>Show saved patch{{if .Summary.PatchPreviewTruncated}} (excerpt){{end}}</summary><pre>{{.Summary.PatchPreview}}</pre><p class="meta">The complete saved patch matched its recorded digest before this preview was generated. Binary patches remain encoded Git patch data.</p></details>{{if .Summary.PatchPreviewTruncated}}<p>Preview limited to 64 KiB of displayed text. Inspect the complete artifact: <code>{{display .Summary.PatchPath}}</code>.</p>{{end}}{{end}}</section>{{end}}
<details class="execution"><summary>Task and execution details</summary>{{if .FullGoal}}<h2>Complete task</h2><pre>{{display .FullGoal}}</pre>{{end}}<div class="facts"><div><strong>Source repository</strong><code>{{display .Report.Repository}}</code></div><div><strong>Starting revision</strong><code>{{.Explanation.BaseCommit}}</code></div><div><strong>Started</strong>{{.Report.StartedAt}}</div><div><strong>Finished</strong>{{.Report.FinishedAt}}</div></div><p class="meta">{{.Report.Mode}} · {{.Report.Execution}} execution · {{.Report.ID}}</p></details>
<h2 class="evidence-heading">Recorded evidence</h2>
{{range .Rows}}<section><h2>{{.ID}} <span class="state">{{.State}}</span></h2><div class="facts"><div><strong>Harness</strong>{{.Harness}}</div><div><strong>Check freshness when generated</strong>{{.FreshLabel}}</div><div><strong>Execution duration</strong>{{.DurationMillis}} ms</div></div>{{if .DependsOn}}<p class="meta">Depends on: {{range .DependsOn}}{{.}} {{end}}</p>{{end}}{{if .Error}}<p class="bad">{{display .Error}}</p>{{end}}{{if .FreshnessError}}<p class="bad">{{display .FreshnessError}}</p>{{end}}
{{if .Verification}}<h3>Independent checks</h3>{{range .Verification.Checks}}<details><summary>{{if .Passed}}<span class="good">Passed</span>{{else}}<span class="bad">Failed</span>{{end}} · {{.Name}} · {{.DurationMillis}} ms</summary><pre>{{range .Command}}{{printf "%q " .}}{{end}}
{{display .Output}}{{if .Error}}
{{display .Error}}{{end}}</pre></details>{{end}}{{else}}<p>No independent check is recorded for this step.</p>{{end}}
{{if .Result}}<details><summary>Agent-reported result{{if .ResultTruncated}} (excerpt){{end}}</summary><pre>{{display .Result}}</pre></details>{{end}}
{{if .NativeEventsPath}}<details><summary>Recorded native events</summary><p>Adapter-visible events, including partial or failed turns. These local records do not attest hidden provider activity.</p>{{if .CredentialsRedacted}}<p>Selected credential values were redacted. The digest identifies the saved redacted evidence.</p>{{end}}{{if .RuntimeHarness}}<p>Tool runtime: <code>{{.RuntimeHarness}}</code>. The inference provider is recorded separately.</p>{{end}}<p><code>{{.NativeEventsPath}}</code></p><p>SHA256: <code>{{.NativeEventsSHA256}}</code></p></details>{{end}}
{{if .Input}}<details><summary>Prepared input{{if .InputTruncated}} (excerpt){{else}} supplied to this turn{{end}}</summary><pre>{{display .Input}}</pre>{{if .InputSHA256}}<p>Complete input SHA256: <code>{{.InputSHA256}}</code></p>{{end}}</details>{{end}}
{{if .EvidencePath}}<p class="meta">Retained turn evidence: <code>{{.EvidencePath}}</code></p>{{end}}
{{if .Patch}}<details><summary>Patch and integration</summary><p><code>{{.Patch}}</code></p><p>Recorded patch digest matches: {{.PatchIntact}}</p><p><code>{{.PatchSHA256}}</code></p>{{if .ApplyCommand}}<p>After settling concurrent source writers, explicitly apply this result:</p><pre>{{.ApplyCommand}}</pre><p>Application rechecks source and result freshness and requires passing verification. It leaves commits and publishing to you.</p>{{end}}</details>{{end}}
{{if .Revision}}<p class="meta">Result tree: <code>{{.Revision}}</code></p>{{end}}{{if .ContentSHA256}}<p class="meta">Result content SHA256: <code>{{.ContentSHA256}}</code></p>{{end}}</section>{{end}}
<footer><p>{{.Explanation.Coverage}}</p><p>This page is a saved view.{{if .ExplainCommand}} Run <code>{{.ExplainCommand}}</code> to inspect current freshness.{{else}} Inspect current freshness through the CLI using exact paths from the JSON evidence.{{end}} Snapshot storage: <code>{{.Summary.Storage}}</code>.{{if .Summary.Isolation}} Required filesystem isolation: <code>{{.Summary.Isolation}}</code>.{{else}} Local processes retain owner filesystem and network authority.{{end}} Process containment: <code>{{.Summary.ProcessContainment}}</code>.</p></footer></body></html>`))

// String supplies a concise human summary for callers that do not render HTML.
func (e Explanation) String() string {
	return fmt.Sprintf("%s: %s (%s), %d evidence items", e.ID, e.Goal, e.State, len(e.Items))
}
