package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

// GuidedOptions describes one owner-requested coding-and-review journey.
// Candidate overrides are explicit owner configuration, useful for repeatable
// command-only checks as well as selecting supported native profiles.
type GuidedOptions struct {
	Repository          string
	ProcessContainment  string
	Goal                string
	Output              string
	CheckCommand        []string
	TimeoutSeconds      int
	MaxAttempts         int
	CheckTimeoutSeconds int
	Coder               *Candidate
	Reviewer            *Candidate
}

// GuidedPreview is read-only preflight evidence. It does not establish that a
// provider is signed in or a later check will pass. Preparing it starts no
// executable and creates no output, Git object, ref, branch or worktree.
type GuidedPreview struct {
	Plan           Plan              `json:"plan"`
	Selection      SnapshotSelection `json:"selection"`
	Repository     string            `json:"repository"`
	Output         string            `json:"output"`
	CheckSource    string            `json:"check_source"`
	Phases         []string          `json:"phases"`
	Authority      string            `json:"authority"`
	MaxNativeTurns int               `json:"max_native_turns"`
}

const guidedReviewSchema = `{"type":"object","additionalProperties":false,"required":["decision","summary","findings"],"properties":{"decision":{"type":"string","enum":["approve","request_changes"]},"summary":{"type":"string","minLength":1,"maxLength":2048},"findings":{"type":"array","maxItems":16,"items":{"type":"object","additionalProperties":false,"required":["file","line","message"],"properties":{"file":{"type":"string","minLength":1,"maxLength":512},"line":{"type":"integer","minimum":1,"maximum":10000000},"message":{"type":"string","minLength":1,"maxLength":512}}}}}}`

const guidedReviewInstructions = `Review the current worktree against the owner's objective and recorded implementation. Read the actual files using native file tools. Do not edit, create or delete files. Do not delegate or invoke other models. This session permits native file tools; do not use terminal commands, RunCommand, ls or pwd. No directory listing is needed. Return JSON only, with no Markdown fences, in exactly this shape: {"decision":"approve"|"request_changes","summary":"specific conclusion","findings":[{"file":"relative/path","line":1,"message":"specific actionable defect"}]}. Approve requires an empty findings array. Request_changes requires at least one actionable finding. An approval means the inspected revision satisfies this review; it is not a guarantee of general correctness.`

