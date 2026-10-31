package research

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"agentmux/internal/managed"
	"agentmux/internal/managedconfig"
)

type PopulationChange struct {
	Actor              string `json:"actor"`
	Parent             string `json:"parent,omitempty"`
	Kind               string `json:"kind"`
	CreditsTransferred int    `json:"credits_transferred"`
	Reason             string `json:"reason,omitempty"`
}
type PopulationSnapshot struct {
	Round   int                `json:"round"`
	Actors  []string           `json:"actors"`
	Changes []PopulationChange `json:"changes,omitempty"`
}
type populationMember struct {
	id, parent string
	depth      int
}
type populationAware interface {
	setPopulation([]populationMember, int) ([]PopulationChange, error)
}

func populationWallets(current []string, wallet map[string]int, payoffs map[string]float64, members []populationMember, entry int) ([]string, []PopulationChange, error) {
	if len(members) < 2 || len(members) > 16 {
		return nil, nil, errors.New("active research population must remain between 2 and 16")
	}
	sort.Slice(members, func(i, j int) bool {
		if members[i].depth != members[j].depth {
			return members[i].depth < members[j].depth
		}
		return members[i].id < members[j].id
	})
	active := map[string]bool{}
	var next []string
	var changes []PopulationChange
	for _, m := range members {
		if active[m.id] {
			return nil, nil, errors.New("duplicate population actor")
		}
		active[m.id] = true
		if _, known := wallet[m.id]; !known {
			if wallet[m.parent] < entry {
				changes = append(changes, PopulationChange{Actor: m.id, Parent: m.parent, Kind: "deferred", Reason: "parent lacks entry credits"})
				continue
			}
			wallet[m.parent] -= entry
			wallet[m.id] = entry
			payoffs[m.id] = 0
			changes = append(changes, PopulationChange{Actor: m.id, Parent: m.parent, Kind: "joined", CreditsTransferred: entry})
		}
		next = append(next, m.id)
	}
	for _, id := range current {
		if !active[id] {
			changes = append(changes, PopulationChange{Actor: id, Kind: "retired", Reason: "broker actor revoked; remaining credits stay frozen, not reminted"})
		}
	}
	if len(next) < 2 {
		return nil, nil, errors.New("too few funded participants remain")
	}
	sort.Strings(next)
	return next, changes, nil
}
func actorSeed(seed int64, round int, id string) uint64 {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%d/%d/%s", seed, round, id)))
	return binary.LittleEndian.Uint64(sum[:8])
}
func (g *goodsEpisode) setPopulation(members []populationMember, entry int) ([]PopulationChange, error) {
	next, changes, err := populationWallets(g.actors, g.credits, g.payoffs, members, entry)
	if err != nil {
		return nil, err
	}
	g.actors = next
	for round := g.round; round < len(g.cases); round++ {
		g.cases[round].Productive = 0
		for _, id := range next {
			bit, exists := g.cases[round].Bits[id]
			if !exists {
				bit = int(actorSeed(g.seed, round, id) % 2)
				g.cases[round].Bits[id] = bit
			}
			g.cases[round].Productive ^= bit
		}
	}
	return changes, nil
}
func (g *exchangeEpisode) setPopulation(members []populationMember, entry int) ([]PopulationChange, error) {
	if g.invested {
		return nil, errors.New("population can change only outside an active exchange round")
	}
	next, changes, err := populationWallets(g.actors, g.credits, g.payoffs, members, entry)
	if err != nil {
		return nil, err
	}
	g.actors = next
	for round := g.round; round < len(g.cases); round++ {
		for _, id := range next {
			if _, exists := g.cases[round].Productivity[id]; !exists {
				g.cases[round].Productivity[id] = 2 + int(actorSeed(g.seed, round, id)%uint64(g.options.Multiplier-1))
			}
		}
	}
	return changes, nil
}

func syncPopulation(ctx context.Context, call rpc, cfg managedconfig.Config, episode Episode, policy Population) (managedconfig.Config, []PopulationChange, error) {
	var infos []managed.AgentInfo
	cursor := ""
	for {
		raw, err := call(ctx, "agent.list", map[string]any{"after": cursor, "limit": 200})
		if err != nil {
			return cfg, nil, err
		}
		var page struct {
			Agents []managed.AgentInfo `json:"agents"`
			Cursor string              `json:"cursor"`
		}
		if json.Unmarshal(raw, &page) != nil {
			return cfg, nil, errors.New("invalid population registry snapshot")
		}
		infos = append(infos, page.Agents...)
		if len(page.Agents) == 0 || page.Cursor == "" || page.Cursor == cursor {
			break
		}
		cursor = page.Cursor
	}
	var members []populationMember
	byID := map[string]managed.AgentInfo{}
	for _, a := range infos {
		byID[a.ID] = a
		if a.State == "active" {
			members = append(members, populationMember{a.ID, a.Parent, a.Depth})
		}
	}
	if len(members) > policy.MaxAgents {
		return cfg, nil, errors.New("broker population exceeds declared research cap")
	}
	aware, ok := episode.(populationAware)
	if !ok {
		return cfg, nil, errors.New("environment does not support population changes")
	}
	changes, err := aware.setPopulation(members, policy.EntryCredits)
	if err != nil {
		return cfg, nil, err
	}
	accepted := map[string]bool{}
	switch g := episode.(type) {
	case *goodsEpisode:
		for _, id := range g.actors {
			accepted[id] = true
		}
	case *exchangeEpisode:
		for _, id := range g.actors {
			accepted[id] = true
		}
	}
	next := cfg.Normalized()
	next.Agents = map[string]managedconfig.Agent{}
	for id := range accepted {
		if old, exists := cfg.Agents[id]; exists {
			next.Agents[id] = old
		} else {
			next.Agents[id] = byID[id].Config
		}
	}
	return next, changes, nil
}

func ensureNoExtraTasks(ctx context.Context, call rpc) error {
	raw, err := call(ctx, "task.list", map[string]any{"state": "proposed", "limit": 1})
	if err != nil {
		return err
	}
	var page struct {
		Tasks []managed.Task `json:"tasks"`
	}
	if json.Unmarshal(raw, &page) != nil {
		return errors.New("invalid pending-task snapshot")
	}
	if len(page.Tasks) > 0 {
		return errors.New("participant queued out-of-protocol task; research phase contract interrupted")
	}
	return nil
}
