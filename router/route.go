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

// Route picks the best model in the classification's tier using a weighted
// score: cost_weight * cost_score + quality_weight * quality_score.
//
// Models that do not meet the MinQuality floor or that lack a required strength
// are excluded before scoring. If no model qualifies, the configured fallback
// model is returned with a zero score.
func (r *Router) Route(class Classification) RoutingDecision {
	tierModels := r.cfg.GetTierModels(class.Tier)
	if len(tierModels) == 0 {
		// Tier unknown — consider every model in the catalogue.
		for name := range r.cfg.Models {
			tierModels = append(tierModels, name)
		}
	}

	type scored struct {
		name  string
		score float64
	}

	// Determine the maximum cost among candidate models so we can normalise the
	// cost dimension to [0, 1].  Models with cost 0 receive the best cost score.
	maxCost := 0.0
	for _, name := range tierModels {
		if m, ok := r.cfg.Models[name]; ok {
			if m.CostPer1kTok > maxCost {
				maxCost = m.CostPer1kTok
			}
		}
	}
	if maxCost == 0 {
		maxCost = 1.0 // prevent division by zero; all costs are 0 → equal cost score
	}

	var candidates []scored

	for _, name := range tierModels {
		m, ok := r.cfg.Models[name]
		if !ok {
			continue // model referenced in tier but not defined — skip
		}

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
			Reasoning: "no qualified models in tier, using fallback",
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
	return RoutingDecision{
		Model:        best.name,
		Score:        best.score,
		Tier:         class.Tier,
		Reasoning:    "best score in " + class.Tier + " tier",
		EstCost:      m.CostPer1kTok,
		Alternatives: alts,
	}
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
