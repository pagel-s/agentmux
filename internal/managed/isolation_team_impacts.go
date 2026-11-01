package managed

import "sort"

type IsolationTeamImpact struct {
	ID      string `json:"id"`
	Version int    `json:"version"`
	Agent   string `json:"agent"`
	Action  string `json:"action"`
}

// IsolationTeamImpacts describes the permanent team edits made by independent
// control. A selected owner dissolves the whole team; otherwise selected
// members/invitees are removed. Reconnecting does not undo these edits.
func IsolationTeamImpacts(teams []Team, agents []string) []IsolationTeamImpact {
	chosen := map[string]bool{}
	for _, id := range agents {
		chosen[id] = true
	}
	impacts := []IsolationTeamImpact{}
	for _, team := range teams {
		if team.State != "active" {
			continue
		}
		if chosen[team.Owner] {
			impacts = append(impacts, IsolationTeamImpact{ID: team.ID, Version: team.Version, Agent: team.Owner, Action: "dissolve"})
			continue
		}
		for id := range chosen {
			action := ""
			if contains(team.Members, id) {
				action = "remove_member"
			} else if contains(team.Invited, id) {
				action = "withdraw_invitation"
			}
			if action != "" {
				impacts = append(impacts, IsolationTeamImpact{ID: team.ID, Version: team.Version, Agent: id, Action: action})
			}
		}
	}
	sortIsolationTeamImpacts(impacts)
	return impacts
}

func sortIsolationTeamImpacts(impacts []IsolationTeamImpact) {
	sort.Slice(impacts, func(i, j int) bool {
		if impacts[i].ID != impacts[j].ID {
			return impacts[i].ID < impacts[j].ID
		}
		if impacts[i].Agent != impacts[j].Agent {
			return impacts[i].Agent < impacts[j].Agent
		}
		if impacts[i].Action != impacts[j].Action {
			return impacts[i].Action < impacts[j].Action
		}
		return impacts[i].Version < impacts[j].Version
	})
}

func sameIsolationTeamImpacts(expected, actual []IsolationTeamImpact) bool {
	if len(expected) != len(actual) {
		return false
	}
	copyExpected := append([]IsolationTeamImpact(nil), expected...)
	sortIsolationTeamImpacts(copyExpected)
	for i := range actual {
		if copyExpected[i] != actual[i] {
			return false
		}
	}
	return true
}
