package research

import (
	"fmt"
	"sort"
)

type Interval struct {
	Lower  float64 `json:"lower"`
	Upper  float64 `json:"upper"`
	Method string  `json:"method"`
}

// NIST two-sided 0.95 t criticals, rounded upward in the final decimal.
// https://www.itl.nist.gov/div898/handbook/eda/section3/eda3672.htm
// Capping df at 30 gives a conservative wider interval for larger samples.
func tCritical95(df int) float64 {
	table := []float64{0, 12.707, 4.304, 3.183, 2.777, 2.572, 2.448, 2.366, 2.307, 2.263, 2.229, 2.202, 2.180, 2.161, 2.146, 2.132, 2.121, 2.111, 2.102, 2.094, 2.087, 2.081, 2.075, 2.070, 2.065, 2.061, 2.057, 2.053, 2.049, 2.046, 2.043}
	if df < 1 {
		return 0
	}
	if df > 30 {
		df = 30
	}
	return table[df]
}

type FactorialEffect struct {
	Factors       []string            `json:"factors"`
	OrderedLevels map[string][]string `json:"ordered_levels"`
	Difference    Estimate            `json:"paired_effect"`
	Definition    string              `json:"definition"`
}

func factorialEffects(report Report) []FactorialEffect {
	p := report.Preregistration
	if p == nil {
		return nil
	}
	names := []string{}
	for name := range p.Factors {
		names = append(names, name)
	}
	sort.Strings(names)
	conditions := map[string]Condition{}
	for _, c := range report.Conditions {
		conditions[c.ID] = c
	}
	blocks := map[string]map[string]float64{}
	for _, r := range report.Results {
		if r.Status != "completed" {
			continue
		}
		key := fmt.Sprintf("%d/%d", r.Trial.Seed, r.Trial.Repetition)
		if blocks[key] == nil {
			blocks[key] = map[string]float64{}
		}
		w, _, _, _, _ := roundMetrics(r.Rounds)
		blocks[key][r.Trial.Condition] = w
	}
	blockKeys := []string{}
	for key := range blocks {
		blockKeys = append(blockKeys, key)
	}
	sort.Strings(blockKeys)
	var effects []FactorialEffect
	for i, first := range names {
		sets := [][]string{{first}}
		for _, second := range names[i+1:] {
			sets = append(sets, []string{first, second})
		}
		for _, set := range sets {
			values := []float64{}
			for _, key := range blockKeys {
				world := blocks[key]
				if len(world) != len(report.Conditions) {
					continue
				}
				sum := 0.0
				for id, welfare := range world {
					c, ok := conditions[id]
					if !ok {
						continue
					}
					weight := 1.0
					for _, name := range set {
						if factor(c, name) == p.Factors[name][0] {
							weight = -weight
						}
					}
					sum += weight * welfare
				}
				denominator := float64(len(report.Conditions)) / float64(uint64(1)<<len(set))
				values = append(values, sum/denominator)
			}
			levels := map[string][]string{}
			for _, name := range set {
				levels[name] = append([]string(nil), p.Factors[name]...)
			}
			definition := "level[1] minus level[0], averaged over other factor levels within paired worlds"
			if len(set) > 1 {
				definition = "paired difference of differences, averaged over remaining factor levels"
			}
			effects = append(effects, FactorialEffect{set, levels, estimate(values), definition})
		}
	}
	return effects
}
