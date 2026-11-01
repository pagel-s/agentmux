package research

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"sort"
)

type PopulationTrajectory struct {
	Trial   string               `json:"trial"`
	Attempt int                  `json:"attempt"`
	Changed bool                 `json:"changed"`
	Rounds  []PopulationSnapshot `json:"rounds"`
}

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
	PrimaryEstimate Estimate            `json:"primary_outcome_estimate"`
}
type Contrast struct {
	From              string   `json:"from"`
	To                string   `json:"to"`
	Difference        Estimate `json:"paired_social_welfare_difference_to_minus_from"`
	PrimaryOutcome    string   `json:"primary_outcome"`
	PrimaryDifference Estimate `json:"paired_primary_outcome_difference_to_minus_from"`
}
type Analysis struct {
	BaselineScope          string                 `json:"baseline_population_scope"`
	PopulationPolicy       Population             `json:"population_policy"`
	PopulationTrajectories []PopulationTrajectory `json:"population_trajectories,omitempty"`

	Research         string             `json:"research"`
	ManifestHash     string             `json:"manifest_sha256"`
	Conditions       []ConditionSummary `json:"conditions"`
	PairedContrasts  []Contrast         `json:"paired_contrasts"`
	IntervenedRuns   int                `json:"intervened_runs"`
	InterruptedRuns  int                `json:"interrupted_runs"`
	Interpretation   string             `json:"interpretation"`
	Preregistration  *Preregistration   `json:"preregistration,omitempty"`
	FactorialEffects []FactorialEffect  `json:"factorial_effects,omitempty"`
	PerAgent         []AgentSummary     `json:"per_agent,omitempty"`
	PowerWarning     string             `json:"power_warning,omitempty"`
	EvidenceWarnings []string           `json:"evidence_warnings,omitempty"`
	PrimaryOutcome   string             `json:"primary_outcome"`
	Counterbalancing string             `json:"counterbalancing,omitempty"`
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
	a.BaselineScope = "initial_fixed_population"
	a.PopulationPolicy = report.Population
	for _, r := range report.Results {
		trajectory := PopulationTrajectory{Trial: r.Trial.ID, Attempt: r.Attempt, Rounds: r.Population}
		for _, snapshot := range r.Population {
			for _, change := range snapshot.Changes {
				if change.Kind == "joined" || change.Kind == "retired" {
					trajectory.Changed = true
				}
			}
		}
		if len(r.Population) > 1 {
			first := append([]string(nil), r.Population[0].Actors...)
			sort.Strings(first)
			for _, p := range r.Population[1:] {
				ids := append([]string(nil), p.Actors...)
				sort.Strings(ids)
				if !reflect.DeepEqual(first, ids) {
					trajectory.Changed = true
				}
			}
		}
		a.PopulationTrajectories = append(a.PopulationTrajectories, trajectory)
		if trajectory.Changed {
			a.EvidenceWarnings = append(a.EvidenceWarnings, "Population changed in "+r.Trial.ID+": baselines use the initial fixed population and are not matched to the observed trajectory.")
		}
	}
	a.PrimaryOutcome = selectedOutcome(report)
	a.Counterbalancing = report.Counterbalancing
	if report.Preregistration == nil {
		a.EvidenceWarnings = append(a.EvidenceWarnings, "Unregistered exploratory evidence: hypotheses, outcome choice and analysis were not frozen by a preregistration; do not interpret observed differences as confirmatory findings.")
	}
	scripted := false
	for _, result := range report.Results {
		for _, actor := range result.ActorMetadata {
			if actor.Harness == "command" {
				scripted = true
			}
		}
	}
	if scripted {
		a.EvidenceWarnings = append(a.EvidenceWarnings, "Includes scripted command participants: these runs validate protocol and programmed strategies; they do not establish model cooperation, competition, personalities or traits.")
	}
	if report.Execution == nil {
		a.EvidenceWarnings = append(a.EvidenceWarnings, "Legacy evidence coverage: no frozen executable/protocol evidence or mandatory authenticated native-trace verification; historical scores are unchanged.")
	}
	if report.Execution != nil && report.Execution.InterventionProtocol == "" {
		a.EvidenceWarnings = append(a.EvidenceWarnings, "Legacy observation coverage: owner interventions were not independently classified and verified.")
	}
	clean := make([]Result, 0, len(report.Results))
	for _, r := range report.Results {
		if r.Status != "completed" {
			a.InterruptedRuns++
		}
		if len(r.Interventions) > 0 || r.OwnerMutations > 0 {
			a.IntervenedRuns++
			continue
		}
		clean = append(clean, r)
	}
	report.Results = clean
	if a.IntervenedRuns > 0 {
		a.EvidenceWarnings = append(a.EvidenceWarnings, "Owner-intervened runs are retained as exploratory evidence and excluded from default estimates and contrasts.")
	}
	groups := map[string][]Result{}
	for _, r := range report.Results {
		if r.Status != "completed" {
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
	pairedPrimary := map[string]map[string]float64{}
	for _, id := range ids {
		s := ConditionSummary{Condition: id, CompletedRuns: len(groups[id]), BaselineWelfare: map[string]Estimate{}}
		var welfare, funding, cost, invalid, freeRiders, teams, failed []float64
		var returnFractions []float64
		baselines := map[string][]float64{}
		paired[id] = map[string]float64{}
		pairedPrimary[id] = map[string]float64{}
		var primaryValues []float64
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
			if value, available := outcomeValue(r, a.PrimaryOutcome); available {
				primaryValues = append(primaryValues, value)
				pairedPrimary[id][fmt.Sprintf("%d/%d", r.Trial.Seed, r.Trial.Repetition)] = value
			}
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
		s.PrimaryEstimate = estimate(primaryValues)
		for name, values := range baselines {
			s.BaselineWelfare[name] = estimate(values)
		}
		a.Conditions = append(a.Conditions, s)
	}
	for i, from := range ids {
		for _, to := range ids[i+1:] {
			var differences []float64
			var primaryDifferences []float64
			keys := make([]string, 0, len(paired[from]))
			for key := range paired[from] {
				keys = append(keys, key)
			}
			sort.Strings(keys)
			for _, key := range keys {
				if value, ok := paired[to][key]; ok {
					differences = append(differences, value-paired[from][key])
				}
				if fromValue, ok := pairedPrimary[from][key]; ok {
					if toValue, ok := pairedPrimary[to][key]; ok {
						primaryDifferences = append(primaryDifferences, toValue-fromValue)
					}
				}
			}
			a.PairedContrasts = append(a.PairedContrasts, Contrast{From: from, To: to, Difference: estimate(differences), PrimaryOutcome: a.PrimaryOutcome, PrimaryDifference: estimate(primaryDifferences)})
		}
	}
	a.Preregistration = report.Preregistration
	a.FactorialEffects = factorialEffects(report)
	a.PerAgent = agentSummaries(report)
	if report.Preregistration != nil {
		minimum := report.Preregistration.MinimumBlocks
		complete := completeBlocks(report)
		if complete < minimum {
			a.PowerWarning = fmt.Sprintf("Underpowered exploratory pilot: %d complete paired blocks; preregistered minimum %d. Intervals are assumption-dependent and cannot establish traits or reliable population effects.", complete, minimum)
		}
	}
	// A minimum-block declaration is not a power calculation. Also disclose
	// tiny exploratory collections, including reports without preregistration.
	blocks := completeBlocks(report)
	if blocks < 8 {
		a.EvidenceWarnings = append(a.EvidenceWarnings, fmt.Sprintf("Small evidence set: %d complete seed/repetition blocks (fewer than 8). This descriptive threshold is not a power calculation; repeated worlds/provider dependence can reduce effective independence further.", blocks))
		if a.PowerWarning == "" {
			a.PowerWarning = fmt.Sprintf("Tiny exploratory evidence: %d complete paired blocks; no reliable population effect or stable trait can be established.", blocks)
		}
	}
	if a.PrimaryOutcome == "escrow_return_fraction" {
		a.EvidenceWarnings = append(a.EvidenceWarnings, "Escrow return fractions exclude runs with zero received escrow. The denominator and paired-block counts may differ between conditions; selection is treatment-dependent and must be interpreted alongside investment/escrow activity.")
	}
	return a
}

func completeBlocks(report Report) int {
	conditions := map[string]bool{}
	for _, c := range report.Conditions {
		conditions[c.ID] = true
	}
	if len(conditions) == 0 {
		for _, r := range report.Results {
			conditions[r.Trial.Condition] = true
		}
	}
	blocks := map[string]map[string]bool{}
	for _, r := range report.Results {
		if r.Status != "completed" {
			continue
		}
		if _, available := outcomeValue(r, selectedOutcome(report)); !available {
			continue
		}
		key := fmt.Sprintf("%d/%d", r.Trial.Seed, r.Trial.Repetition)
		if blocks[key] == nil {
			blocks[key] = map[string]bool{}
		}
		blocks[key][r.Trial.Condition] = true
	}
	complete := 0
	for _, cells := range blocks {
		if len(cells) == len(conditions) && len(conditions) > 0 {
			complete++
		}
	}
	return complete
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
	var stored struct {
		Manifest Manifest `json:"manifest"`
	}
	if err := privateJSON(filepath.Join(dir, "manifest.json"), &stored); err == nil {
		if err := checkStoredManifest(dir, r.ManifestHash); err != nil {
			return r, err
		}
		if r.Population.Mode != "" && r.Population != stored.Manifest.Population {
			return r, errors.New("research population policy differs from its frozen manifest")
		}
		r.Population = stored.Manifest.Population
	} else if !os.IsNotExist(err) {
		return r, err
	}
	return r, nil
}
