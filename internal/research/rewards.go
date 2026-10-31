package research

import "sort"

type RewardTeam struct {
	ID      string   `json:"id"`
	Members []string `json:"members"`
}
type RewardPool struct {
	Team         string   `json:"team"`
	Members      []string `json:"members"`
	TotalUtility float64  `json:"total_utility"`
	PerMember    float64  `json:"per_member"`
}
type RewardAware interface {
	SetRewardTeams(mode string, teams []RewardTeam)
}
type rewardContext struct {
	mode  string
	teams map[string]map[string]bool
}

func (r *rewardContext) set(mode string, teams []RewardTeam) {
	r.mode = mode
	r.teams = map[string]map[string]bool{}
	for _, t := range teams {
		members := map[string]bool{}
		for _, id := range t.Members {
			members[id] = true
		}
		r.teams[t.ID] = members
	}
}
func (r rewardContext) valid(actor, choice string) bool {
	return choice == "" || (r.mode == "pool" && r.teams[choice][actor])
}
func (g *goodsEpisode) SetRewardTeams(mode string, teams []RewardTeam) { g.rewards.set(mode, teams) }
func applyRewards(out *Outcome, actors []string, cumulative map[string]float64, mode string, context rewardContext, choices map[string]string) {
	out.ScoringUtilities = map[string]float64{}
	for _, id := range actors {
		out.ScoringUtilities[id] = out.Utilities[id]
	}
	groups := map[string][]string{}
	if context.mode == "pool" {
		for actor, team := range choices {
			if team != "" && context.valid(actor, team) {
				groups[team] = append(groups[team], actor)
			}
		}
	}
	keys := []string{}
	for team := range groups {
		keys = append(keys, team)
	}
	sort.Strings(keys)
	for _, team := range keys {
		members := groups[team]
		if len(members) < 2 {
			continue
		}
		sort.Strings(members)
		sum := 0.0
		for _, id := range members {
			sum += out.Utilities[id]
		}
		mean := sum / float64(len(members))
		for _, id := range members {
			out.ScoringUtilities[id] = mean
		}
		out.RewardPools = append(out.RewardPools, RewardPool{team, members, sum, mean})
	}
	maximum := 0.0
	winners := 0
	for _, u := range out.ScoringUtilities {
		if u > maximum {
			maximum = u
		}
	}
	for _, u := range out.ScoringUtilities {
		if u == maximum && maximum > 0 {
			winners++
		}
	}
	for _, id := range actors {
		out.Payoffs[id] = 0
		switch mode {
		case "shared":
			out.Payoffs[id] = out.SocialWelfare / float64(len(actors))
		case "individual":
			out.Payoffs[id] = out.ScoringUtilities[id]
		case "winner":
			if maximum > 0 && out.ScoringUtilities[id] == maximum {
				out.Payoffs[id] = 1 / float64(winners)
			}
		}
		cumulative[id] += out.Payoffs[id]
		out.CumulativePayoffs[id] = cumulative[id]
	}
}