// PrepareGuided validates a useful end-to-end plan before any model can run.
// Default checks are deliberately narrow: Go tests, or an existing npm test
// script. Multi-stack and unsupported projects need an explicit argv command.
func PrepareGuided(ctx context.Context, options GuidedOptions) (GuidedPreview, error) {
	var preview GuidedPreview
	if err := ctx.Err(); err != nil {
		return preview, err
	}
	if strings.TrimSpace(options.Goal) == "" || len(options.Goal) > 128<<10 || !utf8.ValidString(options.Goal) {
		return preview, errors.New("describe the work in 1–131072 bytes of valid UTF-8")
	}
	if !utf8.ValidString(options.Repository) || !utf8.ValidString(options.Output) {
		return preview, errors.New("project and output paths must be valid UTF-8 so retained evidence preserves their exact identity")
	}
	for _, argument := range options.CheckCommand {
		if !utf8.ValidString(argument) {
			return preview, errors.New("check command arguments must be valid UTF-8")
		}
	}
	repository := options.Repository
	if repository == "" {
		repository = "."
	}
	repo, err := repositoryRoot(ctx, repository)
	if err != nil {
		return preview, fmt.Errorf("choose an existing Git project: %w", err)
	}
	if _, err := gitText(ctx, repo, nil, "rev-parse", "--verify", "HEAD^{commit}"); err != nil {
		return preview, errors.New("the project needs an initial Git commit before work can be captured")
	}
	selection, err := SnapshotUsage(ctx, repo)
	if err != nil {
		return preview, fmt.Errorf("snapshot admission: %w", err)
	}
	output, err := guidedOutput(repo, options.Output)
	if err != nil {
		return preview, err
	}
	checks, checkSource, err := guidedChecks(repo, options.CheckCommand)
	if err != nil {
		return preview, err
	}
	checkTimeout := options.CheckTimeoutSeconds
	if checkTimeout == 0 {
		checkTimeout = 60
	}
	timeout, attempts := options.TimeoutSeconds, options.MaxAttempts
	if timeout == 0 {
		timeout = 600
	}
	if attempts == 0 {
		attempts = 2
	}
	coder, err := guidedCandidate("coder", options.Coder)
	if err != nil {
		return preview, err
	}
	reviewer, err := guidedCandidate("reviewer", options.Reviewer)
	if err != nil {
		return preview, err
	}
	containment := options.ProcessContainment
	if containment == "" {
		containment = "process-group"
	}
	plan := Plan{
		Version: 1, Kind: "work", Repository: repo, Goal: options.Goal, Execution: "local", Mode: "mission",
		ProcessContainment: containment,
		TimeoutSeconds:     timeout, MaxAttempts: attempts, MaxParallel: 1,
		Candidates: []Candidate{coder, reviewer},
		Checks:     []Check{{Name: "project-check", Command: checks, TimeoutSeconds: checkTimeout}},
		Steps: []Step{
			{ID: "implement", Candidate: coder.ID, Verify: true, Goal: "Implement the requested work in this worktree. Preserve unrelated work, do not delegate or invoke other models, and use the declared checks to validate the changes. Report what changed and any remaining limitations."},
			{ID: "review", Candidate: reviewer.ID, DependsOn: []string{"implement"}, ReadOnly: true, Review: true, RequireResult: true, Goal: "Inspect the implemented revision. Report actionable defects and return the required structured review. Do not change files."},
			{ID: "address-review", Candidate: coder.ID, DependsOn: []string{"implement", "review"}, Verify: true, Goal: "Address the preceding review's actionable findings and rerun the declared checks. If the review approved without findings, preserve the implementation and report that no changes were needed. Do not make unrelated improvements, delegate or invoke other models."},
			{ID: "final-review", Candidate: reviewer.ID, DependsOn: []string{"implement", "review", "address-review"}, ReadOnly: true, Review: true, GateReview: true, RequireResult: true, Goal: "Inspect the exact final revision after the review response. Return the required structured review. Approve only when no actionable defects remain; otherwise request_changes and identify them. Do not change files."},
			{ID: "final-check", Verify: true, DependsOn: []string{"final-review"}},
		},
	}
	if err := ValidatePlan(plan); err != nil {
		return preview, err
	}
	for _, candidate := range plan.Candidates {
		binary := candidate.Harness
		if candidate.Harness == "antigravity" {
			binary = "agy"
		}
		if len(candidate.Command) > 0 {
			binary = candidate.Command[0]
		}
		if err := guidedExecutable(repo, binary); err != nil {
			return preview, fmt.Errorf("%s is unavailable: %w; install and sign in to its CLI before starting work", candidate.ID, err)
		}
	}
	if err := guidedExecutable(repo, checks[0]); err != nil {
		return preview, fmt.Errorf("project check is unavailable: %w; select an installed check command after --", err)
	}
	if err := ctx.Err(); err != nil {
		return preview, err
	}
	authority := "Trusted local filesystem and network authority. Process groups bound ordinary descendants. Integration requires an explicit apply command."
	if containment == "pid-namespace" {
		authority = "Linux PID namespace containment is required and probed before execution. Filesystem and network retain trusted local authority. Integration requires an explicit apply command."
	}
	return GuidedPreview{
		Plan: plan, Selection: selection, Repository: repo, Output: output, CheckSource: checkSource,
		Phases:         []string{"Implement and check", "Review the implementation", "Address review findings", "Review the final revision", "Run final checks"},
		Authority:      authority,
		MaxNativeTurns: 2*attempts + 2,
	}, nil
}

func RunGuided(ctx context.Context, options GuidedOptions, allowModels bool) (Report, error) {
	preview, err := PrepareGuided(ctx, options)
	if err != nil {
		return Report{}, err
	}
	return RunMission(ctx, preview.Plan, preview.Output, allowModels)
}

func guidedCandidate(role string, override *Candidate) (Candidate, error) {
	var c Candidate
	if override != nil {
		c = *override
		c.Command = append([]string(nil), override.Command...)
		if override.Env != nil {
			c.Env = make(map[string]string, len(override.Env))
			for k, v := range override.Env {
				c.Env[k] = v
			}
		}
	} else if role == "coder" {
		c.Harness = "codex"
	} else {
		c.Harness = "antigravity"
	}
	if c.ID == "" {
		c.ID = role
	}
	for _, value := range append([]string{c.Harness, c.ID, c.Model, c.BaseURL, c.APIKeyEnv, c.Prompt}, c.Command...) {
		if !utf8.ValidString(value) {
			return c, errors.New("guided profile strings must be valid UTF-8")
		}
	}
	for key, value := range c.Env {
		if !utf8.ValidString(key) || !utf8.ValidString(value) {
			return c, errors.New("guided profile environment must be valid UTF-8")
		}
	}
	switch c.Harness {
	case "codex":
		if c.Model == "" {
			c.Model = "gpt-6.1-sol"
		}
		if c.TimeoutSeconds == 0 {
			c.TimeoutSeconds = 180
		}
	case "antigravity":
		if c.Model == "" {
			c.Model = "gemini-3.8-flash-low"
		}
		if !strings.HasPrefix(c.Model, "gemini-") {
			return c, errors.New("the guided Antigravity profile requires an explicitly selected Gemini model")
		}
		if len(c.Command) == 0 {
			mode := "accept-edits"
			if role == "reviewer" {
				mode = "plan"
			}
			c.Command = []string{"agy", "--mode", mode, "--print-timeout", "90s"}
			if role == "reviewer" {
				c.Command = append(c.Command, "--json-schema", guidedReviewSchema)
			}
		}
		if c.TimeoutSeconds == 0 {
			c.TimeoutSeconds = 90
		}
	case "command":
		// Explicit owner/test fixture argv. A command's name does not establish
		// whether it will use a model; the normal local authority boundary applies.
	default:
		return c, errors.New("guided work supports Codex, Antigravity/Gemini or explicit owner command profiles")
	}
	if role == "reviewer" {
		if c.Prompt != "" {
			c.Prompt += "\n\n"
		}
		c.Prompt += guidedReviewInstructions
	} else if c.Harness == "antigravity" {
		c.Prompt += "\nUse native read_file/write_file tools directly in the current worktree. Do not use RunCommand, ls or pwd; terminal commands are not approved in this profile."
	}
	return c, nil
}

