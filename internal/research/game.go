// Package research runs controlled, finite environments on authenticated managed
// agents. Observed decisions and objective outcomes are not inferred motives.
package research

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"sort"
)

const GameVersion = "public-goods-v1"

// Scenario is the extension point for owner-controlled environment mechanics.
// An Episode owns its hidden state; participant tasks receive only Observe.
type Scenario interface {
	Version() string
	New(seed int64, actors []string, options GameOptions) (Episode, error)
}

type Episode interface {
	Observe(actor string) (json.RawMessage, error)
	Resolve(inputs map[string]Input, rewardMode string) (Outcome, error)
	GroundTruth() json.RawMessage
	Done() bool
}

type GameOptions struct {
	Kind        string `yaml:"kind" json:"kind"`
	Rounds      int    `yaml:"rounds" json:"rounds"`
	Credits     int    `yaml:"credits" json:"credits"`
	PerRoundCap int    `yaml:"per_round_cap" json:"per_round_cap"`
	Threshold   int    `yaml:"threshold" json:"threshold"`
	Dividend    int    `yaml:"dividend" json:"dividend"`
}

type Input struct {
	Raw     string `json:"raw"`
	Failure string `json:"failure,omitempty"`
}

type Action struct {
	Project    *int `json:"project"`
	Contribute int  `json:"contribute"`
}

type ActionResult struct {
	Action Action `json:"action"`
	Valid  bool   `json:"valid"`
	Reason string `json:"reason,omitempty"`
	Cost   int    `json:"cost"`
}

type Outcome struct {
	Round             int                     `json:"round"`
	ProductiveProject int                     `json:"productive_project"`
	Funding           [2]int                  `json:"funding"`
	Funded            bool                    `json:"funded"`
	Actions           map[string]ActionResult `json:"actions"`
	Credits           map[string]int          `json:"remaining_credits"`
	Utilities         map[string]float64      `json:"utilities"`
	Payoffs           map[string]float64      `json:"payoffs"`
	CumulativePayoffs map[string]float64      `json:"cumulative_payoffs"`
	SocialWelfare     float64                 `json:"social_welfare"`
	TotalCost         int                     `json:"total_cost"`
	InvalidActions    int                     `json:"invalid_actions"`
	FreeRiders        int                     `json:"free_riders"`
}

type PublicGoods struct{}

func (PublicGoods) Version() string { return GameVersion }

type goodsCase struct {
	Bits       map[string]int `json:"private_bits"`
	Productive int            `json:"productive_project"`
}
type goodsEpisode struct {
	options GameOptions
	actors  []string
	cases   []goodsCase
	round   int
	credits map[string]int
	payoffs map[string]float64
}

func (PublicGoods) New(seed int64, actors []string, options GameOptions) (Episode, error) {
	if err := options.validate(len(actors)); err != nil {
		return nil, err
	}
	actors = append([]string(nil), actors...)
	sort.Strings(actors)
	g := &goodsEpisode{options: options, actors: actors, credits: map[string]int{}, payoffs: map[string]float64{}}
	for i, id := range actors {
		if !identifier.MatchString(id) || (i > 0 && id == actors[i-1]) {
			return nil, errors.New("unique conservative actor ids required")
		}
		g.credits[id] = options.Credits
		g.payoffs[id] = 0
	}
	rng := rand.New(rand.NewSource(seed))
	for range options.Rounds {
		c := goodsCase{Bits: map[string]int{}}
		for _, id := range actors {
			c.Bits[id] = rng.Intn(2)
			c.Productive ^= c.Bits[id]
		}
		g.cases = append(g.cases, c)
	}
	return g, nil
}

