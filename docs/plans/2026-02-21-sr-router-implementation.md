# sr-router Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Build a Go binary that routes LLM requests to the cheapest model meeting quality requirements, with MCP server and HTTP proxy modes.

**Architecture:** Config-driven routing with two-layer classification (route class + task type), weighted model scoring, cascading failover with SSE streaming through all providers. Cobra CLI, SQLite telemetry, YAML configs.

**Tech Stack:** Go 1.26, Cobra, yaml.v3, go-sqlite3, mcp-go, golang.org/x/term

---

### Task 1: Project Scaffold + Git Init

**Files:**
- Create: `go.mod`
- Create: `cmd/main.go`
- Create: `CLAUDE.md`
- Create: `.gitignore`

**Step 1: Initialize git repo and Go module**

```bash
cd /Users/joel.castillo.cq/.repos/github.com/jbctechsolutions/sr-router
git init
```

**Step 2: Create go.mod**

```
module github.com/jbctechsolutions/sr-router

go 1.26.0
```

**Step 3: Create .gitignore**

```
sr-router
*.db
*.sqlite
.env
dist/
```

**Step 4: Create minimal cmd/main.go**

```go
package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

func main() {
	rootCmd := &cobra.Command{
		Use:   "sr-router",
		Short: "Intelligent LLM request router",
		Long:  "Routes LLM requests to the cheapest model that meets quality requirements.",
	}

	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
```

**Step 5: Create CLAUDE.md**

```markdown
# sr-router

Intelligent LLM request router. Routes requests to cheapest model meeting quality requirements.

## Build & Test
- Build: `go build -o sr-router ./cmd/`
- Test: `go test ./...`
- Lint: `go vet ./...`

## Architecture
- `cmd/` - CLI entrypoint (cobra)
- `config/` - YAML config loader + 3 YAML files
- `router/` - classify, route, failover, prompt injection
- `proxy/` - HTTP proxy with SSE streaming
- `mcp/` - MCP server (stdio)
- `telemetry/` - SQLite event logging

## Key Patterns
- Config-driven: models.yaml, tasks.yaml, route_classes.yaml
- Two-layer classification: route class then task type
- Weighted scoring: cost_weight * cost_score + quality_weight * quality_score
- Failover chains per tier
```

**Step 6: Install deps and verify build**

```bash
go get github.com/spf13/cobra
go mod tidy
go build -o sr-router ./cmd/
```

**Step 7: Commit**

```bash
git add .
git commit -m "feat: project scaffold with cobra CLI entrypoint"
```

---

### Task 2: YAML Config Files

**Files:**
- Create: `config/models.yaml`
- Create: `config/tasks.yaml`
- Create: `config/route_classes.yaml`

**Step 1: Create config/models.yaml**

Use the exact YAML from the spec with these changes:
- Remove `groq-llama` from speed tier failover chain (replace with `ollama/llama3.2`)
- Remove `groq-llama` from speed tier models list
- Keep all 6 model definitions (claude-opus, claude-sonnet, minimax-m2, cerebras-glm, ollama/llama3.2, ollama/codellama)

**Step 2: Create config/tasks.yaml**

```yaml
tasks:
  code:
    patterns:
      - "write.*function"
      - "implement.*class"
      - "create.*endpoint"
      - "fix.*bug"
      - "refactor"
      - "add.*test"
      - "debug"
      - "code review"
    required_strengths: [code]
    min_quality: 0.80

  architecture:
    patterns:
      - "design.*system"
      - "architect"
      - "microservice"
      - "database.*schema"
      - "API.*design"
      - "system.*design"
    required_strengths: [architecture, complex_reasoning]
    min_quality: 0.90

  summarization:
    patterns:
      - "summarize"
      - "compress"
      - "condense"
      - "TLDR"
      - "key.*points"
      - "brief"
    required_strengths: [summarization]
    min_quality: 0.50

  data_extraction:
    patterns:
      - "extract"
      - "parse"
      - "CSV"
      - "JSON.*convert"
      - "data.*from"
      - "scrape"
    required_strengths: [data_extraction]
    min_quality: 0.55

  translation:
    patterns:
      - "translate"
      - "convert.*language"
      - "localize"
    required_strengths: [translation]
    min_quality: 0.60

  simple_code:
    patterns:
      - "hello.*world"
      - "boilerplate"
      - "template"
      - "scaffold"
      - "generate.*config"
    required_strengths: [simple_code]
    min_quality: 0.60

  chat:
    patterns:
      - "explain"
      - "what.*is"
      - "how.*does"
      - "tell.*me"
      - "help.*me"
    required_strengths: []
    min_quality: 0.70

  code_review:
    patterns:
      - "review.*code"
      - "review.*PR"
      - "code.*quality"
      - "find.*issues"
    required_strengths: [code_review]
    min_quality: 0.85
```

