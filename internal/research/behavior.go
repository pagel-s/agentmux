package research

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

func writeBehaviors(path string, rows []Behavior) error {
	file, err := os.CreateTemp(filepath.Dir(path), ".behavior-")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	defer file.Close()
	for _, row := range rows {
		if err := json.NewEncoder(file).Encode(row); err != nil {
			return err
		}
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), path)
}

type AgentLabel struct {
	Harness string `json:"harness"`
	Model   string `json:"model"`
	Parent  string `json:"parent,omitempty"`
}
type Behavior struct {
	ID               string  `json:"behavior_id"`
	Trial            string  `json:"trial"`
	Condition        string  `json:"condition"`
	TrialStatus      string  `json:"trial_status"`
	Round            int     `json:"round"`
	Actor            string  `json:"actor"`
	Harness          string  `json:"harness"`
	Model            string  `json:"model"`
	Valid            bool    `json:"valid"`
	ExecutionFailed  bool    `json:"execution_failed"`
	Contribution     int     `json:"contribution"`
	Invested         int     `json:"invested"`
	Returned         int     `json:"returned"`
	Retained         int     `json:"retained"`
	Utility          float64 `json:"utility"`
	Payoff           float64 `json:"payoff"`
	RemainingCredits int     `json:"remaining_credits"`
}

func Behaviors(report Report) []Behavior {
	var rows []Behavior
	for _, result := range report.Results {
		for _, round := range result.Rounds {
			ids := []string{}
			for id := range round.Actions {
				ids = append(ids, id)
			}
			sort.Strings(ids)
			for _, id := range ids {
				action := round.Actions[id]
				label := result.ActorMetadata[id]
				row := Behavior{ID: digest(fmt.Sprintf("%s/%s/%d/%d/%s", report.ManifestHash, result.Trial.ID, result.Attempt, round.Round, id))[:24], Trial: result.Trial.ID, Condition: result.Trial.Condition, TrialStatus: result.Status, Round: round.Round, Actor: id, Harness: label.Harness, Model: label.Model, Valid: action.Valid, Contribution: action.Action.Contribute, Utility: round.Utilities[id], Payoff: round.Payoffs[id], RemainingCredits: round.Credits[id]}
				for _, phase := range result.Phases {
					if phase.Round == round.Round {
						if task, exists := phase.Tasks[id]; exists && task.State != "submitted" && task.State != "verified" {
							row.ExecutionFailed = true
						}
					}
				}
				if round.Exchange != nil {
					row.Invested = round.Exchange.Investments[id].Amount
					for _, transfer := range round.Exchange.Transfers {
						if transfer.To == id {
							row.Returned += transfer.Returned
							row.Retained += transfer.Retained
						}
					}
				}
				rows = append(rows, row)
			}
		}
	}
	return rows
}

type AgentSummary struct {
	Condition         string   `json:"condition"`
	Actor             string   `json:"actor"`
	Harness           string   `json:"harness"`
	Model             string   `json:"model"`
	Decisions         int      `json:"decisions"`
	Invalid           int      `json:"invalid_decisions"`
	ExecutionFailures int      `json:"execution_failures"`
	Utility           Estimate `json:"round_utility"`
	Payoff            Estimate `json:"round_payoff"`
	Contribution      Estimate `json:"contribution"`
	Returned          Estimate `json:"returned"`
}

func agentSummaries(report Report) []AgentSummary {
	groups := map[string][]Behavior{}
	for _, row := range Behaviors(report) {
		if row.TrialStatus == "completed" {
			key := row.Condition + "\x00" + row.Actor
			groups[key] = append(groups[key], row)
		}
	}
	keys := []string{}
	for key := range groups {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var summaries []AgentSummary
	for _, key := range keys {
		rows := groups[key]
		s := AgentSummary{Condition: rows[0].Condition, Actor: rows[0].Actor, Harness: rows[0].Harness, Model: rows[0].Model, Decisions: len(rows)}
		var utilities, payoffs, contributions, returns []float64
		byRun := map[string][]Behavior{}
		for _, row := range rows {
			if !row.Valid {
				s.Invalid++
			}
			if row.ExecutionFailed {
				s.ExecutionFailures++
			}
			byRun[row.Trial] = append(byRun[row.Trial], row)
		}
		runIDs := []string{}
		for id := range byRun {
			runIDs = append(runIDs, id)
		}
		sort.Strings(runIDs)
		for _, id := range runIDs {
			rows := byRun[id]
			u, p, c, r := 0.0, 0.0, 0.0, 0.0
			for _, row := range rows {
				u += row.Utility
				p += row.Payoff
				c += float64(row.Contribution)
				r += float64(row.Returned)
			}
			n := float64(len(rows))
			utilities = append(utilities, u/n)
			payoffs = append(payoffs, p/n)
			contributions = append(contributions, c/n)
			returns = append(returns, r/n)
		}
		s.Utility = estimate(utilities)
		s.Payoff = estimate(payoffs)
		s.Contribution = estimate(contributions)
		s.Returned = estimate(returns)
		summaries = append(summaries, s)
	}
	return summaries
}
