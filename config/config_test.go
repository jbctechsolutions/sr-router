package config

import (
	"testing"
)

func TestLoadConfig(t *testing.T) {
	cfg, err := Load(".")
	if err != nil {
		t.Fatalf("failed to load config: %v", err)
	}
	if cfg == nil {
		t.Fatal("config is nil")
	}
	if len(cfg.Models) == 0 {
		t.Error("expected models to be loaded")
	}
	if len(cfg.Tasks) == 0 {
		t.Error("expected tasks to be loaded")
	}
	if len(cfg.RouteClasses) == 0 {
		t.Error("expected route classes to be loaded")
	}
}

func TestModelsHaveRequiredFields(t *testing.T) {
	cfg, err := Load(".")
	if err != nil {
		t.Fatalf("failed to load config: %v", err)
	}

	for name, m := range cfg.Models {
		if m.Provider == "" {
			t.Errorf("model %s missing provider", name)
		}
		if m.APIModel == "" {
			t.Errorf("model %s missing api_model", name)
		}
		if m.QualityCeiling <= 0 {
			t.Errorf("model %s has invalid quality_ceiling", name)
		}
	}
}

func TestGetFailoverChain(t *testing.T) {
	cfg, err := Load(".")
	if err != nil {
		t.Fatalf("failed to load config: %v", err)
	}

	chain := cfg.GetFailoverChain("premium")
	if len(chain) == 0 {
		t.Error("expected premium failover chain to have models")
	}
	if chain[0] != "claude-opus" {
		t.Errorf("expected first model in premium chain to be claude-opus, got %s", chain[0])
	}
}

func TestTiersExist(t *testing.T) {
	cfg, err := Load(".")
	if err != nil {
		t.Fatalf("failed to load config: %v", err)
	}

	for _, tier := range []string{"premium", "budget", "speed", "free"} {
		if _, ok := cfg.Tiers[tier]; !ok {
			t.Errorf("missing tier: %s", tier)
		}
	}
}
