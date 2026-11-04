package workflow

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"path/filepath"
	"reflect"
	"strconv"
)

// The digest identifies retained profile metadata, not the provider's resolved
// model, executable bytes or credential values. Runtime environment capabilities
// are deliberately excluded, matching redactedPlan's publication contract.
func candidateProfileDigest(candidate Candidate) (string, error) {
	candidate.Env = nil
	data, err := json.Marshal(candidate)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

func verificationMatchesPlan(v *Verification, checks []Check, incompleteRun bool) bool {
	if v == nil {
		return true
	}
	if len(v.Checks) > len(checks) || len(v.Checks) != len(checks) && (!incompleteRun || v.Passed) {
		return false
	}
	for i, recorded := range v.Checks {
		check := checks[i]
		if recorded.Name != check.Name || !reflect.DeepEqual(recorded.Command, check.Command) {
			return false
		}
	}
	return true
}

func readPolicyRecord(path string, value any) error {
	// Report.Output was canonicalized by validateReport. Refuse redirected
	// intermediate directories as well as DecodeFile's linked/special files.
	parent, err := filepath.EvalSymlinks(filepath.Dir(path))
	if err != nil || parent != filepath.Dir(path) {
		return errors.New("guided evidence record directory is unavailable or redirected")
	}
	if err := DecodeFile(path, value); err != nil {
		return errors.New("guided evidence record is missing, unreadable or malformed")
	}
	return nil
}

func policyAggregateCandidate(candidate CandidateResult) CandidateResult {
	r := compactReport(Report{ReviewPolicy: ReviewPolicyEarlyApproval, Steps: []StepResult{{CandidateResult: candidate}}})
	result := r.Steps[0].CandidateResult
	// A StepResult's outer ID shadows the embedded candidate ID on JSON reload.
	result.ID = ""
	return result
}

// canonicalUsageJSON makes both retention bounds and artifact comparison
// independent of pretty-printing and HTML escaping. UseNumber preserves integer
// precision and number literals; json.Marshal applies one escaping policy.
func canonicalUsageJSON(raw json.RawMessage) (json.RawMessage, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return nil, errors.New("usage must contain exactly one JSON value")
	}
	if value == nil {
		return nil, nil
	}
	return json.Marshal(value)
}

func samePolicyCandidate(a, b CandidateResult) bool {
	// Normalize adapter-authored usage while comparing every other field exactly.
	normalize := func(candidate CandidateResult) (CandidateResult, error) {
		var err error
		candidate.Usage, err = canonicalUsageJSON(candidate.Usage)
		if err != nil {
			return candidate, err
		}
		candidate.UsageRecords = append([]AttemptUsage(nil), candidate.UsageRecords...)
		for i := range candidate.UsageRecords {
			candidate.UsageRecords[i].Usage, err = canonicalUsageJSON(candidate.UsageRecords[i].Usage)
			if err != nil {
				return candidate, err
			}
		}
		return candidate, nil
	}
	left, leftErr := normalize(a)
	right, rightErr := normalize(b)
	return leftErr == nil && rightErr == nil && reflect.DeepEqual(left, right)
}

// These checks bind independently retained owner artifacts. They reject a
// report-only rewrite, but do not attest an owner who rewrites all artifacts.
func validateRetainedPolicyEvidence(r Report, plan Plan) error {
	if err := ValidatePlan(plan); err != nil {
		return errors.New("guided report retained plan is invalid")
	}
	if plan.Kind != r.Kind || plan.Mode != r.Mode || plan.Repository != r.Repository || plan.Goal != r.Goal || plan.Storage != r.Storage || plan.ProcessContainment != r.ProcessContainment || !reflect.DeepEqual(plan.Isolation, r.Isolation) {
		return errors.New("guided report execution policy differs from its retained plan")
	}
	ordered, err := orderedSteps(plan)
	if err != nil || len(r.Steps) > len(ordered) {
		return errors.New("guided report phases differ from its retained plan")
	}
	profiles := map[string]Candidate{}
	for _, candidate := range plan.Candidates {
		profiles[candidate.ID] = candidate
	}
	// Reconstruct the complete aggregate from its independent records before
	// applying compaction. The global size fallback depends on the whole report,
	// so independently compacting each candidate can produce another projection.
	expected := r
	expected.Steps = append([]StepResult(nil), r.Steps...)
	if !verificationMatchesPlan(r.Verification, plan.Checks, r.State != "completed") {
		return errors.New("guided final acceptance commands differ from the retained plan")
	}
	for i, step := range r.Steps {
		planned := ordered[i]
		if !verificationMatchesPlan(step.Verification, plan.Checks, r.State != "completed") {
			return errors.New("guided phase acceptance commands differ from the retained plan")
		}
		if step.State == "skipped" {
			if r.State != "completed" {
				continue
			}
			path := filepath.Join(r.Output, "steps", step.ID, "skipped.json")
			var retained StepResult
			if step.EvidencePath != path {
				return errors.New("guided skipped phase evidence path changed")
			}
			if err := readPolicyRecord(path, &retained); err != nil {
				return err
			}
			if !reflect.DeepEqual(step, retained) {
				return errors.New("guided skipped phase differs from its retained evidence")
			}
			continue
		}
		if planned.Candidate == "" {
			if step.Harness != "" || step.Model != "" || step.CandidateID != "" || step.ProfileSHA256 != "" {
				return errors.New("guided check-only phase invents an agent profile")
			}
			if r.State == "completed" {
				var retained Verification
				path := filepath.Join(r.Output, "steps", step.ID, "verification.json")
				if step.EvidencePath != path {
					return errors.New("guided check evidence path changed")
				}
				if err := readPolicyRecord(path, &retained); err != nil {
					return err
				}
				expected.Steps[i].Verification = &retained
			}
			continue
		}
		// The runner publishes an empty running cursor before candidate setup.
		// Failed/interrupted partial work must remain inspectable at that point.
		if r.State != "completed" && step.State == "running" && step.Harness == "" && step.CandidateID == "" && step.ProfileSHA256 == "" {
			continue
		}
		candidate := profiles[planned.Candidate]
		digest, err := candidateProfileDigest(candidate)
		if err != nil || step.CandidateID != planned.Candidate || step.ProfileSHA256 != digest || step.Harness != candidate.Harness || step.Model != candidate.Model {
			return errors.New("guided phase profile or role differs from its retained plan")
		}
		if r.State != "completed" {
			continue
		}
		path := filepath.Join(r.Output, "steps", step.ID, "attempt-"+strconv.Itoa(step.Attempts)+".json")
		if step.EvidencePath != path {
			return errors.New("guided attempt evidence path changed")
		}
		var retained CandidateResult
		if err := readPolicyRecord(path, &retained); err != nil {
			return err
		}
		if retained.ID != step.ID {
			return errors.New("guided attempt evidence identifies another phase")
		}
		expected.Steps[i].CandidateResult = retained
	}
	if r.State == "completed" {
		var retained Verification
		if err := readPolicyRecord(filepath.Join(r.Output, "verification.json"), &retained); err != nil {
			return err
		}
		expected.Verification = &retained
		projected := compactReport(expected)
		for i, step := range r.Steps {
			actual, candidate := step.CandidateResult, projected.Steps[i].CandidateResult
			actual.ID, candidate.ID = "", ""
			if !samePolicyCandidate(actual, candidate) {
				return errors.New("guided phase execution differs from its retained attempt or verification")
			}
		}
		if !reflect.DeepEqual(r.Verification, projected.Verification) {
			return errors.New("guided result differs from its retained final verification")
		}
	}
	return nil
}