**Step 3: Create config/route_classes.yaml**

Use the exact YAML from the spec.

**Step 4: Commit**

```bash
git add config/
git commit -m "feat: add YAML config files for models, tasks, and route classes"
```

---

### Task 3: Config Loader + Types

**Files:**
- Create: `config/config.go`
- Create: `config/config_test.go`

**Step 1: Write failing test for config loading**

`config/config_test.go`:
```go
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
```

**Step 2: Run test to verify it fails**

```bash
cd /Users/joel.castillo.cq/.repos/github.com/jbctechsolutions/sr-router
go test ./config/ -v
```

Expected: FAIL (config.go doesn't exist)

**Step 3: Implement config/config.go**

```go
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

func Load(configDir string) (*Config, error) {
	cfg := &Config{}

	// Load models.yaml (has defaults, tiers, failover, models at top level)
	modelsFile := filepath.Join(configDir, "models.yaml")
	if err := loadYAML(modelsFile, cfg); err != nil {
		return nil, fmt.Errorf("loading models.yaml: %w", err)
	}

	// Load tasks.yaml (has tasks key)
	var tasksWrapper struct {
		Tasks map[string]TaskSpec `yaml:"tasks"`
	}
	tasksFile := filepath.Join(configDir, "tasks.yaml")
	if err := loadYAML(tasksFile, &tasksWrapper); err != nil {
		return nil, fmt.Errorf("loading tasks.yaml: %w", err)
	}
	cfg.Tasks = tasksWrapper.Tasks

	// Load route_classes.yaml
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

func (c *Config) GetFailoverChain(tier string) []string {
	if f, ok := c.Failover[tier]; ok {
		return f.Chain
	}
	return []string{c.Defaults.FallbackModel}
}

func (c *Config) GetTierModels(tier string) []string {
	if t, ok := c.Tiers[tier]; ok {
		return t.Models
	}
	return nil
}
```

**Step 4: Install yaml dep and run tests**

```bash
go get gopkg.in/yaml.v3
go test ./config/ -v
```

Expected: PASS

**Step 5: Commit**

```bash
git add config/config.go config/config_test.go go.mod go.sum
git commit -m "feat: config loader with typed structs for all 3 YAML files"
```

---

### Task 4: Task Classifier

**Files:**
- Create: `router/classify.go`
- Create: `router/classify_test.go`

**Step 1: Write failing tests**

`router/classify_test.go`:
```go
package router

import (
	"testing"

	"github.com/jbctechsolutions/sr-router/config"
)

func loadTestConfig(t *testing.T) *config.Config {
	t.Helper()
	cfg, err := config.Load("../config")
	if err != nil {
		t.Fatalf("failed to load config: %v", err)
	}
	return cfg
}

func TestClassifyTaskType(t *testing.T) {
	cfg := loadTestConfig(t)
	c := NewClassifier(cfg)

	tests := []struct {
		prompt   string
		wantType string
	}{
		{"Write a Go function for rate limiting", "code"},
		{"Design a microservice architecture", "architecture"},
		{"Summarize this document", "summarization"},
		{"Extract emails from this CSV", "data_extraction"},
		{"Translate this to Spanish", "translation"},
		{"What is a goroutine?", "chat"},
	}

	for _, tt := range tests {
		t.Run(tt.prompt, func(t *testing.T) {
			result := c.Classify(tt.prompt, nil)
			if result.TaskType != tt.wantType {
				t.Errorf("got task type %q, want %q", result.TaskType, tt.wantType)
			}
		})
	}
}

func TestClassifyRouteClass(t *testing.T) {
	cfg := loadTestConfig(t)
	c := NewClassifier(cfg)

	// Compaction detection via content patterns
	result := c.Classify("Please summarize this conversation history", nil)
	if result.RouteClass != "compaction" {
		t.Errorf("expected compaction route class, got %s", result.RouteClass)
	}
}

func TestClassifyRouteClassFromHeaders(t *testing.T) {
	cfg := loadTestConfig(t)
	c := NewClassifier(cfg)

	headers := map[string]string{"x-request-type": "background"}
	result := c.Classify("Do something", headers)
	if result.RouteClass != "background" {
		t.Errorf("expected background route class, got %s", result.RouteClass)
	}
}

func TestClassifySetsTierFromRouteClass(t *testing.T) {
	cfg := loadTestConfig(t)
	c := NewClassifier(cfg)

	headers := map[string]string{"x-request-type": "background"}
	result := c.Classify("Process this batch", headers)
	if result.Tier != "budget" {
		t.Errorf("expected budget tier for background, got %s", result.Tier)
	}
}
```

**Step 2: Run test to verify it fails**

```bash
go test ./router/ -v
```

**Step 3: Implement router/classify.go**

```go
package router

import (
	"regexp"
	"strings"

	"github.com/jbctechsolutions/sr-router/config"
)

type Classification struct {
	RouteClass        string
	TaskType          string
	Tier              string
	MinQuality        float64
	LatencyBudgetMs   int
	RequiredStrengths []string
	Confidence        float64
}

type Classifier struct {
	cfg            *config.Config
	taskPatterns   map[string][]*regexp.Regexp
	routePatterns  map[string]*compiledRoutePatterns
}

type compiledRoutePatterns struct {
	contentPatterns      []*regexp.Regexp
	systemPromptPatterns []*regexp.Regexp
}

func NewClassifier(cfg *config.Config) *Classifier {
	c := &Classifier{
		cfg:           cfg,
		taskPatterns:  make(map[string][]*regexp.Regexp),
		routePatterns: make(map[string]*compiledRoutePatterns),
	}

	for name, task := range cfg.Tasks {
		for _, p := range task.Patterns {
			re, err := regexp.Compile("(?i)" + p)
			if err == nil {
				c.taskPatterns[name] = append(c.taskPatterns[name], re)
			}
		}
	}

	for name, rc := range cfg.RouteClasses {
		crp := &compiledRoutePatterns{}
		for _, p := range rc.Detection.ContentPatterns {
			re, err := regexp.Compile("(?i)" + p)
			if err == nil {
				crp.contentPatterns = append(crp.contentPatterns, re)
			}
		}
		for _, p := range rc.Detection.SystemPromptPatterns {
			re, err := regexp.Compile("(?i)" + p)
			if err == nil {
				crp.systemPromptPatterns = append(crp.systemPromptPatterns, re)
			}
		}
		c.routePatterns[name] = crp
	}

	return c
}

func (c *Classifier) Classify(prompt string, headers map[string]string) Classification {
	routeClass := c.detectRouteClass(prompt, headers)
	taskType, strengths, confidence := c.detectTaskType(prompt)

	rc := c.cfg.RouteClasses[routeClass]
	minQuality := rc.QualityFloor

	if task, ok := c.cfg.Tasks[taskType]; ok {
		if task.MinQuality > minQuality {
			minQuality = task.MinQuality
		}
	}

	return Classification{
		RouteClass:        routeClass,
		TaskType:          taskType,
		Tier:              rc.DefaultTier,
		MinQuality:        minQuality,
		LatencyBudgetMs:   rc.LatencyBudgetMs,
		RequiredStrengths: strengths,
		Confidence:        confidence,
	}
}

func (c *Classifier) detectRouteClass(prompt string, headers map[string]string) string {
	// 1. Check headers first (explicit)
	if rt, ok := headers["x-request-type"]; ok {
		for name := range c.cfg.RouteClasses {
			for _, h := range c.cfg.RouteClasses[name].Detection.Headers {
				if strings.Contains(h, rt) {
					return name
				}
			}
		}
	}

	// 2. Check content patterns (compaction detection)
	for name, crp := range c.routePatterns {
		for _, re := range crp.contentPatterns {
			if re.MatchString(prompt) {
				return name
			}
		}
	}

	// 3. Default to interactive
	return "interactive"
}

func (c *Classifier) detectTaskType(prompt string) (string, []string, float64) {
	bestType := "chat"
	bestCount := 0
	var bestStrengths []string

	for name, patterns := range c.taskPatterns {
		count := 0
		for _, re := range patterns {
			if re.MatchString(prompt) {
				count++
			}
		}
		if count > bestCount {
			bestCount = count
			bestType = name
			if task, ok := c.cfg.Tasks[name]; ok {
				bestStrengths = task.RequiredStrengths
			}
		}
	}

	confidence := 0.5
	if bestCount >= 2 {
		confidence = 0.85
	} else if bestCount == 1 {
		confidence = 0.70
	}

	return bestType, bestStrengths, confidence
}
```

**Step 4: Run tests**

```bash
go test ./router/ -v
```

Expected: PASS

**Step 5: Commit**

```bash
git add router/
git commit -m "feat: two-layer task classifier with route class and task type detection"
```

---

### Task 5: Router / Scorer

**Files:**
- Create: `router/route.go`
- Create: `router/route_test.go`

**Step 1: Write failing tests**

`router/route_test.go`:
```go
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

	// Budget tier for summarization should pick a cheap model
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
```

**Step 2: Run test to verify it fails**

```bash
go test ./router/ -v -run TestRoute
```

**Step 3: Implement router/route.go**

```go
package router

import (
	"sort"

	"github.com/jbctechsolutions/sr-router/config"
)

type RoutingDecision struct {
	Model        string
	Score        float64
	Tier         string
	Reasoning    string
	EstCost      float64
	Alternatives []Alternative
}

type Alternative struct {
	Model string
	Score float64
}

type Router struct {
	cfg *config.Config
}

func NewRouter(cfg *config.Config) *Router {
	return &Router{cfg: cfg}
}

func (r *Router) Route(class Classification) RoutingDecision {
	tierModels := r.cfg.GetTierModels(class.Tier)
	if len(tierModels) == 0 {
		// Fall back to all models
		for name := range r.cfg.Models {
			tierModels = append(tierModels, name)
		}
	}

	type scored struct {
		name  string
		score float64
	}

	var candidates []scored

	// Find max cost for normalization
	maxCost := 0.0
	for _, name := range tierModels {
		if m, ok := r.cfg.Models[name]; ok {
			if m.CostPer1kTok > maxCost {
				maxCost = m.CostPer1kTok
			}
		}
	}
	if maxCost == 0 {
		maxCost = 1.0 // avoid division by zero
	}

	for _, name := range tierModels {
		m, ok := r.cfg.Models[name]
		if !ok {
			continue
		}

		// Filter: quality ceiling must meet minimum
		if m.QualityCeiling < class.MinQuality {
			continue
		}

		// Filter: must have required strengths
		if !hasStrengths(m.Strengths, class.RequiredStrengths) {
			continue
		}

		// Score: weighted combination
		qualityScore := m.QualityCeiling
		costScore := 1.0 - (m.CostPer1kTok / maxCost) // lower cost = higher score

		cw := r.cfg.Defaults.CostWeight
		qw := r.cfg.Defaults.QualityWeight
		total := cw*costScore + qw*qualityScore

		candidates = append(candidates, scored{name: name, score: total})
	}

	if len(candidates) == 0 {
		// Fallback
		return RoutingDecision{
			Model:     r.cfg.Defaults.FallbackModel,
			Score:     0,
			Tier:      class.Tier,
			Reasoning: "no qualified models in tier, using fallback",
		}
	}

	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].score > candidates[j].score
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
```

**Step 4: Run tests**

```bash
go test ./router/ -v
```

Expected: PASS

**Step 5: Commit**

```bash
git add router/route.go router/route_test.go
git commit -m "feat: model router with weighted scoring and tier filtering"
```

---

### Task 6: Prompt Suffix Injection

**Files:**
- Create: `router/prompt.go`
- Create: `router/prompt_test.go`

**Step 1: Write failing test**

`router/prompt_test.go`:
```go
package router

import (
	"testing"
)

func TestInjectSuffix(t *testing.T) {
	cfg := loadTestConfig(t)
	r := NewRouter(cfg)

	// MiniMax has a prompt suffix defined
	system := "You are a helpful assistant."
	result := r.InjectSuffix("minimax-m2", system)
	if result == system {
		t.Error("expected suffix to be injected for minimax-m2")
	}
}

func TestNoSuffixForClaude(t *testing.T) {
	cfg := loadTestConfig(t)
	r := NewRouter(cfg)

	system := "You are a helpful assistant."
	result := r.InjectSuffix("claude-sonnet", system)
	if result != system {
		t.Error("expected no suffix injection for claude-sonnet")
	}
}
```

**Step 2: Implement router/prompt.go**

```go
package router

import "strings"

func (r *Router) InjectSuffix(modelName string, systemPrompt string) string {
	m, ok := r.cfg.Models[modelName]
	if !ok || m.PromptSuffix == nil {
		return systemPrompt
	}

	suffix := strings.TrimSpace(*m.PromptSuffix)
	if suffix == "" {
		return systemPrompt
	}

	if systemPrompt == "" {
		return suffix
	}

	return systemPrompt + "\n\n" + suffix
}
```

**Step 3: Run tests**

```bash
go test ./router/ -v -run TestInject
```

Expected: PASS

**Step 4: Commit**

```bash
git add router/prompt.go router/prompt_test.go
git commit -m "feat: prompt suffix injection for model-specific instructions"
```

---

### Task 7: Telemetry Collector

**Files:**
- Create: `telemetry/collector.go`
- Create: `telemetry/collector_test.go`

**Step 1: Write failing test**

`telemetry/collector_test.go`:
```go
package telemetry

import (
	"os"
	"testing"
)

func TestRecordAndQueryEvents(t *testing.T) {
	dbPath := "test_telemetry.db"
	defer os.Remove(dbPath)

	c, err := NewCollector(dbPath)
	if err != nil {
		t.Fatalf("failed to create collector: %v", err)
	}
	defer c.Close()

	err = c.RecordRouting(RoutingEvent{
		ID:            "test-1",
		RouteClass:    "interactive",
		TaskType:      "code",
		Tier:          "premium",
		SelectedModel: "claude-sonnet",
		LatencyMs:     1500,
		EstimatedCost: 0.015,
	})
	if err != nil {
		t.Fatalf("failed to record event: %v", err)
	}

	stats, err := c.GetStats("")
	if err != nil {
		t.Fatalf("failed to get stats: %v", err)
	}

	if stats.TotalRequests != 1 {
		t.Errorf("expected 1 request, got %d", stats.TotalRequests)
	}
}

func TestRecordFailover(t *testing.T) {
	dbPath := "test_failover.db"
	defer os.Remove(dbPath)

	c, err := NewCollector(dbPath)
	if err != nil {
		t.Fatalf("failed to create collector: %v", err)
	}
	defer c.Close()

	c.RecordRouting(RoutingEvent{
		ID:            "fo-1",
		RouteClass:    "interactive",
		TaskType:      "code",
		Tier:          "premium",
		SelectedModel: "claude-opus",
	})

	err = c.RecordFailover("fo-1", "claude-opus", "claude-sonnet")
	if err != nil {
		t.Fatalf("failed to record failover: %v", err)
	}
}
```

**Step 2: Implement telemetry/collector.go**

```go
package telemetry

import (
	"database/sql"
	"encoding/json"

	_ "github.com/mattn/go-sqlite3"
)

type Collector struct {
	db *sql.DB
}

type RoutingEvent struct {
	ID            string
	RouteClass    string
	TaskType      string
	Tier          string
	SelectedModel string
	Alternatives  []string
	LatencyMs     int
	EstimatedCost float64
	FailoverFrom  string
	UserRating    int
	UserOverride  string
}

type Stats struct {
	TotalRequests int
	TotalCost     float64
	ByModel       map[string]int
	ByTier        map[string]int
	FailoverCount int
}

func NewCollector(dbPath string) (*Collector, error) {
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return nil, err
	}

	_, err = db.Exec(`CREATE TABLE IF NOT EXISTS routing_events (
		id TEXT PRIMARY KEY,
		timestamp DATETIME DEFAULT CURRENT_TIMESTAMP,
		route_class TEXT,
		task_type TEXT,
		tier TEXT,
		selected_model TEXT,
		alternatives TEXT,
		latency_ms INTEGER,
		estimated_cost REAL,
		failover_from TEXT,
		user_rating INTEGER,
		user_override TEXT
	)`)
	if err != nil {
		return nil, err
	}

	return &Collector{db: db}, nil
}

func (c *Collector) Close() error {
	return c.db.Close()
}

func (c *Collector) RecordRouting(e RoutingEvent) error {
	altsJSON, _ := json.Marshal(e.Alternatives)
	_, err := c.db.Exec(
		`INSERT INTO routing_events (id, route_class, task_type, tier, selected_model, alternatives, latency_ms, estimated_cost)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		e.ID, e.RouteClass, e.TaskType, e.Tier, e.SelectedModel, string(altsJSON), e.LatencyMs, e.EstimatedCost,
	)
	return err
}

