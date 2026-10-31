package research

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"math/rand"
	"sort"
)

const ExchangeVersion = "trust-exchange-v1"

type TrustExchange struct{}

func (TrustExchange) Version() string { return ExchangeVersion }

type InvestmentAction struct {
	To     *string `json:"to"`
	Amount int     `json:"amount"`
}
type ReturnAction struct {
	Returns    map[string]int `json:"returns"`
	RewardTeam string         `json:"reward_team,omitempty"`
}
type Transfer struct {
	From       string `json:"from"`
	To         string `json:"to"`
	Invested   int    `json:"invested"`
	Multiplier int    `json:"multiplier"`
	Escrow     int    `json:"escrow"`
	Returned   int    `json:"returned"`
	Retained   int    `json:"retained"`
	Burned     int    `json:"burned"`
}
type ExchangeOutcome struct {
	Investments     map[string]InvestmentAction `json:"investment_actions"`
	Returns         map[string]ReturnAction     `json:"return_actions"`
	Transfers       []Transfer                  `json:"transfers"`
	TotalInvested   int                         `json:"total_invested"`
	TotalEscrow     int                         `json:"total_escrow"`
	TotalReturned   int                         `json:"total_returned"`
	TotalRetained   int                         `json:"total_retained"`
	TotalBurned     int                         `json:"total_burned"`
	InvestmentValid map[string]bool             `json:"investment_valid"`
	ReturnValid     map[string]bool             `json:"return_valid"`
}
type exchangeCase struct {
	Productivity map[string]int `json:"private_productivity"`
}
type exchangeEpisode struct {
	options          GameOptions
	actors           []string
	round            int
	seed             int64
	cases            []exchangeCase
	credits          map[string]int
	payoffs          map[string]float64
	opening          map[string]int
	investments      map[string]InvestmentAction
	valid            map[string]bool
	reasons          map[string]string
	transfers        []Transfer
	invested         bool
	rewards          rewardContext
	virtualPenalties map[string]int
	chargedPenalties map[string]int
}

