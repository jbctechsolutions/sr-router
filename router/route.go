package router

import (
	"sort"

	"github.com/jbctechsolutions/sr-router/config"
)

// RoutingDecision is the output of the Router: the selected model and the
// reasoning behind the choice, along with ranked alternatives.
type RoutingDecision struct {
	Model        string
	Score        float64
	Tier         string
	Reasoning    string
	EstCost      float64
	Alternatives []Alternative
}

// Alternative is a model that was considered but not selected.
type Alternative struct {
	Model string
	Score float64
}

// Router selects the best model for a Classification using weighted scoring.
type Router struct {
	cfg *config.Config
}

// NewRouter returns a Router backed by the provided config.
func NewRouter(cfg *config.Config) *Router {
	return &Router{cfg: cfg}
}

// Route picks the best model across ALL configured models using a weighted
// score: cost_weight * cost_score + quality_weight * quality_score.
//
// Models that do not meet the task's MinQuality floor or that lack a required
// strength are excluded before scoring. The tier is derived from the selected
// model's membership rather than being predetermined by the route class.
// If no model qualifies, the configured fallback model is returned.
func (r *Router) Route(class Classification) RoutingDecision {
	type scored struct {
		name  string
		score float64
	}

	// Determine the maximum cost across all models for normalisation.
	maxCost := 0.0
	for _, m := range r.cfg.Models {
		if m.CostPer1kTok > maxCost {
			maxCost = m.CostPer1kTok
		}
	}
	if maxCost == 0 {
		maxCost = 1.0
	}

	var candidates []scored

	for name, m := range r.cfg.Models {
		// Quality floor filter.
		if m.QualityCeiling < class.MinQuality {
			continue
		}

		// Required-strengths filter.
		if !hasStrengths(m.Strengths, class.RequiredStrengths) {
			continue
		}

		// Weighted score: higher quality and lower cost both improve the score.
		qualityScore := m.QualityCeiling
		costScore := 1.0 - (m.CostPer1kTok / maxCost)

		cw := r.cfg.Defaults.CostWeight
		qw := r.cfg.Defaults.QualityWeight
		total := cw*costScore + qw*qualityScore

		candidates = append(candidates, scored{name: name, score: total})
	}

	if len(candidates) == 0 {
		return RoutingDecision{
			Model:     r.cfg.Defaults.FallbackModel,
			Score:     0,
			Tier:      class.Tier,
			Reasoning: "no qualified models, using fallback",
		}
	}

	// Sort descending by score; ties are broken by model name for determinism.
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].score != candidates[j].score {
			return candidates[i].score > candidates[j].score
		}
		return candidates[i].name < candidates[j].name
	})

	best := candidates[0]

	var alts []Alternative
	for _, c := range candidates[1:] {
		alts = append(alts, Alternative{Model: c.name, Score: c.score})
	}

	m := r.cfg.Models[best.name]
	tier := r.findModelTier(best.name)

	return RoutingDecision{
		Model:        best.name,
		Score:        best.score,
		Tier:         tier,
		Reasoning:    class.TaskType + " task → " + best.name + " (cheapest qualified)",
		EstCost:      m.CostPer1kTok,
		Alternatives: alts,
	}
}

// findModelTier returns the tier name that contains the given model.
// If the model is not in any tier, returns the fallback tier "premium".
func (r *Router) findModelTier(modelName string) string {
	for tierName, tier := range r.cfg.Tiers {
		for _, m := range tier.Models {
			if m == modelName {
				return tierName
			}
		}
	}
	return "premium"
}

// hasStrengths reports whether modelStrengths contains every element of
// required.  An empty required slice always returns true.
func hasStrengths(modelStrengths, required []string) bool {
	if len(required) == 0 {
		return true
	}
	set := make(map[string]bool, len(modelStrengths))
	for _, s := range modelStrengths {
		set[s] = true
	}
	for _, r := range required {
		if !set[r] {
			return false
		}
	}
	return true
}