func (c *Collector) RecordFailover(eventID, fromModel, toModel string) error {
	_, err := c.db.Exec(
		`UPDATE routing_events SET failover_from = ?, selected_model = ? WHERE id = ?`,
		fromModel, toModel, eventID,
	)
	return err
}

func (c *Collector) RecordFeedback(eventID string, rating int, override string) error {
	_, err := c.db.Exec(
		`UPDATE routing_events SET user_rating = ?, user_override = ? WHERE id = ?`,
		rating, override, eventID,
	)
	return err
}

func (c *Collector) GetStats(modelFilter string) (*Stats, error) {
	stats := &Stats{
		ByModel: make(map[string]int),
		ByTier:  make(map[string]int),
	}

	query := `SELECT COUNT(*), COALESCE(SUM(estimated_cost), 0) FROM routing_events`
	args := []interface{}{}
	if modelFilter != "" {
		query += ` WHERE selected_model = ?`
		args = append(args, modelFilter)
	}

	err := c.db.QueryRow(query, args...).Scan(&stats.TotalRequests, &stats.TotalCost)
	if err != nil {
		return nil, err
	}

	rows, err := c.db.Query(`SELECT selected_model, COUNT(*) FROM routing_events GROUP BY selected_model`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var model string
		var count int
		rows.Scan(&model, &count)
		stats.ByModel[model] = count
	}

	rows2, err := c.db.Query(`SELECT tier, COUNT(*) FROM routing_events GROUP BY tier`)
	if err != nil {
		return nil, err
	}
	defer rows2.Close()
	for rows2.Next() {
		var tier string
		var count int
		rows2.Scan(&tier, &count)
		stats.ByTier[tier] = count
	}

	c.db.QueryRow(`SELECT COUNT(*) FROM routing_events WHERE failover_from IS NOT NULL`).Scan(&stats.FailoverCount)

	return stats, nil
}
```

**Step 3: Install dep and run tests**

```bash
go get github.com/mattn/go-sqlite3
go test ./telemetry/ -v
```

Expected: PASS

**Step 4: Commit**

```bash
git add telemetry/
git commit -m "feat: SQLite telemetry collector for routing events"
```

---

### Task 8: Failover Engine + Provider Calls

**Files:**
- Create: `router/failover.go`
- Create: `router/providers.go`
- Create: `router/failover_test.go`

**Step 1: Write failing test**

`router/failover_test.go`:
```go
package router

