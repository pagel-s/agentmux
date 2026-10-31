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
	N             int      `json:"n"`
	Mean          float64  `json:"mean"`
	StandardError *float64 `json:"standard_error,omitempty"`
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
}
type Contrast struct {
	From       string   `json:"from"`
	To         string   `json:"to"`
	Difference Estimate `json:"paired_social_welfare_difference_to_minus_from"`
}
type Analysis struct {
	Research        string             `json:"research"`
	ManifestHash    string             `json:"manifest_sha256"`
	Conditions      []ConditionSummary `json:"conditions"`
	PairedContrasts []Contrast         `json:"paired_contrasts"`
	InterruptedRuns int                `json:"interrupted_runs"`
	Interpretation  string             `json:"interpretation"`
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
	}
	return e
}

func Analyze(report Report) Analysis {
	a := Analysis{Research: report.Research, ManifestHash: report.ManifestHash, Interpretation: "Descriptive environment outcomes, not inferred motives or traits. Standard errors assume independent runs; repeated worlds and model/provider dependence can violate this. Paired contrasts align seed/repetition blocks, not execution timing or native sampling."}
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
	return r, nil
}
