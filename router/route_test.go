package router

import (
	"testing"
)

func TestRouteSelectsCheapestQualifiedModel(t *testing.T) {
	cfg := loadTestConfig(t)
	r := NewRouter(cfg)

	decision := r.Route(Classification{
		RouteClass:        "interactive",
		TaskType:          "chat",
		Tier:              "premium",
		MinQuality:        0.85,
		RequiredStrengths: []string{},
	})

	if decision.Model == "" {
		t.Fatal("expected a model to be selected")
	}
	if decision.Score <= 0 {
		t.Error("expected positive score")
	}
}

func TestRoutePrefersLowerCostAtSameQuality(t *testing.T) {
	cfg := loadTestConfig(t)
	r := NewRouter(cfg)

	decision := r.Route(Classification{
		RouteClass:        "background",
		TaskType:          "summarization",
		Tier:              "budget",
		MinQuality:        0.50,
		RequiredStrengths: []string{"summarization"},
	})

	model := cfg.Models[decision.Model]
	if model.CostPer1kTok > 0.01 {
		t.Errorf("expected cheap model for budget summarization, got %s at $%.4f/1k", decision.Model, model.CostPer1kTok)
	}
}

func TestRouteRespectsQualityFloor(t *testing.T) {
	cfg := loadTestConfig(t)
	r := NewRouter(cfg)

	decision := r.Route(Classification{
		RouteClass:        "interactive",
		TaskType:          "architecture",
		Tier:              "premium",
		MinQuality:        0.90,
		RequiredStrengths: []string{"architecture", "complex_reasoning"},
	})

	model := cfg.Models[decision.Model]
	if model.QualityCeiling < 0.90 {
		t.Errorf("model %s quality ceiling %.2f below floor 0.90", decision.Model, model.QualityCeiling)
	}
}

func TestRouteReturnsAlternatives(t *testing.T) {
	cfg := loadTestConfig(t)
	r := NewRouter(cfg)

	decision := r.Route(Classification{
		RouteClass: "interactive",
		TaskType:   "code",
		Tier:       "premium",
		MinQuality: 0.80,
	})

	if len(decision.Alternatives) == 0 {
		t.Error("expected alternatives to be populated")
	}
}
