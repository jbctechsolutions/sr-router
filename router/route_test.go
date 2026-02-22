package router

import (
	"testing"
)

func TestRouteSummarizationPicksCheapModel(t *testing.T) {
	cfg := loadTestConfig(t)
	r := NewRouter(cfg)

	decision := r.Route(Classification{
		RouteClass:        "interactive",
		TaskType:          "summarization",
		MinQuality:        0.50,
		RequiredStrengths: []string{"summarization"},
	})

	model := cfg.Models[decision.Model]
	if model.CostPer1kTok > 0.01 {
		t.Errorf("expected cheap model for summarization, got %s at $%.4f/1k", decision.Model, model.CostPer1kTok)
	}
}

func TestRouteCodePicksQualityModel(t *testing.T) {
	cfg := loadTestConfig(t)
	r := NewRouter(cfg)

	decision := r.Route(Classification{
		RouteClass:        "interactive",
		TaskType:          "code",
		MinQuality:        0.80,
		RequiredStrengths: []string{"code"},
	})

	model := cfg.Models[decision.Model]
	if model.QualityCeiling < 0.80 {
		t.Errorf("model %s quality ceiling %.2f below floor 0.80", decision.Model, model.QualityCeiling)
	}
}

func TestRouteArchitecturePicksOpus(t *testing.T) {
	cfg := loadTestConfig(t)
	r := NewRouter(cfg)

	decision := r.Route(Classification{
		RouteClass:        "interactive",
		TaskType:          "architecture",
		MinQuality:        0.90,
		RequiredStrengths: []string{"architecture", "complex_reasoning"},
	})

	if decision.Model != "claude-opus" {
		t.Errorf("expected claude-opus for architecture, got %s", decision.Model)
	}
}

func TestRouteReturnsAlternatives(t *testing.T) {
	cfg := loadTestConfig(t)
	r := NewRouter(cfg)

	decision := r.Route(Classification{
		RouteClass:        "interactive",
		TaskType:          "chat",
		MinQuality:        0.50,
		RequiredStrengths: []string{},
	})

	if len(decision.Alternatives) == 0 {
		t.Error("expected alternatives to be populated")
	}
}

func TestRouteDeriverTierFromModel(t *testing.T) {
	cfg := loadTestConfig(t)
	r := NewRouter(cfg)

	// Summarization should pick a cheap model in budget/speed/free tier.
	decision := r.Route(Classification{
		TaskType:          "summarization",
		MinQuality:        0.50,
		RequiredStrengths: []string{"summarization"},
	})

	if decision.Tier == "premium" {
		t.Errorf("expected non-premium tier for summarization, got %s (model=%s)", decision.Tier, decision.Model)
	}
}

func TestRouteFallbackWhenNoModelQualifies(t *testing.T) {
	cfg := loadTestConfig(t)
	r := NewRouter(cfg)

	decision := r.Route(Classification{
		TaskType:          "impossible",
		MinQuality:        0.99,
		RequiredStrengths: []string{"teleportation"},
	})

	if decision.Model != cfg.Defaults.FallbackModel {
		t.Errorf("expected fallback model %s, got %s", cfg.Defaults.FallbackModel, decision.Model)
	}
}
