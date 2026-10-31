package research

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
)

type Estimate struct {
	N             int       `json:"n"`
	Mean          float64   `json:"mean"`
	StandardError *float64  `json:"standard_error,omitempty"`
	CI95          *Interval `json:"ci95,omitempty"`
}
type ConditionSummary struct {
	Condition       string              `json:"condition"`
	CompletedRuns   int                 `json:"completed_runs"`
	SocialWelfare   Estimate            `json:"total_social_welfare"`
	FundingRate     Estimate            `json:"productive_project_funding_rate"`
	Cost            Estimate            `json:"total_credits_spent"`
	InvalidActions  Estimate            `json:"invalid_action_count"`
	FreeRiders      Estimate            `json:"funded_zero_contributors"`
	ConsentedTeams  Estimate            `json:"teams_with_multiple_members"`
	TaskFailures    Estimate            `json:"failed_task_count"`
	BaselineWelfare map[string]Estimate `json:"baseline_total_social_welfare"`
	ReturnFraction  Estimate            `json:"escrow_return_fraction"`
}
type Contrast struct {
	From       string   `json:"from"`
	To         string   `json:"to"`
	Difference Estimate `json:"paired_social_welfare_difference_to_minus_from"`
}
type Analysis struct {
	Research         string             `json:"research"`
	ManifestHash     string             `json:"manifest_sha256"`
	Conditions       []ConditionSummary `json:"conditions"`
	PairedContrasts  []Contrast         `json:"paired_contrasts"`
	InterruptedRuns  int                `json:"interrupted_runs"`
	Interpretation   string             `json:"interpretation"`
	Preregistration  *Preregistration   `json:"preregistration,omitempty"`
	FactorialEffects []FactorialEffect  `json:"factorial_effects,omitempty"`
	PerAgent         []AgentSummary     `json:"per_agent,omitempty"`
	PowerWarning     string             `json:"power_warning,omitempty"`
	EvidenceWarnings []string           `json:"evidence_warnings,omitempty"`
}

func estimate(values []float64) Estimate {
	e := Estimate{N: len(values)}
	if len(values) == 0 {
		return e
	}
	for _, v := range values {
		e.Mean += v
	}
	e.Mean /= float64(len(values))
	if len(values) > 1 {
		variance := 0.0
		for _, v := range values {
			variance += (v - e.Mean) * (v - e.Mean)
		}
		se := math.Sqrt(variance / float64(len(values)-1) / float64(len(values)))
		e.StandardError = &se
		critical := tCritical95(len(values) - 1)
		e.CI95 = &Interval{Lower: e.Mean - critical*se, Upper: e.Mean + critical*se, Method: "Student-t run/block mean; df capped at 30; conservative table rounding"}
	}
	return e
}

