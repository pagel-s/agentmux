package provider

import (
	"bytes"
	"os/exec"
	"strings"
)

type LookPathFunc func(string) (string, error)
type RunFunc func(path string, args ...string) (string, error)

type HealthIssue struct {
	Severity string
	Message  string
}

type HealthReport struct {
	Spec       Spec
	Launch     LaunchProfile
	BinaryPath string
	Issues     []HealthIssue
}

func (r HealthReport) HasErrors() bool {
	for _, issue := range r.Issues {
		if issue.Severity == "error" {
			return true
		}
	}
	return false
}

func CheckCLI(name string) HealthReport {
	return CheckCLIWith(name, exec.LookPath, func(path string, args ...string) (string, error) {
		cmd := exec.Command(path, args...)
		var buf bytes.Buffer
		cmd.Stdout = &buf
		cmd.Stderr = &buf
		err := cmd.Run()
		return buf.String(), err
	})
}

func CheckCLIWith(name string, lookPath LookPathFunc, run RunFunc) HealthReport {
	return checkCLIWith(name, lookPath, run)
}

func checkCLIWith(name string, lookPath LookPathFunc, run RunFunc) HealthReport {
	spec, ok := Lookup(name)
	if !ok {
		return HealthReport{
			Issues: []HealthIssue{{Severity: "error", Message: "unsupported provider"}},
		}
	}

	report := HealthReport{Spec: spec}
	report.Launch = defaultLaunchProfile(spec, spec.IntegrationMode, "")
	if spec.Binary == "" {
		return report
	}

	path, err := lookPath(spec.Binary)
	if err != nil {
		report.Issues = append(report.Issues, HealthIssue{Severity: "error", Message: "binary not found in PATH"})
		return report
	}
	report.BinaryPath = path

	if len(spec.HelpArgs) == 0 {
		report.Launch = ResolveLaunchWith(name, lookPath, run)
		return report
	}

	out, err := run(path, spec.HelpArgs...)
	if strings.TrimSpace(out) == "" && err != nil {
		report.Launch = defaultLaunchProfile(spec, fallbackLaunchMode(spec), "unable to inspect provider help output for native mode")
		report.Issues = append(report.Issues, HealthIssue{Severity: "warning", Message: "unable to inspect CLI help output"})
		return report
	}

	for _, token := range spec.RequiredHelpTokens {
		if !strings.Contains(out, token) {
			report.Issues = append(report.Issues, HealthIssue{
				Severity: "error",
				Message:  "missing expected CLI help token " + token,
			})
		}
	}

	if err != nil {
		report.Issues = append(report.Issues, HealthIssue{Severity: "warning", Message: "CLI help command exited non-zero but returned output"})
	}

	report.Launch = ResolveLaunchWith(name, lookPath, run)
	if report.Launch.Mode != spec.IntegrationMode && report.Launch.FallbackReason != "" {
		report.Issues = append(report.Issues, HealthIssue{
			Severity: "warning",
			Message:  report.Launch.FallbackReason,
		})
	}

	return report
}
