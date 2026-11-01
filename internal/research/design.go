package research

import (
	"errors"
	"fmt"
	"strings"
)

const defaultOutcome = "total_social_welfare"

var primaryOutcomes = map[string]bool{
	defaultOutcome: true, "productive_project_funding_rate": true,
	"total_credits_spent": true, "invalid_action_count": true,
	"funded_zero_contributors": true, "teams_with_multiple_members": true,
	"failed_task_count": true, "escrow_return_fraction": true,
}

func cloneProfiles(profiles map[string]map[string]string) map[string]map[string]string {
	if profiles == nil {
		return nil
	}
	copy := make(map[string]map[string]string, len(profiles))
	for name, assignments := range profiles {
		copy[name] = make(map[string]string, len(assignments))
		for actor, text := range assignments {
			copy[name][actor] = text
		}
	}
	return copy
}

func validateDesign(m Manifest) error {
	if m.PrimaryOutcome != "" && !primaryOutcomes[m.PrimaryOutcome] {
		return errors.New("unsupported primary_outcome; see research outcome definitions")
	}
	if m.Preregistration != nil && m.PrimaryOutcome != "" && m.PrimaryOutcome != m.Preregistration.PrimaryOutcome {
		return errors.New("primary_outcome conflicts with preregistration.primary_outcome")
	}
	outcome := m.PrimaryOutcome
	if m.Preregistration != nil {
		outcome = m.Preregistration.PrimaryOutcome
	}
	if outcome == "escrow_return_fraction" && m.Game.Kind != ExchangeVersion {
		return errors.New("escrow_return_fraction requires trust-exchange-v1")
	}
	if (outcome == "productive_project_funding_rate" || outcome == "funded_zero_contributors") && m.Game.Kind != GameVersion {
		return errors.New("project funding outcomes require public-goods-v1")
	}
	if m.Counterbalancing != "" && m.Counterbalancing != "latin_square" {
		return errors.New("counterbalancing must be omitted (seeded random order) or latin_square")
	}
	if m.Counterbalancing == "latin_square" && (len(m.Conditions) == 0 || m.Trials*len(m.Seeds)%len(m.Conditions) != 0) {
		return errors.New("latin_square requires a whole number of condition-order cycles: trials * seeds must be divisible by condition count")
	}
	for kind, profiles := range map[string]map[string]map[string]string{"role_assignments": m.RoleAssignments, "personality_profiles": m.PersonalityProfiles} {
		if len(profiles) > 64 {
			return fmt.Errorf("%s exceeds 64 profiles", kind)
		}
		for name, assignments := range profiles {
			if !identifier.MatchString(name) || len(assignments) == 0 || len(assignments) > len(m.Config.Agents) {
				return fmt.Errorf("%s requires named nonempty assignments to initial actors", kind)
			}
			for actor, text := range assignments {
				if _, ok := m.Config.Agents[actor]; !ok || strings.TrimSpace(text) == "" || len(text) > 4096 {
					return fmt.Errorf("%s profile %s requires known initial actors and nonempty text <=4096 bytes", kind, name)
				}
			}
		}
	}
	for _, c := range m.Conditions {
		if c.RoleAssignment != "" && m.RoleAssignments[c.RoleAssignment] == nil {
			return fmt.Errorf("condition %s references unknown role_assignment %q", c.ID, c.RoleAssignment)
		}
		if c.Personality != "" && m.PersonalityProfiles[c.Personality] == nil {
			return fmt.Errorf("condition %s references unknown personality %q", c.ID, c.Personality)
		}
	}
	return nil
}

func selectedOutcome(report Report) string {
	if report.Preregistration != nil {
		return report.Preregistration.PrimaryOutcome
	}
	if report.PrimaryOutcome != "" {
		return report.PrimaryOutcome
	}
	return defaultOutcome
}

// Each metric is reduced once per completed run. Missing exchange denominators
// are unavailable observations, never invented zero return fractions.
func outcomeValue(result Result, name string) (float64, bool) {
	welfare, funding, cost, invalid, freeRiders := roundMetrics(result.Rounds)
	switch name {
	case defaultOutcome:
		return welfare, true
	case "productive_project_funding_rate":
		return funding, true
	case "total_credits_spent":
		return cost, true
	case "invalid_action_count":
		return invalid, true
	case "funded_zero_contributors":
		return freeRiders, true
	case "teams_with_multiple_members":
		formed := map[string]bool{}
		for _, phase := range result.Phases {
			for _, team := range phase.Teams {
				if team.State == "active" && len(team.Members) > 1 {
					formed[team.ID] = true
				}
			}
		}
		return float64(len(formed)), true
	case "failed_task_count":
		failed := 0.0
		for _, phase := range result.Phases {
			for _, task := range phase.Tasks {
				if task.State != "submitted" && task.State != "verified" {
					failed++
				}
			}
		}
		return failed, true
	case "escrow_return_fraction":
		returned, escrow := 0, 0
		for _, round := range result.Rounds {
			if round.Exchange != nil {
				returned += round.Exchange.TotalReturned
				escrow += round.Exchange.TotalEscrow
			}
		}
		if escrow > 0 {
			return float64(returned) / float64(escrow), true
		}
	}
	return 0, false
}