func (o GameOptions) validate(population int) error {
	if o.Kind != GameVersion {
		return fmt.Errorf("game.kind must be %s", GameVersion)
	}
	if population < 2 || population > 16 || o.Rounds < 1 || o.Rounds > 100 {
		return errors.New("game requires 2–16 participants and 1–100 rounds")
	}
	if o.Credits < 1 || o.Credits > 10000 || o.PerRoundCap < 1 || o.PerRoundCap > o.Credits || o.Threshold < 1 || o.Threshold > o.PerRoundCap*population || o.Dividend < 1 || o.Dividend > 10000 {
		return errors.New("game credits, per-round cap, threshold, and dividend must be bounded and feasible")
	}
	return nil
}

func (g *goodsEpisode) Done() bool                   { return g.round >= len(g.cases) }
func (g *goodsEpisode) GroundTruth() json.RawMessage { data, _ := json.Marshal(g.cases); return data }
func (g *goodsEpisode) Observe(actor string) (json.RawMessage, error) {
	if g.Done() {
		return nil, errors.New("episode is complete")
	}
	credits, exists := g.credits[actor]
	if !exists {
		return nil, errors.New("unknown participant")
	}
	data, err := json.Marshal(map[string]any{"round": g.round + 1, "rounds": g.options.Rounds,
		"actor": actor, "actors": append([]string(nil), g.actors...), "private_bit": g.cases[g.round].Bits[actor],
		"remaining_credits": credits, "cumulative_payoff": g.payoffs[actor], "rules": g.options})
	return data, err
}

// parseAction accepts exactly one strict object with both fields. Duplicate
// keys, extra prose/fences, unknown keys, decimals, and extra documents fail.
func parseAction(raw string) (Action, error) {
	d := json.NewDecoder(bytes.NewBufferString(raw))
	token, err := d.Token()
	if err != nil || token != json.Delim('{') {
		return Action{}, errors.New("return one JSON action object")
	}
	fields := map[string]json.RawMessage{}
	for d.More() {
		key, err := d.Token()
		if err != nil {
			return Action{}, errors.New("invalid action JSON")
		}
		name, ok := key.(string)
		if !ok || (name != "project" && name != "contribute") {
			return Action{}, errors.New("unknown action field")
		}
		if _, exists := fields[name]; exists {
			return Action{}, errors.New("duplicate action field")
		}
		var value json.RawMessage
		if d.Decode(&value) != nil {
			return Action{}, errors.New("invalid action value")
		}
		fields[name] = value
	}
	if _, err := d.Token(); err != nil || len(fields) != 2 {
		return Action{}, errors.New("action requires project and contribute")
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return Action{}, errors.New("action has trailing content")
	}
	var action Action
	if json.Unmarshal(fields["project"], &action.Project) != nil || json.Unmarshal(fields["contribute"], &action.Contribute) != nil || bytes.Equal(fields["contribute"], []byte("null")) {
		return Action{}, errors.New("action fields must be integers (project may be null)")
	}
	if action.Contribute < 0 || (action.Project != nil && (*action.Project < 0 || *action.Project > 1)) {
		return Action{}, errors.New("project must be 0, 1, or null; contribution cannot be negative")
	}
	if (action.Contribute == 0) != (action.Project == nil) {
		return Action{}, errors.New("use project:null exactly when contributing zero")
	}
	return action, nil
}

