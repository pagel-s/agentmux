package workflow

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"agentmux/internal/harness"
)

// Plan is an owner-controlled, finite workflow. Local execution has the same
// filesystem authority as its owner; a worktree is not a sandbox.
type Plan struct {
	Kind               string      `json:"kind,omitempty"`
	Version            int         `json:"version"`
	Repository         string      `json:"repository"`
	Goal               string      `json:"goal"`
	Execution          string      `json:"execution"`
	ProcessContainment string      `json:"process_containment,omitempty"`
	Mode               string      `json:"mode,omitempty"`
	Candidates         []Candidate `json:"candidates"`
	Checks             []Check     `json:"checks"`
	Steps              []Step      `json:"steps,omitempty"`
	TimeoutSeconds     int         `json:"timeout_seconds,omitempty"`
	MaxAttempts        int         `json:"max_attempts,omitempty"`
	MaxParallel        int         `json:"max_parallel,omitempty"`
	expectedTree       string
	expectedContent    string
	requireResult      bool
	requireReadOnly    bool
}

type Candidate struct {
	ID             string            `json:"id"`
	Harness        string            `json:"harness"`
	Model          string            `json:"model,omitempty"`
	Command        []string          `json:"command,omitempty"`
	Env            map[string]string `json:"env,omitempty"`
	BaseURL        string            `json:"base_url,omitempty"`
	APIKeyEnv      string            `json:"api_key_env,omitempty"`
	Prompt         string            `json:"prompt,omitempty"`
	TimeoutSeconds int               `json:"timeout_seconds,omitempty"`
}

type Step struct {
	ReadOnly      bool     `json:"read_only,omitempty"`
	Review        bool     `json:"review,omitempty"`
	GateReview    bool     `json:"gate_review,omitempty"`
	ID            string   `json:"id"`
	Candidate     string   `json:"candidate,omitempty"`
	DependsOn     []string `json:"depends_on,omitempty"`
	Goal          string   `json:"goal,omitempty"`
	Verify        bool     `json:"verify,omitempty"`
	RequireResult bool     `json:"require_result,omitempty"`
}

type CandidateResult struct {
	Review               *ReviewResult   `json:"review,omitempty"`
	ReviewTruncated      bool            `json:"review_truncated,omitempty"`
	NativeEventsPath     string          `json:"native_events_path,omitempty"`
	NativeEventsSHA256   string          `json:"native_events_sha256,omitempty"`
	ID                   string          `json:"id"`
	Harness              string          `json:"harness"`
	Model                string          `json:"model,omitempty"`
	State                string          `json:"state"`
	Worktree             string          `json:"worktree"`
	Patch                string          `json:"patch,omitempty"`
	PatchSHA256          string          `json:"patch_sha256,omitempty"`
	StartedAt            string          `json:"started_at"`
	FinishedAt           string          `json:"finished_at,omitempty"`
	Result               string          `json:"result,omitempty"`
	Error                string          `json:"error,omitempty"`
	Attempts             int             `json:"attempts"`
	DurationMillis       int64           `json:"duration_millis"`
	Usage                json.RawMessage `json:"usage,omitempty"`
	UsageRecords         []AttemptUsage  `json:"usage_records,omitempty"`
	Verification         *Verification   `json:"verification,omitempty"`
	FinalSnapshot        *Snapshot       `json:"final_snapshot,omitempty"`
	StartingSnapshot     *Snapshot       `json:"starting_snapshot,omitempty"`
	Truncated            bool            `json:"truncated,omitempty"`
	Input                string          `json:"input,omitempty"`
	InputSHA256          string          `json:"input_sha256,omitempty"`
	InputTruncated       bool            `json:"input_truncated,omitempty"`
	EvidencePath         string          `json:"evidence_path,omitempty"`
	AttemptTree          string          `json:"attempt_tree,omitempty"`
	AttemptContentSHA256 string          `json:"attempt_content_sha256,omitempty"`
	AttemptStartedAt     string          `json:"attempt_started_at,omitempty"`
	AttemptFinishedAt    string          `json:"attempt_finished_at,omitempty"`
}

type AttemptUsage struct {
	Attempt int             `json:"attempt"`
	Usage   json.RawMessage `json:"usage"`
}

type StepResult struct {
	ReadOnly   bool     `json:"read_only,omitempty"`
	GateReview bool     `json:"gate_review,omitempty"`
	ID         string   `json:"id"`
	DependsOn  []string `json:"depends_on,omitempty"`
	CandidateResult
}