import (
	"testing"
)

func TestIsRetryable(t *testing.T) {
	tests := []struct {
		statusCode int
		want       bool
	}{
		{429, true},
		{500, true},
		{502, true},
		{503, true},
		{400, false},
		{401, false},
		{404, false},
	}

	for _, tt := range tests {
		got := isRetryableStatus(tt.statusCode)
		if got != tt.want {
			t.Errorf("isRetryableStatus(%d) = %v, want %v", tt.statusCode, got, tt.want)
		}
	}
}
```

**Step 2: Implement router/providers.go**

This file contains the HTTP client logic for calling each provider type:
- `callAnthropic(ctx, model, req)` — passthrough to Anthropic API
- `callOpenAICompat(ctx, model, req)` — translate to OpenAI format, call base_url
- `callOllama(ctx, model, req)` — translate to Ollama format, call localhost

Each returns `(responseBody io.ReadCloser, statusCode int, error)` for streaming support.

Provider key resolution: read from env vars based on provider name:
- anthropic → `ANTHROPIC_API_KEY`
- openai_compat with "minimax" in base_url → `MINIMAX_API_KEY`
- openai_compat with "cerebras" in base_url → `CEREBRAS_API_KEY`

**Step 3: Implement router/failover.go**

Core function: `ExecuteWithFailover(ctx, classification, request) (response, error)`

1. Get failover chain from config based on tier
2. For each model in chain: call provider, on success return, on retryable error continue
3. Record failover events in telemetry
4. If all exhausted, return error

```go
func isRetryableStatus(code int) bool {
	return code == 429 || (code >= 500 && code < 600)
}
```

**Step 4: Run tests**

```bash
go test ./router/ -v -run TestIsRetryable
```

Expected: PASS

**Step 5: Commit**

```bash
git add router/failover.go router/providers.go router/failover_test.go
git commit -m "feat: failover engine with provider abstraction layer"
```

---

### Task 9: SSE Streaming Support

**Files:**
- Create: `proxy/stream.go`
- Create: `proxy/stream_test.go`

**Step 1: Implement proxy/stream.go**

Three streaming translators:
- `StreamAnthropicPassthrough(w, resp)` — pipe raw SSE from Anthropic → client
- `StreamOpenAIToAnthropic(w, resp)` — translate OpenAI SSE chunks to Anthropic event format (message_start, content_block_start, content_block_delta, message_stop)
- `StreamOllamaToAnthropic(w, resp)` — translate Ollama streaming JSON lines to Anthropic SSE

All use `flusher, ok := w.(http.Flusher)` to flush after each event.

Anthropic SSE event format to emit:
```
event: message_start
data: {"type":"message_start","message":{"id":"...","type":"message","role":"assistant","model":"...","content":[],"usage":{"input_tokens":0,"output_tokens":0}}}

