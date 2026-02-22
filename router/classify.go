package router

import (
	"regexp"
	"strings"

	"github.com/jbctechsolutions/sr-router/config"
)

// Classification holds the two-layer classification result for a request.
type Classification struct {
	RouteClass        string
	TaskType          string
	Tier              string
	MinQuality        float64
	LatencyBudgetMs   int
	RequiredStrengths []string
	Confidence        float64
}

// Classifier performs two-layer classification: route class then task type.
// It compiles all patterns once at construction time so Classify is cheap.
type Classifier struct {
	cfg           *config.Config
	taskPatterns  map[string][]*regexp.Regexp
	routePatterns map[string]*compiledRoutePatterns
}

type compiledRoutePatterns struct {
	contentPatterns      []*regexp.Regexp
	systemPromptPatterns []*regexp.Regexp
}

// NewClassifier constructs a Classifier and pre-compiles all regex patterns
// from the provided config. Invalid patterns are silently skipped.
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

// Classify runs the two-layer classification against the prompt and optional
// HTTP headers. Layer 1 determines the route class (interactive, background,
// compaction). Layer 2 determines the task type (code, architecture, etc.).
// The resulting quality floor is the maximum of the route-class floor and the
// task-specific minimum quality.
func (c *Classifier) Classify(prompt string, headers map[string]string) Classification {
	routeClass := c.detectRouteClass(prompt, headers)
	taskType, strengths, confidence := c.detectTaskType(prompt)

	rc := c.cfg.RouteClasses[routeClass]

	// Task min_quality drives the quality floor — this determines which
	// models are eligible. The route class floor no longer forces everything
	// to premium; it only applies as a boost for explicit header overrides.
	minQuality := rc.QualityFloor
	if task, ok := c.cfg.Tasks[taskType]; ok {
		minQuality = task.MinQuality
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

// detectRouteClass applies a three-priority decision:
//  1. Explicit x-request-type header value matched against configured headers.
//  2. Content patterns matched against the prompt text.
//  3. Default to "interactive".
func (c *Classifier) detectRouteClass(prompt string, headers map[string]string) string {
	// Priority 1: explicit header wins.
	if rt, ok := headers["x-request-type"]; ok {
		for name := range c.cfg.RouteClasses {
			for _, h := range c.cfg.RouteClasses[name].Detection.Headers {
				if strings.Contains(h, rt) {
					return name
				}
			}
		}
	}

	// Priority 2: content pattern match.
	for name, crp := range c.routePatterns {
		for _, re := range crp.contentPatterns {
			if re.MatchString(prompt) {
				return name
			}
		}
	}

	// Priority 3: fall back to interactive.
	return "interactive"
}

// detectTaskType scans all task patterns and returns the task name with the
// most pattern hits, the required strengths for that task, and a confidence
// score derived from the hit count. Defaults to "chat" with confidence 0.5
// when no patterns match.
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