type Report struct {
	Kind                 string            `json:"kind,omitempty"`
	Version              int               `json:"version"`
	ID                   string            `json:"id"`
	Mode                 string            `json:"mode"`
	Goal                 string            `json:"goal"`
	Execution            string            `json:"execution"`
	ProcessContainment   string            `json:"process_containment,omitempty"`
	State                string            `json:"state"`
	Repository           string            `json:"repository"`
	Output               string            `json:"output"`
	StartedAt            string            `json:"started_at"`
	FinishedAt           string            `json:"finished_at,omitempty"`
	Error                string            `json:"error,omitempty"`
	Snapshot             Snapshot          `json:"snapshot"`
	Candidates           []CandidateResult `json:"candidates,omitempty"`
	Steps                []StepResult      `json:"steps,omitempty"`
	Verification         *Verification     `json:"verification,omitempty"`
	Worktree             string            `json:"worktree,omitempty"`
	Patch                string            `json:"patch,omitempty"`
	PatchSHA256          string            `json:"patch_sha256,omitempty"`
	FinalSnapshot        *Snapshot         `json:"final_snapshot,omitempty"`
	DiagnosticsTruncated bool              `json:"diagnostics_truncated,omitempty"`
}

type RunReport = Report

var workflowID = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,63}$`)

func LoadPlan(path string) (Plan, error) {
	var p Plan
	if err := decodeFile(path, &p); err != nil {
		return p, err
	}
	return p, ValidatePlan(p)
}

func LoadReport(directory string) (Report, error) {
	var r Report
	if err := decodeFile(directory+string(os.PathSeparator)+"report.json", &r); err != nil {
		return r, err
	}
	return r, validateReport(directory, r)
}

func validateReport(directory string, r Report) error {
	invalid := func(reason string) error { return fmt.Errorf("invalid workflow report: %s", reason) }
	if r.Version != 1 || !workflowID.MatchString(r.ID) || r.Execution != "local" || (r.Mode != "compare" && r.Mode != "mission") {
		return invalid("version, identity, execution or mode")
	}
	if r.Kind != "" && r.Kind != "work" {
		return invalid("unknown workflow kind")
	}
	if r.ProcessContainment != "" && r.ProcessContainment != ProcessGroups && r.ProcessContainment != PIDNamespace {
		return invalid("unknown process containment policy")
	}
	if r.Goal == "" || r.Repository == "" || r.Output == "" {
		return invalid("missing goal or paths")
	}
	actual, err := filepath.Abs(directory)
	if err != nil {
		return err
	}
	actual, err = filepath.EvalSymlinks(actual)
	if err != nil {
		return err
	}
	if r.Output != actual {
		return invalid("output does not identify the directory being inspected")
	}
	if _, err := time.Parse(time.RFC3339Nano, r.StartedAt); err != nil {
		return invalid("missing or malformed start time")
	}
	switch r.State {
	case "running":
		if r.FinishedAt != "" {
			return invalid("running result has a finish time")
		}
	case "completed", "failed", "interrupted":
		if _, err := time.Parse(time.RFC3339Nano, r.FinishedAt); err != nil {
			return invalid("missing or malformed finish time")
		}
	default:
		return invalid("unknown state")
	}
	if r.State == "completed" && r.Error != "" {
		return invalid("completed result records a failure")
	}
	if (r.State == "failed" || r.State == "interrupted") && r.Error == "" {
		return invalid("failed result has no reason")
	}
	if len(r.Candidates) > 16 || len(r.Steps) > 64 {
		return invalid("too many candidates or steps")
	}
	if r.Mode == "compare" && len(r.Steps) > 0 || r.Mode == "mission" && len(r.Candidates) > 0 {
		return invalid("result collections do not match mode")
	}
	seen := map[string]bool{}
	validCandidate := func(id string, c CandidateResult, verifierOnly bool) error {
		if !workflowID.MatchString(id) || seen[id] {
			return invalid("invalid or duplicate result ID")
		}
		seen[id] = true
		switch c.State {
		case "pending", "running", "not_started", "failed", "uncertain", "completed", "verified", "checks_failed", "interrupted", "stale", "empty_result", "review_invalid", "review_modified", "changes_requested":
		default:
			return invalid("unknown candidate state")
		}
		if c.Worktree == "" {
			return invalid("candidate has no worktree")
		}
		if r.ProcessContainment == PIDNamespace && c.Verification != nil && c.Verification.ProcessContainment != PIDNamespace {
			return invalid("candidate verification does not use the recorded PID containment")
		}
		if c.State == "verified" {
			if !validVerification(c.Verification, c.Worktree) {
				return invalid("verified result lacks passing revision-bound checks")
			}
			if !verifierOnly && (c.FinalSnapshot == nil || c.FinalSnapshot.Tree != c.Verification.Tree || c.FinalSnapshot.ContentSHA256 != c.Verification.ContentSHA256 || c.Patch == "" || !digestID(c.PatchSHA256)) {
				return invalid("verified result lacks its bound patch and snapshot")
			}
		}
		return nil
	}
	for _, c := range r.Candidates {
		if err := validCandidate(c.ID, c, false); err != nil {
			return err
		}
	}
	for _, s := range r.Steps {
		if err := validCandidate(s.ID, s.CandidateResult, s.Harness == ""); err != nil {
			return err
		}
	}
	if r.ProcessContainment == PIDNamespace && r.Verification != nil && r.Verification.ProcessContainment != PIDNamespace {
		return invalid("final verification does not use the recorded PID containment")
	}
	if r.State == "completed" {
		if err := RequireApprovedReview(r); err != nil {
			return invalid(err.Error())
		}
		if !objectID(r.Snapshot.Commit) || !objectID(r.Snapshot.Tree) || !objectID(r.Snapshot.BaseCommit) || !digestID(r.Snapshot.ContentSHA256) || r.Repository != r.Snapshot.Repository {
			return invalid("completed result has no valid starting snapshot")
		}
		if r.Mode == "compare" {
			if len(r.Candidates) == 0 {
				return invalid("completed comparison has no candidates")
			}
			for _, c := range r.Candidates {
				if c.State != "verified" {
					return invalid("completed comparison contains unsuccessful candidates")
				}
			}
		} else {
			if len(r.Steps) == 0 || !validVerification(r.Verification, r.Worktree) || r.FinalSnapshot == nil || r.FinalSnapshot.Tree != r.Verification.Tree || r.FinalSnapshot.ContentSHA256 != r.Verification.ContentSHA256 || r.Patch == "" || !digestID(r.PatchSHA256) {
				return invalid("completed mission lacks its final checked artifact")
			}
		}
	}
	return nil
}

func digestID(s string) bool {
	if len(s) != 64 {
		return false
	}
	for _, v := range s {
		if !(v >= '0' && v <= '9' || v >= 'a' && v <= 'f') {
			return false
		}
	}
	return true
}

func validVerification(v *Verification, repo string) bool {
	if v == nil || !v.Passed || v.Repository != repo || !objectID(v.Tree) || v.Tree != v.EndingTree || !digestID(v.ContentSHA256) || v.ContentSHA256 != v.EndingContentSHA256 || len(v.Checks) == 0 || len(v.Checks) > 32 {
		return false
	}
	if v.ProcessContainment != "" && v.ProcessContainment != ProcessGroups && v.ProcessContainment != PIDNamespace {
		return false
	}
	for _, c := range v.Checks {
		if !c.Passed || c.Name == "" || len(c.Command) == 0 || c.ExitCode != 0 {
			return false
		}
	}
	return true
}

func decodeFile(path string, target any) error {
	return DecodeFile(path, target)
}

// DecodeFile rejects nonregular files, symlinks, oversized content and ambiguous
// duplicate JSON fields before interpreting a workflow configuration or record.
func DecodeFile(path string, target any) error {
	data, err := ReadRegularFile(path, 32<<20)
	if err != nil {
		return err
	}
	return DecodeJSON(data, target)
}

func DecodeJSON(data []byte, target any) error {
	if !utf8.Valid(data) {
		return errors.New("workflow JSON must be valid UTF-8")
	}
	if len(data) > 32<<20 {
		return errors.New("workflow JSON exceeds 32 MiB")
	}
	if err := validateJSONKeys(data); err != nil {
		return err
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err := d.Decode(target); err != nil {
		return err
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		return errors.New("workflow file must contain exactly one JSON object")
	}
	return nil
}

func validateJSONKeys(data []byte) error {
	d := json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	tokens := 0
	var value func(int) error
	value = func(depth int) error {
		if depth > 64 {
			return errors.New("workflow JSON nesting exceeds 64 levels")
		}
		tokens++
		if tokens > 1_000_000 {
			return errors.New("workflow JSON exceeds token limit")
		}
		t, err := d.Token()
		if err != nil {
			return err
		}
		delim, container := t.(json.Delim)
		if !container {
			return nil
		}
		switch delim {
		case '{':
			seen := map[string]bool{}
			for d.More() {
				keyToken, err := d.Token()
				if err != nil {
					return err
				}
				key, ok := keyToken.(string)
				if !ok {
					return errors.New("invalid JSON object key")
				}
				folded := foldedJSONKey(key)
				if seen[folded] {
					return fmt.Errorf("duplicate JSON object key %q", key)
				}
				seen[folded] = true
				if err = value(depth + 1); err != nil {
					return err
				}
			}
			end, err := d.Token()
			if err != nil {
				return err
			}
			if end != json.Delim('}') {
				return errors.New("invalid JSON object")
			}
		case '[':
			for d.More() {
				if err := value(depth + 1); err != nil {
					return err
				}
			}
			end, err := d.Token()
			if err != nil {
				return err
			}
			if end != json.Delim(']') {
				return errors.New("invalid JSON array")
			}
		default:
			return errors.New("unexpected JSON delimiter")
		}
		return nil
	}
	if err := value(0); err != nil {
		return err
	}
	if _, err := d.Token(); err != io.EOF {
		return errors.New("workflow file must contain exactly one JSON object")
	}
	return nil
}

func foldedJSONKey(key string) string {
	var b strings.Builder
	for _, r := range key {
		smallest := r
		for next := unicode.SimpleFold(r); next != r; next = unicode.SimpleFold(next) {
			if next < smallest {
				smallest = next
			}
		}
		b.WriteRune(smallest)
	}
	return b.String()
}

func normalized(p Plan) Plan {
	p.ProcessContainment = normalizedContainment(p.ProcessContainment)
	if p.Mode == "" {
		p.Mode = "compare"
	}
	if p.TimeoutSeconds == 0 {
		p.TimeoutSeconds = 600
	}
	if p.MaxAttempts == 0 {
		p.MaxAttempts = 1
	}
	if p.MaxParallel == 0 {
		p.MaxParallel = 2
	}
	return p
}

func ValidatePlan(p Plan) error {
	p = normalized(p)
	if p.Kind != "" && p.Kind != "work" {
		return errors.New("unknown workflow kind")
	}
	if p.Version != 1 {
		return errors.New("workflow version must be 1")
	}
	if p.Execution != "local" {
		return errors.New("execution must explicitly be local (trusted host execution; worktrees do not isolate processes)")
	}
	if err := ValidateProcessContainment(p.ProcessContainment); err != nil {
		return err
	}
	if p.Mode != "compare" && p.Mode != "mission" {
		return errors.New("mode must be compare or mission")
	}
	if strings.TrimSpace(p.Repository) == "" || strings.ContainsRune(p.Repository, 0) {
		return errors.New("repository is required")
	}
	if strings.TrimSpace(p.Goal) == "" || len(p.Goal) > 128<<10 {
		return errors.New("goal must contain 1–131072 bytes")
	}
	if p.TimeoutSeconds < 1 || p.TimeoutSeconds > 86400 || p.MaxAttempts < 1 || p.MaxAttempts > 5 {
		return errors.New("timeout_seconds must be 1–86400 and max_attempts 1–5")
	}
	if p.MaxParallel < 1 || p.MaxParallel > 4 {
		return errors.New("max_parallel must be 1–4")
	}
	if len(p.Candidates) < 1 || len(p.Candidates) > 16 {
		return errors.New("choose 1–16 candidates")
	}
	ids := map[string]bool{}
	for _, c := range p.Candidates {
		if !workflowID.MatchString(c.ID) || ids[c.ID] {
			return fmt.Errorf("invalid or duplicate candidate ID %q", c.ID)
		}
		ids[c.ID] = true
		switch c.Harness {
		case "command", "claude", "codex", "pi", "antigravity":
		default:
			return fmt.Errorf("unsupported harness %q", c.Harness)
		}
		if c.Harness == "command" && (len(c.Command) == 0 || c.Command[0] == "") {
			return fmt.Errorf("candidate %s requires command argv", c.ID)
		}
		if len(c.Command) > 64 || len(c.Prompt) > 128<<10 || c.TimeoutSeconds < 0 || c.TimeoutSeconds > 600 {
			return fmt.Errorf("candidate %s exceeds prompt/argv/time bounds", c.ID)
		}
		for _, a := range c.Command {
			if strings.ContainsRune(a, 0) || len(a) > 16<<10 {
				return errors.New("invalid command argument")
			}
		}
		if len(c.Env) > 256 {
			return fmt.Errorf("candidate %s has too many environment values", c.ID)
		}
		for key, value := range c.Env {
			if len(key) > 128 || len(value) > 128<<10 {
				return fmt.Errorf("candidate %s environment exceeds bounds", c.ID)
			}
		}
		if len(c.Model) > 1024 || len(c.BaseURL) > 8192 || len(c.APIKeyEnv) > 128 {
			return fmt.Errorf("candidate %s metadata exceeds bounds", c.ID)
		}
		// Build validates argv/environment without writing files or launching a
		// provider. Reject every invalid peer before any candidate can execute.
		spec, err := harness.Build(harness.Request{Harness: c.Harness, Model: c.Model, Command: c.Command, Env: c.Env, BaseURL: c.BaseURL, APIKeyEnv: c.APIKeyEnv, Workdir: p.Repository})
		if err != nil {
			return fmt.Errorf("candidate %s configuration: %w", c.ID, err)
		}
		if wrap := processContainmentWrapper(p.ProcessContainment); wrap != nil {
			if _, err := wrap(spec); err != nil {
				return fmt.Errorf("candidate %s containment configuration: %w", c.ID, err)
			}
		}
	}
	if len(p.Checks) < 1 || len(p.Checks) > 32 {
		return errors.New("choose 1–32 independent checks")
	}
	checkNames := map[string]bool{}
	for _, c := range p.Checks {
		if strings.TrimSpace(c.Name) == "" || len(c.Name) > 128 || checkNames[c.Name] || len(c.Command) == 0 || len(c.Command) > 64 || c.Command[0] == "" || c.TimeoutSeconds < 0 || c.TimeoutSeconds > 600 {
			return errors.New("checks require unique names, command argv and bounded timeouts")
		}
		for _, arg := range c.Command {
			if strings.ContainsRune(arg, 0) || len(arg) > 16<<10 {
				return errors.New("invalid check command argument")
			}
		}
		checkNames[c.Name] = true
	}
	if p.Mode == "mission" {
		steps, err := orderedSteps(p)
		if err == nil && p.Kind == "work" {
			gate := -1
			for i, s := range steps {
				if s.GateReview {
					gate = i
				}
			}
			if gate < 0 {
				return errors.New("guided work requires a final review gate")
			}
			for _, s := range steps[gate+1:] {
				if s.Candidate != "" {
					return errors.New("guided work cannot run agents after its final review")
				}
			}
		}
		return err
	}
	if p.Kind == "work" {
		return errors.New("guided work requires mission mode")
	}
	if len(p.Steps) > 0 {
		return errors.New("steps are only supported in mission mode")
	}
	return nil
}

func orderedSteps(p Plan) ([]Step, error) {
	if len(p.Steps) < 1 || len(p.Steps) > 64 {
		return nil, errors.New("missions require 1–64 steps")
	}
	candidates := map[string]bool{}
	for _, c := range p.Candidates {
		candidates[c.ID] = true
	}
	steps := map[string]Step{}
	for _, s := range p.Steps {
		if !workflowID.MatchString(s.ID) {
			return nil, fmt.Errorf("invalid step ID %q", s.ID)
		}
		if _, ok := steps[s.ID]; ok {
			return nil, errors.New("duplicate step ID")
		}
		if s.Candidate != "" && !candidates[s.Candidate] || s.Candidate == "" && !s.Verify {
			return nil, fmt.Errorf("step %s must reference one candidate or be a verify step", s.ID)
		}
		if s.RequireResult && s.Candidate == "" {
			return nil, fmt.Errorf("step %s cannot require result text from a verifier-only step", s.ID)
		}
		if (s.ReadOnly || s.Review || s.GateReview) && s.Candidate == "" {
			return nil, fmt.Errorf("step %s requires an agent for review controls", s.ID)
		}
		if s.Review && (!s.ReadOnly || !s.RequireResult) {
			return nil, fmt.Errorf("step %s review must be read_only and require_result", s.ID)
		}
		if s.Review && s.Verify {
			return nil, fmt.Errorf("step %s must keep structured review and verification in separate phases", s.ID)
		}
		if s.GateReview && !s.Review {
			return nil, fmt.Errorf("step %s review gate requires a structured review", s.ID)
		}
		if len(s.Goal) > 128<<10 {
			return nil, errors.New("step goal exceeds bounds")
		}
		steps[s.ID] = s
	}
	for _, s := range p.Steps {
		seen := map[string]bool{}
		for _, dep := range s.DependsOn {
			if _, ok := steps[dep]; !ok || dep == s.ID || seen[dep] {
				return nil, fmt.Errorf("invalid dependency for %s", s.ID)
			}
			seen[dep] = true
		}
	}
	result := []Step{}
	done := map[string]bool{}
	for len(result) < len(p.Steps) {
		progress := false
		for _, s := range p.Steps {
			if done[s.ID] {
				continue
			}
			ready := true
			for _, dep := range s.DependsOn {
				ready = ready && done[dep]
			}
			if ready {
				result = append(result, s)
				done[s.ID] = true
				progress = true
			}
		}
		if !progress {
			return nil, errors.New("mission dependencies contain a cycle")
		}
	}
	return result, nil
}