func Analyze(report Report) Analysis {
	a := Analysis{Research: report.Research, ManifestHash: report.ManifestHash, Interpretation: "Descriptive environment outcomes, not inferred motives or traits. Standard errors assume independent runs; repeated worlds and model/provider dependence can violate this. Paired contrasts align seed/repetition blocks, not execution timing or native sampling."}
	if err := validateReportEntries(report); err != nil {
		a.Interpretation = "Invalid research evidence; estimates withheld."
		a.EvidenceWarnings = []string{err.Error()}
		return a
	}
	if report.Execution == nil {
		a.EvidenceWarnings = append(a.EvidenceWarnings, "Legacy evidence coverage: no frozen executable/protocol evidence or mandatory authenticated native-trace verification; historical scores are unchanged.")
	}
	groups := map[string][]Result{}
	for _, r := range report.Results {
		if r.Status != "completed" {
			a.InterruptedRuns++
			continue
		}
		groups[r.Trial.Condition] = append(groups[r.Trial.Condition], r)
	}
	ids := make([]string, 0, len(groups))
	for id := range groups {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	paired := map[string]map[string]float64{}
	for _, id := range ids {
		s := ConditionSummary{Condition: id, CompletedRuns: len(groups[id]), BaselineWelfare: map[string]Estimate{}}
		var welfare, funding, cost, invalid, freeRiders, teams, failed []float64
		var returnFractions []float64
		baselines := map[string][]float64{}
		paired[id] = map[string]float64{}
		for _, r := range groups[id] {
			w, f, c, i, z := roundMetrics(r.Rounds)
			welfare = append(welfare, w)
			funding = append(funding, f)
			cost = append(cost, c)
			invalid = append(invalid, i)
			freeRiders = append(freeRiders, z)
			formed := map[string]bool{}
			failures := 0.0
			for _, p := range r.Phases {
				for _, t := range p.Teams {
					if t.State == "active" && len(t.Members) > 1 {
						formed[t.ID] = true
					}
				}
				for _, t := range p.Tasks {
					if t.State != "submitted" && t.State != "verified" {
						failures++
					}
				}
			}
			teams = append(teams, float64(len(formed)))
			failed = append(failed, failures)
			returned, escrow := 0, 0
			for _, round := range r.Rounds {
				if round.Exchange != nil {
					returned += round.Exchange.TotalReturned
					escrow += round.Exchange.TotalEscrow
				}
			}
			if escrow > 0 {
				returnFractions = append(returnFractions, float64(returned)/float64(escrow))
			}
			paired[id][fmt.Sprintf("%d/%d", r.Trial.Seed, r.Trial.Repetition)] = w
			for name, rounds := range r.Baselines {
				w, _, _, _, _ := roundMetrics(rounds)
				baselines[name] = append(baselines[name], w)
			}
		}
		s.SocialWelfare = estimate(welfare)
		s.FundingRate = estimate(funding)
		s.Cost = estimate(cost)
		s.InvalidActions = estimate(invalid)
		s.FreeRiders = estimate(freeRiders)
		s.ConsentedTeams = estimate(teams)
		s.TaskFailures = estimate(failed)
		s.ReturnFraction = estimate(returnFractions)
		for name, values := range baselines {
			s.BaselineWelfare[name] = estimate(values)
		}
		a.Conditions = append(a.Conditions, s)
	}
	for i, from := range ids {
		for _, to := range ids[i+1:] {
			var differences []float64
			keys := make([]string, 0, len(paired[from]))
			for key := range paired[from] {
				keys = append(keys, key)
			}
			sort.Strings(keys)
			for _, key := range keys {
				if value, ok := paired[to][key]; ok {
					differences = append(differences, value-paired[from][key])
				}
			}
			a.PairedContrasts = append(a.PairedContrasts, Contrast{From: from, To: to, Difference: estimate(differences)})
		}
	}
	a.Preregistration = report.Preregistration
	a.FactorialEffects = factorialEffects(report)
	a.PerAgent = agentSummaries(report)
	if report.Preregistration != nil {
		minimum := report.Preregistration.MinimumBlocks
		blocks := map[string]map[string]bool{}
		for _, r := range report.Results {
			if r.Status == "completed" {
				key := fmt.Sprintf("%d/%d", r.Trial.Seed, r.Trial.Repetition)
				if blocks[key] == nil {
					blocks[key] = map[string]bool{}
				}
				blocks[key][r.Trial.Condition] = true
			}
		}
		complete := 0
		for _, cells := range blocks {
			if len(cells) == len(report.Conditions) {
				complete++
			}
		}
		if complete < minimum {
			a.PowerWarning = fmt.Sprintf("Underpowered exploratory pilot: %d complete paired blocks; preregistered minimum %d. Intervals are assumption-dependent and cannot establish traits or reliable population effects.", complete, minimum)
		}
	}
	return a
}
func roundMetrics(rounds []Outcome) (welfare, funding, cost, invalid, freeRiders float64) {
	for _, r := range rounds {
		welfare += r.SocialWelfare
		if r.Funded {
			funding++
		}
		cost += float64(r.TotalCost)
		invalid += float64(r.InvalidActions)
		freeRiders += float64(r.FreeRiders)
	}
	if len(rounds) > 0 {
		funding /= float64(len(rounds))
	}
	return
}
func ReadReport(dir string) (Report, error) {
	data, err := os.ReadFile(filepath.Join(dir, "results.json"))
	if err != nil {
		return Report{}, err
	}
	var r Report
	if json.Unmarshal(data, &r) != nil || r.Research == "" || r.ManifestHash == "" {
		return r, errors.New("invalid research result file")
	}
	if err := validateReportEntries(r); err != nil {
		return r, err
	}
	if err := verifyReportEvidence(dir, r); err != nil {
		return r, err
	}
	return r, nil
}