event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"chunk"}}

event: content_block_stop
data: {"type":"content_block_stop","index":0}

event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":N}}

event: message_stop
data: {"type":"message_stop"}
```

**Step 2: Write test for SSE format**

Test that `StreamOpenAIToAnthropic` translates a mock OpenAI SSE response into valid Anthropic SSE events using `httptest.NewRecorder()`.

**Step 3: Run tests**

```bash
go test ./proxy/ -v
```

**Step 4: Commit**

```bash
git add proxy/
git commit -m "feat: SSE streaming translators for all provider types"
```

---

### Task 10: HTTP Proxy Server

**Files:**
- Create: `proxy/server.go`
- Create: `proxy/types.go`

**Step 1: Implement proxy/types.go**

Copy and adapt the Anthropic/OpenAI/Ollama request/response types from `claude-proxy/main.go` — they're already well-tested. Add streaming-specific types.

**Step 2: Implement proxy/server.go**

```go
type ProxyServer struct {
	classifier *router.Classifier
	router     *router.Router
	telemetry  *telemetry.Collector
	cfg        *config.Config
	port       string
}

func (p *ProxyServer) handleMessages(w http.ResponseWriter, r *http.Request) {
	// 1. Read body
	// 2. Parse as Anthropic request
	// 3. Extract system + messages text for classification
	// 4. Classify (route class from headers + content, task type from content)
	// 5. Route (score models, pick best)
	// 6. Determine if streaming requested (check req body for "stream": true)
	// 7. Inject prompt suffix
	// 8. Build provider-specific request
	// 9. Forward to provider
	// 10. If streaming: use appropriate stream translator
	//     If not streaming: buffer response, translate to Anthropic format, return
	// 11. Record telemetry
}
```

Add `/health`, `/dashboard` (simple JSON stats endpoint), and `/v1/messages` routes.

**Step 3: Run manual test**

```bash
go build -o sr-router ./cmd/
./sr-router proxy --port 8889
# In another terminal:
curl -X POST http://localhost:8889/v1/messages \
  -H "Content-Type: application/json" \
  -H "x-api-key: test" \
  -d '{"model":"claude-sonnet","messages":[{"role":"user","content":"Hello"}],"max_tokens":100}'
