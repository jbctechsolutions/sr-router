package config

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Defaults     Defaults                `yaml:"defaults"`
	Tiers        map[string]Tier         `yaml:"tiers"`
	Failover     map[string]FailoverSpec `yaml:"failover"`
	Models       map[string]Model        `yaml:"models"`
	Tasks        map[string]TaskSpec     `yaml:"tasks"`
	RouteClasses map[string]RouteClass   `yaml:"route_classes"`
}

type Defaults struct {
	QualityThreshold float64 `yaml:"quality_threshold"`
	CostWeight       float64 `yaml:"cost_weight"`
	QualityWeight    float64 `yaml:"quality_weight"`
	FallbackModel    string  `yaml:"fallback_model"`
}

type Tier struct {
	Description string   `yaml:"description"`
	Models      []string `yaml:"models"`
}

type FailoverSpec struct {
	Chain      []string `yaml:"chain"`
	RetryOn    []string `yaml:"retry_on"`
	MaxRetries int      `yaml:"max_retries"`
}

type Model struct {
	Provider       string   `yaml:"provider"`
	APIModel       string   `yaml:"api_model"`
	BaseURL        string   `yaml:"base_url,omitempty"`
	Strengths      []string `yaml:"strengths"`
	Weaknesses     []string `yaml:"weaknesses"`
	CostPer1kTok   float64  `yaml:"cost_per_1k_tokens"`
	AvgLatencyMs   int      `yaml:"avg_latency_ms"`
	QualityCeiling float64  `yaml:"quality_ceiling"`
	MaxContext     int      `yaml:"max_context"`
	PromptSuffix   *string  `yaml:"prompt_suffix"`
}

type TaskSpec struct {
	Patterns          []string `yaml:"patterns"`
	RequiredStrengths []string `yaml:"required_strengths"`
	MinQuality        float64  `yaml:"min_quality"`
}

type RouteClass struct {
	Description     string          `yaml:"description"`
	Detection       DetectionConfig `yaml:"detection"`
	DefaultTier     string          `yaml:"default_tier"`
	LatencyBudgetMs int             `yaml:"latency_budget_ms"`
	QualityFloor    float64         `yaml:"quality_floor"`
}

type DetectionConfig struct {
	Stdin                bool     `yaml:"stdin,omitempty"`
	Flags                []string `yaml:"flags,omitempty"`
	Headers              []string `yaml:"headers,omitempty"`
	Env                  []string `yaml:"env,omitempty"`
	ContentPatterns      []string `yaml:"content_patterns,omitempty"`
	SystemPromptPatterns []string `yaml:"system_prompt_patterns,omitempty"`
}

// Load reads the three YAML config files from configDir and merges them into
// a single Config. configDir should be the directory that contains models.yaml,
// tasks.yaml, and route_classes.yaml.
func Load(configDir string) (*Config, error) {
	cfg := &Config{}

	// models.yaml holds defaults, tiers, failover, and models at top level.
	modelsFile := filepath.Join(configDir, "models.yaml")
	if err := loadYAML(modelsFile, cfg); err != nil {
		return nil, fmt.Errorf("loading models.yaml: %w", err)
	}

	// tasks.yaml wraps entries under a "tasks" key.
	var tasksWrapper struct {
		Tasks map[string]TaskSpec `yaml:"tasks"`
	}
	tasksFile := filepath.Join(configDir, "tasks.yaml")
	if err := loadYAML(tasksFile, &tasksWrapper); err != nil {
		return nil, fmt.Errorf("loading tasks.yaml: %w", err)
	}
	cfg.Tasks = tasksWrapper.Tasks

	// route_classes.yaml wraps entries under a "route_classes" key.
	var rcWrapper struct {
		RouteClasses map[string]RouteClass `yaml:"route_classes"`
	}
	rcFile := filepath.Join(configDir, "route_classes.yaml")
	if err := loadYAML(rcFile, &rcWrapper); err != nil {
		return nil, fmt.Errorf("loading route_classes.yaml: %w", err)
	}
	cfg.RouteClasses = rcWrapper.RouteClasses

	return cfg, nil
}

func loadYAML(path string, target interface{}) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return yaml.Unmarshal(data, target)
}

// GetFailoverChain returns the ordered list of model names to try for a tier.
// If the tier has no explicit failover spec, the global fallback model is returned.
func (c *Config) GetFailoverChain(tier string) []string {
	if f, ok := c.Failover[tier]; ok {
		return f.Chain
	}
	return []string{c.Defaults.FallbackModel}
}

// GetTierModels returns the primary model list for a tier, or nil if the tier
// does not exist.
func (c *Config) GetTierModels(tier string) []string {
	if t, ok := c.Tiers[tier]; ok {
		return t.Models
	}
	return nil
}