func (TrustExchange) New(seed int64, actors []string, options GameOptions) (Episode, error) {
	if options.Kind != ExchangeVersion {
		return nil, errors.New("trust exchange constructor requires its own versioned kind")
	}
	if err := options.validate(len(actors)); err != nil {
		return nil, err
	}
	actors = append([]string(nil), actors...)
	sort.Strings(actors)
	g := &exchangeEpisode{options: options, actors: actors, credits: map[string]int{}, payoffs: map[string]float64{}, seed: seed}
	for i, id := range actors {
		if !identifier.MatchString(id) || (i > 0 && id == actors[i-1]) {
			return nil, errors.New("unique actor ids required")
		}
		g.credits[id] = options.Credits
		g.payoffs[id] = 0
	}
	rng := rand.New(rand.NewSource(seed))
	for range options.Rounds {
		c := exchangeCase{Productivity: map[string]int{}}
		for _, id := range actors {
			c.Productivity[id] = 2 + rng.Intn(options.Multiplier-1)
		}
		g.cases = append(g.cases, c)
	}
	return g, nil
}
func (g *exchangeEpisode) Done() bool { return g.round >= len(g.cases) }
func (g *exchangeEpisode) GroundTruth() json.RawMessage {
	data, _ := json.Marshal(g.cases)
	return data
}
func (g *exchangeEpisode) SetRewardTeams(mode string, teams []RewardTeam) { g.rewards.set(mode, teams) }
func (g *exchangeEpisode) Observe(actor string) (json.RawMessage, error) {
	if g.Done() {
		return nil, errors.New("episode complete")
	}
	credits, ok := g.credits[actor]
	if !ok {
		return nil, errors.New("unknown participant")
	}
	observation := map[string]any{"round": g.round + 1, "rounds": g.options.Rounds, "actor": actor, "actors": g.actors, "remaining_credits": credits, "cumulative_payoff": g.payoffs[actor], "private_productivity": g.cases[g.round].Productivity[actor], "rules": g.options}
	if g.invested {
		received := []Transfer{}
		for _, t := range g.transfers {
			if t.To == actor {
				received = append(received, t)
			}
		}
		observation["received_escrows"] = received
		observation["your_investment"] = g.investments[actor]
	}
	data, err := json.Marshal(observation)
	return data, err
}
func strictObject(raw string, allowed map[string]bool, required []string) (map[string]json.RawMessage, error) {
	d := json.NewDecoder(bytes.NewBufferString(raw))
	token, err := d.Token()
	if err != nil || token != json.Delim('{') {
		return nil, errors.New("return exactly one JSON object")
	}
	fields := map[string]json.RawMessage{}
	for d.More() {
		key, err := d.Token()
		if err != nil {
			return nil, errors.New("invalid object")
		}
		name, ok := key.(string)
		if !ok || !allowed[name] {
			return nil, errors.New("unknown object field")
		}
		if fields[name] != nil {
			return nil, errors.New("duplicate object field")
		}
		var value json.RawMessage
		if d.Decode(&value) != nil {
			return nil, errors.New("invalid field")
		}
		fields[name] = value
	}
	if _, err := d.Token(); err != nil {
		return nil, errors.New("invalid object")
	}
	var more any
	if d.Decode(&more) != io.EOF {
		return nil, errors.New("trailing content")
	}
	for _, key := range required {
		if fields[key] == nil {
			return nil, errors.New("missing required field")
		}
	}
	return fields, nil
}
func parseInvestment(raw string) (InvestmentAction, error) {
	fields, err := strictObject(raw, map[string]bool{"to": true, "amount": true}, []string{"to", "amount"})
	if err != nil {
		return InvestmentAction{}, err
	}
	var a InvestmentAction
	if json.Unmarshal(fields["to"], &a.To) != nil || json.Unmarshal(fields["amount"], &a.Amount) != nil || bytes.Equal(fields["amount"], []byte("null")) || a.Amount < 0 || (a.Amount == 0) != (a.To == nil) {
		return a, errors.New("to must be a recipient, amount a nonnegative integer; zero uses to:null")
	}
	return a, nil
}
func parseReturn(raw string, actors []string) (ReturnAction, error) {
	fields, err := strictObject(raw, map[string]bool{"returns": true, "reward_team": true}, []string{"returns"})
	if err != nil {
		return ReturnAction{}, err
	}
	allowed := map[string]bool{}
	for _, id := range actors {
		allowed[id] = true
	}
	entries, err := strictObject(string(fields["returns"]), allowed, nil)
	if err != nil {
		return ReturnAction{}, err
	}
	a := ReturnAction{Returns: map[string]int{}}
	for id, raw := range entries {
		var amount int
		if json.Unmarshal(raw, &amount) != nil || bytes.Equal(raw, []byte("null")) || amount < 0 {
			return a, errors.New("returns require nonnegative integer amounts")
		}
		a.Returns[id] = amount
	}
	if fields["reward_team"] != nil && (bytes.Equal(fields["reward_team"], []byte("null")) || json.Unmarshal(fields["reward_team"], &a.RewardTeam) != nil) {
		return a, errors.New("reward_team must be a string")
	}
	return a, nil
}
func (g *exchangeEpisode) Invest(inputs map[string]Input) error {
	if g.Done() || g.invested {
		return errors.New("investment stage is unavailable")
	}
	if len(inputs) != len(g.actors) {
		return errors.New("one investment per participant required")
	}
	for _, id := range g.actors {
		if _, exists := inputs[id]; !exists {
			return errors.New("missing participant investment")
		}
	}
	g.opening = map[string]int{}
	g.investments = map[string]InvestmentAction{}
	g.valid = map[string]bool{}
	g.reasons = map[string]string{}
	g.transfers = nil
	g.virtualPenalties = map[string]int{}
	g.chargedPenalties = map[string]int{}
	for _, id := range g.actors {
		input, ok := inputs[id]
		if !ok {
			return errors.New("missing participant investment")
		}
		g.opening[id] = g.credits[id]
		a, err := parseInvestment(input.Raw)
		if input.Failure != "" {
			err = errors.New("investment task failed")
		}
		if err == nil && (a.Amount > g.options.PerRoundCap || a.Amount > g.credits[id]) {
			err = errors.New("investment exceeds remaining wallet or round cap")
		}
		if err == nil && a.To != nil {
			known := false
			for _, active := range g.actors {
				if active == *a.To {
					known = true
				}
			}
			if !known || *a.To == id {
				err = errors.New("recipient must be another active participant")
			}
		}
		g.valid[id] = err == nil
		if err != nil {
			g.reasons[id] = err.Error()
			if g.credits[id] > 0 {
				g.credits[id]--
				g.chargedPenalties[id]++
			} else {
				g.virtualPenalties[id]++
			}
			g.investments[id] = InvestmentAction{}
			continue
		}
		g.investments[id] = a
		g.credits[id] -= a.Amount
		if a.To != nil {
			multiplier := g.cases[g.round].Productivity[*a.To]
			g.transfers = append(g.transfers, Transfer{From: id, To: *a.To, Invested: a.Amount, Multiplier: multiplier, Escrow: a.Amount * multiplier})
		}
	}
	g.invested = true
	return nil
}
func (g *exchangeEpisode) Resolve(inputs map[string]Input, mode string) (Outcome, error) {
	if !g.invested || g.Done() {
		return Outcome{}, errors.New("resolve returns only after investment")
	}
	if mode != "shared" && mode != "individual" && mode != "winner" {
		return Outcome{}, errors.New("unknown reward mode")
	}
	if len(inputs) != len(g.actors) {
		return Outcome{}, errors.New("one return decision per participant required")
	}
	for _, id := range g.actors {
		if _, exists := inputs[id]; !exists {
			return Outcome{}, errors.New("missing participant return")
		}
	}
	out := Outcome{Round: g.round + 1, Environment: ExchangeVersion, Actions: map[string]ActionResult{}, Credits: map[string]int{}, Utilities: map[string]float64{}, Payoffs: map[string]float64{}, CumulativePayoffs: map[string]float64{}, Exchange: &ExchangeOutcome{Investments: g.investments, Returns: map[string]ReturnAction{}, InvestmentValid: g.valid, ReturnValid: map[string]bool{}}}
	choices := map[string]string{}
	for _, id := range g.actors {
		input, ok := inputs[id]
		if !ok {
			return Outcome{}, errors.New("missing participant return")
		}
		a, err := parseReturn(input.Raw, g.actors)
		if input.Failure != "" {
			err = errors.New("return task failed")
		}
		received := map[string]int{}
		for _, t := range g.transfers {
			if t.To == id {
				received[t.From] = t.Escrow
			}
		}
		if err == nil {
			for sender, amount := range a.Returns {
				if amount > received[sender] || (sender == id) {
					err = errors.New("return exceeds sender-specific received escrow")
					break
				}
			}
		}
		if err == nil && !g.rewards.valid(id, a.RewardTeam) {
			err = errors.New("reward_team requires enabled pooling and frozen active membership")
		}
		valid := err == nil
		out.Exchange.ReturnValid[id] = valid
		result := ActionResult{Valid: valid && g.valid[id]}
		if !g.valid[id] {
			result.Reason = g.reasons[id]
		}
		if !valid {
			result.Reason = err.Error()
			if g.credits[id] > 0 {
				g.credits[id]--
				g.chargedPenalties[id]++
			} else {
				g.virtualPenalties[id]++
			}
			out.Exchange.Returns[id] = ReturnAction{Returns: map[string]int{}}
		} else {
			out.Exchange.Returns[id] = a
			choices[id] = a.RewardTeam
		}
		if !result.Valid {
			out.InvalidActions++
		}
		out.Actions[id] = result
	}
	for _, t := range g.transfers {
		if out.Exchange.ReturnValid[t.To] {
			t.Returned = out.Exchange.Returns[t.To].Returns[t.From]
			t.Retained = t.Escrow - t.Returned
			g.credits[t.From] += t.Returned
			g.credits[t.To] += t.Retained
		} else {
			t.Burned = t.Escrow
		}
		out.Exchange.TotalInvested += t.Invested
		out.Exchange.TotalEscrow += t.Escrow
		out.Exchange.TotalReturned += t.Returned
		out.Exchange.TotalRetained += t.Retained
		out.Exchange.TotalBurned += t.Burned
		out.Exchange.Transfers = append(out.Exchange.Transfers, t)
	}
	for _, id := range g.actors {
		utility := float64(g.credits[id] - g.opening[id] - g.virtualPenalties[id])
		out.Utilities[id] = utility
		out.Credits[id] = g.credits[id]
		out.SocialWelfare += utility
		out.TotalCost += g.investments[id].Amount + g.chargedPenalties[id]
		result := out.Actions[id]
		result.Cost = g.investments[id].Amount + g.chargedPenalties[id]
		out.Actions[id] = result
	}
	applyRewards(&out, g.actors, g.payoffs, mode, g.rewards, choices)
	g.round++
	g.invested = false
	return out, nil
}
func scenario(kind string) Scenario {
	if kind == ExchangeVersion {
		return TrustExchange{}
	}
	return PublicGoods{}
}
func exchangeBaseline(seed int64, actors []string, options GameOptions, mode, policy string) ([]Outcome, error) {
	episode, err := (TrustExchange{}).New(seed, actors, options)
	if err != nil {
		return nil, err
	}
	g := episode.(*exchangeEpisode)
	rng := rand.New(rand.NewSource(seed ^ 0x42617365))
	var outcomes []Outcome
	for !g.Done() {
		inputs := map[string]Input{}
		for i, id := range g.actors {
			a := InvestmentAction{}
			if policy != "all-defect" {
				a.Amount = min(options.PerRoundCap, g.credits[id])
				recipient := g.actors[(i+1)%len(g.actors)]
				if policy == "random" {
					a.Amount = rng.Intn(a.Amount + 1)
				} else if policy != "oracle-cooperative" {
					return nil, errors.New("unknown baseline")
				} else {
					for _, candidate := range g.actors {
						if candidate != id && g.cases[g.round].Productivity[candidate] > g.cases[g.round].Productivity[recipient] {
							recipient = candidate
						}
					}
				}
				if a.Amount > 0 {
					a.To = &recipient
				}
			}
			raw, _ := json.Marshal(a)
			inputs[id] = Input{Raw: string(raw)}
		}
		if err := g.Invest(inputs); err != nil {
			return nil, err
		}
		returns := map[string]Input{}
		for _, id := range g.actors {
			a := ReturnAction{Returns: map[string]int{}}
			for _, t := range g.transfers {
				if t.To == id {
					amount := t.Escrow
					if policy == "random" {
						amount = rng.Intn(amount + 1)
					}
					a.Returns[t.From] = amount
				}
			}
			raw, _ := json.Marshal(a)
			returns[id] = Input{Raw: string(raw)}
		}
		out, err := g.Resolve(returns, mode)
		if err != nil {
			return nil, err
		}
		outcomes = append(outcomes, out)
	}
	return outcomes, nil
}