func guidedChecks(repo string, explicit []string) ([]string, string, error) {
	if len(explicit) > 0 {
		return append([]string(nil), explicit...), "explicit command", nil
	}
	goFile := filepath.Join(repo, "go.mod")
	goProject := false
	if _, err := os.Lstat(goFile); err == nil {
		if _, err = ReadRegularFile(goFile, 1<<20); err != nil {
			return nil, "", fmt.Errorf("cannot inspect go.mod safely: %w; provide an explicit check command after --", err)
		}
		goProject = true
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, "", err
	}
	npmProject := false
	packageFile := filepath.Join(repo, "package.json")
	if _, err := os.Lstat(packageFile); err == nil {
		data, err := ReadRegularFile(packageFile, 1<<20)
		if err != nil {
			return nil, "", fmt.Errorf("cannot inspect package.json safely: %w; provide an explicit check command after --", err)
		}
		var pkg map[string]json.RawMessage
		if err = DecodeJSON(data, &pkg); err != nil {
			return nil, "", fmt.Errorf("cannot inspect package.json: %w; provide an explicit check command after --", err)
		}
		var scripts map[string]json.RawMessage
		if raw, ok := pkg["scripts"]; ok {
			if err = DecodeJSON(raw, &scripts); err != nil {
				return nil, "", fmt.Errorf("cannot inspect package.json scripts: %w; provide an explicit check command after --", err)
			}
			var test string
			if raw, ok := scripts["test"]; ok {
				if err = json.Unmarshal(raw, &test); err != nil {
					return nil, "", errors.New("package.json scripts.test must be a string; provide an explicit check command after --")
				}
				npmProject = strings.TrimSpace(test) != ""
			}
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, "", err
	}
	if goProject && npmProject {
		return nil, "", errors.New("this project has both Go and npm tests; select the intended check command after --")
	}
	if goProject {
		return []string{"go", "test", "./..."}, "Go tests detected from go.mod", nil
	}
	if npmProject {
		return []string{"npm", "test"}, "npm test detected from package.json", nil
	}
	return nil, "", errors.New("no supported project check was detected; provide its executable argv after -- (for example: -- go test ./...)")
}

func guidedExecutable(repo, binary string) error {
	if binary == "" {
		return errors.New("empty executable")
	}
	if strings.ContainsRune(binary, filepath.Separator) && !filepath.IsAbs(binary) {
		binary = filepath.Join(repo, binary)
	}
	if _, err := exec.LookPath(binary); err != nil {
		return fmt.Errorf("executable %q was not found or is not executable", binary)
	}
	return nil
}

// guidedOutput permits a not-yet-created private catalog parent during preview.
// It resolves the nearest existing ancestor and checks the resulting path, but
// leaves creation and writability checks to the actual catalog/run operation.
func guidedOutput(repo, requested string) (string, error) {
	if strings.TrimSpace(requested) == "" {
		return "", errors.New("choose a new output directory outside the project")
	}
	abs, err := filepath.Abs(requested)
	if err != nil {
		return "", err
	}
	if _, err := os.Lstat(abs); err == nil {
		return "", errors.New("work output already exists; choose a new run directory")
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	ancestor := filepath.Dir(abs)
	suffix := []string{filepath.Base(abs)}
	for {
		info, err := os.Lstat(ancestor)
		if err == nil {
			if info.Mode()&os.ModeSymlink != 0 {
				resolved, resolveErr := filepath.EvalSymlinks(ancestor)
				if resolveErr != nil {
					return "", resolveErr
				}
				info, err = os.Stat(resolved)
				if err != nil {
					return "", err
				}
			}
			if !info.IsDir() {
				return "", errors.New("work output parent is not a directory")
			}
			break
		}
		if !errors.Is(err, os.ErrNotExist) || ancestor == filepath.Dir(ancestor) {
			return "", err
		}
		suffix = append(suffix, filepath.Base(ancestor))
		ancestor = filepath.Dir(ancestor)
	}
	ancestor, err = filepath.EvalSymlinks(ancestor)
	if err != nil {
		return "", err
	}
	for i := len(suffix) - 1; i >= 0; i-- {
		ancestor = filepath.Join(ancestor, suffix[i])
	}
	rel, err := filepath.Rel(repo, ancestor)
	if err != nil {
		return "", err
	}
	if rel == "." || rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", errors.New("work output must be outside the project")
	}
	return ancestor, nil
}