func (g *goodsEpisode) Resolve(inputs map[string]Input, mode string) (Outcome, error) {
	if g.Done() {
		return Outcome{}, errors.New("episode is complete")
	}
	if mode != "shared" && mode != "individual" && mode != "winner" {
		return Outcome{}, errors.New("unknown reward mode")
	}
	if len(inputs) != len(g.actors) {
		return Outcome{}, errors.New("provide exactly one input per participant")
	}
	for _, actor := range g.actors {
		if _, ok := inputs[actor]; !ok {
			return Outcome{}, errors.New("missing participant input")
		}
	}
	out := Outcome{Round: g.round + 1, ProductiveProject: g.cases[g.round].Productive, Actions: map[string]ActionResult{}, Credits: map[string]int{}, Utilities: map[string]float64{}, Payoffs: map[string]float64{}, CumulativePayoffs: map[string]float64{}}
	for _, actor := range g.actors {
		input := inputs[actor]
		action, err := parseAction(input.Raw)
		if input.Failure != "" {
			err = errors.New("task did not submit an action")
		}
		if err == nil && (action.Contribute > g.options.PerRoundCap || action.Contribute > g.credits[actor]) {
			err = errors.New("contribution exceeds remaining credits or per-round cap")
		}
		result := ActionResult{Action: action, Valid: err == nil}
		if err != nil {
			result.Action = Action{}
			result.Reason = err.Error()
			out.InvalidActions++
			if g.credits[actor] > 0 {
				result.Cost = 1
			}
		} else {
			result.Cost = action.Contribute
			if action.Project != nil {
				out.Funding[*action.Project] += action.Contribute
			}
		}
		g.credits[actor] -= result.Cost
		out.Actions[actor] = result
		out.TotalCost += result.Cost
		out.Credits[actor] = g.credits[actor]
	}
	out.Funded = out.Funding[out.ProductiveProject] >= g.options.Threshold
	for _, actor := range g.actors {
		result := out.Actions[actor]
		utility := -float64(result.Cost)
		if !result.Valid {
			utility = -1
		} else if out.Funded {
			utility += float64(g.options.Dividend)
			if result.Cost == 0 {
				out.FreeRiders++
			}
		}
		out.Utilities[actor] = utility
		out.SocialWelfare += utility
	}
	maxUtility := 0.0
	winners := 0
	for _, value := range out.Utilities {
		if value > maxUtility {
			maxUtility = value
		}
	}
	for _, value := range out.Utilities {
		if value == maxUtility && maxUtility > 0 {
			winners++
		}
	}
	for _, actor := range g.actors {
		out.Payoffs[actor] = 0
		switch mode {
		case "shared":
			out.Payoffs[actor] = out.SocialWelfare / float64(len(g.actors))
		case "individual":
			out.Payoffs[actor] = out.Utilities[actor]
		case "winner":
			if out.Utilities[actor] == maxUtility && maxUtility > 0 {
				out.Payoffs[actor] = 1 / float64(winners)
			}
		}
		g.payoffs[actor] += out.Payoffs[actor]
		out.CumulativePayoffs[actor] = g.payoffs[actor]
	}
	g.round++
	return out, nil
}

// Baseline runs declared scripted policies against the same hidden cases and
// resource accounting. oracle-cooperative is an informed feasible comparator,
// not a proof of global optimality over the finite horizon.
func Baseline(seed int64, actors []string, options GameOptions, mode, policy string) ([]Outcome, error) {
	episode, err := (PublicGoods{}).New(seed, actors, options)
	if err != nil {
		return nil, err
	}
	g := episode.(*goodsEpisode)
	rng := rand.New(rand.NewSource(seed ^ 0x42617365))
	var rounds []Outcome
	for !g.Done() {
		inputs := map[string]Input{}
		remaining := options.Threshold
		capacity := 0
		for _, id := range g.actors {
			capacity += min(options.PerRoundCap, g.credits[id])
		}
		for _, id := range g.actors {
			action := Action{}
			switch policy {
			case "all-defect":
			case "random":
				action.Contribute = rng.Intn(min(options.PerRoundCap, g.credits[id]) + 1)
				if action.Contribute > 0 {
					project := rng.Intn(2)
					action.Project = &project
				}
			case "oracle-cooperative":
				if capacity >= options.Threshold && len(g.actors)*options.Dividend > options.Threshold {
					action.Contribute = min(remaining, min(options.PerRoundCap, g.credits[id]))
					remaining -= action.Contribute
					if action.Contribute > 0 {
						project := g.cases[g.round].Productive
						action.Project = &project
					}
				}
			default:
				return nil, errors.New("unknown baseline")
			}
			raw, _ := json.Marshal(action)
			inputs[id] = Input{Raw: string(raw)}
		}
		out, err := g.Resolve(inputs, mode)
		if err != nil {
			return nil, err
		}
		rounds = append(rounds, out)
	}
	return rounds, nil
}