```

**Step 4: Commit**

```bash
git add proxy/ cmd/
git commit -m "feat: HTTP proxy server with Anthropic API compatibility"
```

---

### Task 11: MCP Server

**Files:**
- Create: `mcp/server.go`

**Step 1: Implement mcp/server.go**

Use `github.com/mark3labs/mcp-go` for stdio transport. Expose 4 tools:

- `route` — classify + route, return decision JSON
- `classify` — classify only, return classification JSON
- `models` — list all configured models with costs/capabilities
- `stats` — return telemetry stats

**Step 2: Install dep**

```bash
go get github.com/mark3labs/mcp-go
```

**Step 3: Test with mcp-inspector or manual stdin/stdout**

```bash
echo '{"jsonrpc":"2.0","id":1,"method":"tools/list"}' | ./sr-router mcp
```

**Step 4: Commit**

```bash
git add mcp/
git commit -m "feat: MCP server with route, classify, models, and stats tools"
```

---

### Task 12: CLI Commands

**Files:**
- Modify: `cmd/main.go`

**Step 1: Add all cobra subcommands**

- `route <prompt>` with `--background`, `--interactive` flags
- `classify <prompt>`
- `models` with `--tier` filter
- `proxy` with `--port` and `--dashboard` flags
- `mcp` (starts MCP server)
- `stats` with `--model` filter
- `feedback <event_id>` with `--rating` and `--override` flags
- `config validate` and `config init`

**Step 2: Wire each command to the appropriate package**

Each command: load config → create classifier/router → execute → print result.

**Step 3: Test CLI**

```bash
go build -o sr-router ./cmd/
./sr-router route "Write a Go function for rate limiting"
./sr-router classify "Design a microservice architecture"
./sr-router models
./sr-router models --tier budget
./sr-router config validate
```

**Step 4: Commit**

```bash
git add cmd/
git commit -m "feat: complete CLI with route, classify, models, proxy, mcp, stats commands"
```

---

### Task 13: README + Final Polish

**Files:**
- Create: `README.md`
- Verify: `CLAUDE.md` is complete

**Step 1: Write README.md**

Sections: one-liner pitch, quick start (3 commands), how it works (diagram), config examples, cost savings table, CLI reference.

**Step 2: Run full test suite**

```bash
go test ./... -v
go vet ./...
go build -o sr-router ./cmd/
```

**Step 3: Final commit**

```bash
git add README.md
git commit -m "docs: README with quick start, architecture diagram, and CLI reference"
```

---

### Definition of Done Checklist

- [ ] `sr-router route "prompt"` returns routing decision with model, reasoning, cost
- [ ] `sr-router proxy --port 8889` accepts Anthropic API requests and routes them
- [ ] Streaming works through the proxy
- [ ] Background detection works (piped input or header → budget tier)
- [ ] Compaction detection works (summarize conversation → speed tier)
- [ ] Failover works (primary fails → cascades to fallback)
- [ ] Prompt suffixes inject correctly for MiniMax/Ollama
- [ ] `sr-router stats` shows routing decisions and cost savings
- [ ] `sr-router mcp` starts and responds to tool calls
- [ ] README explains what it does and how to use it
- [ ] `go build` produces a single binary
- [ ] All tests pass with `go test ./...`
