package mcp

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jbctechsolutions/sr-router/config"
	"github.com/jbctechsolutions/sr-router/router"
	"github.com/jbctechsolutions/sr-router/telemetry"
	mcpgo "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// MCPServer exposes sr-router capabilities over the Model Context Protocol
// using stdio transport. It wraps the classifier, router, and telemetry
// collector and registers four tools: route, classify, models, and stats.
type MCPServer struct {
	cfg        *config.Config
	classifier *router.Classifier
	router     *router.Router
	telemetry  *telemetry.Collector
}

// NewMCPServer constructs an MCPServer from the already-initialized
// dependencies. The caller is responsible for loading config and building the
// classifier, router, and telemetry collector before calling this.
func NewMCPServer(
	cfg *config.Config,
	classifier *router.Classifier,
	rtr *router.Router,
	tel *telemetry.Collector,
) *MCPServer {
	return &MCPServer{
		cfg:        cfg,
		classifier: classifier,
		router:     rtr,
		telemetry:  tel,
	}
}

// Start registers all tools with a new MCP server and begins serving requests
// over stdio. It blocks until stdin is closed or an error occurs.
func (m *MCPServer) Start() error {
	s := server.NewMCPServer(
		"sr-router",
		"0.1.0",
		server.WithToolCapabilities(true),
	)

	s.AddTool(mcpgo.NewTool("route",
		mcpgo.WithDescription("Classify a prompt and return the optimal model routing decision"),
		mcpgo.WithString("prompt",
			mcpgo.Required(),
			mcpgo.Description("The prompt to classify and route"),
		),
		mcpgo.WithString("mode",
			mcpgo.Description("Override route class: interactive, background, or compaction"),
		),
	), m.handleRoute)

	s.AddTool(mcpgo.NewTool("classify",
		mcpgo.WithDescription("Classify a prompt without routing — returns task type and route class"),
		mcpgo.WithString("prompt",
			mcpgo.Required(),
			mcpgo.Description("The prompt to classify"),
		),
	), m.handleClassify)

	s.AddTool(mcpgo.NewTool("models",
		mcpgo.WithDescription("List configured models with capabilities and costs"),
		mcpgo.WithString("tier",
			mcpgo.Description("Filter by tier: premium, budget, speed, free"),
		),
	), m.handleModels)

	s.AddTool(mcpgo.NewTool("stats",
		mcpgo.WithDescription("Show routing statistics and cost savings"),
		mcpgo.WithString("model",
			mcpgo.Description("Filter stats by model name"),
		),
	), m.handleStats)

	return server.ServeStdio(s)
}

// routeResult is the JSON shape returned by the route tool.
type routeResult struct {
	Model        string               `json:"model"`
	Score        float64              `json:"score"`
	Tier         string               `json:"tier"`
	Reasoning    string               `json:"reasoning"`
	RouteClass   string               `json:"route_class"`
	TaskType     string               `json:"task_type"`
	Alternatives []router.Alternative `json:"alternatives"`
}

// handleRoute classifies the prompt and selects the best model.
// An optional "mode" argument overrides the route class detected from content.
func (m *MCPServer) handleRoute(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
	prompt, err := req.RequireString("prompt")
	if err != nil {
		return mcpgo.NewToolResultError(err.Error()), nil
	}

	// Build headers for the classifier. If the caller supplied a mode override,
	// translate it into the x-request-type header so detectRouteClass picks it up.
	headers := make(map[string]string)
	if mode := req.GetString("mode", ""); mode != "" {
		headers["x-request-type"] = mode
	}

	classification := m.classifier.Classify(prompt, headers)
	decision := m.router.Route(classification)

	result := routeResult{
		Model:        decision.Model,
		Score:        decision.Score,
		Tier:         decision.Tier,
		Reasoning:    decision.Reasoning,
		RouteClass:   classification.RouteClass,
		TaskType:     classification.TaskType,
		Alternatives: decision.Alternatives,
	}

	b, err := json.Marshal(result)
	if err != nil {
		return mcpgo.NewToolResultError(fmt.Sprintf("marshal result: %v", err)), nil
	}
	return mcpgo.NewToolResultText(string(b)), nil
}

// classifyResult is the JSON shape returned by the classify tool.
type classifyResult struct {
	RouteClass        string   `json:"route_class"`
	TaskType          string   `json:"task_type"`
	Tier              string   `json:"tier"`
	MinQuality        float64  `json:"min_quality"`
	LatencyBudgetMs   int      `json:"latency_budget_ms"`
	RequiredStrengths []string `json:"required_strengths"`
	Confidence        float64  `json:"confidence"`
}

// handleClassify runs the two-layer classifier and returns the result without
// performing any model selection.
func (m *MCPServer) handleClassify(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
	prompt, err := req.RequireString("prompt")
	if err != nil {
		return mcpgo.NewToolResultError(err.Error()), nil
	}

	classification := m.classifier.Classify(prompt, nil)

	result := classifyResult{
		RouteClass:        classification.RouteClass,
		TaskType:          classification.TaskType,
		Tier:              classification.Tier,
		MinQuality:        classification.MinQuality,
		LatencyBudgetMs:   classification.LatencyBudgetMs,
		RequiredStrengths: classification.RequiredStrengths,
		Confidence:        classification.Confidence,
	}

	b, err := json.Marshal(result)
	if err != nil {
		return mcpgo.NewToolResultError(fmt.Sprintf("marshal result: %v", err)), nil
	}
	return mcpgo.NewToolResultText(string(b)), nil
}

// modelEntry is the JSON shape for a single model in the models tool response.
type modelEntry struct {
	Name           string   `json:"name"`
	Provider       string   `json:"provider"`
	CostPer1kTok   float64  `json:"cost_per_1k_tokens"`
	QualityCeiling float64  `json:"quality_ceiling"`
	Strengths      []string `json:"strengths"`
}

// handleModels returns the list of configured models, optionally filtered by
// tier. When no tier is specified every model in the catalogue is returned.
func (m *MCPServer) handleModels(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
	tierFilter := req.GetString("tier", "")

	// Collect the model names we want to expose.
	var names []string
	if tierFilter != "" {
		names = m.cfg.GetTierModels(tierFilter)
		if len(names) == 0 {
			return mcpgo.NewToolResultError(fmt.Sprintf("unknown tier: %q", tierFilter)), nil
		}
	} else {
		for name := range m.cfg.Models {
			names = append(names, name)
		}
	}

	entries := make([]modelEntry, 0, len(names))
	for _, name := range names {
		model, ok := m.cfg.Models[name]
		if !ok {
			continue
		}
		entries = append(entries, modelEntry{
			Name:           name,
			Provider:       model.Provider,
			CostPer1kTok:   model.CostPer1kTok,
			QualityCeiling: model.QualityCeiling,
			Strengths:      model.Strengths,
		})
	}

	b, err := json.Marshal(entries)
	if err != nil {
		return mcpgo.NewToolResultError(fmt.Sprintf("marshal result: %v", err)), nil
	}
	return mcpgo.NewToolResultText(string(b)), nil
}

// handleStats returns aggregate routing statistics from the telemetry
// collector. An optional "model" argument scopes TotalRequests and TotalCost
// to that model only.
func (m *MCPServer) handleStats(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
	if m.telemetry == nil {
		return mcpgo.NewToolResultError("telemetry collector not available"), nil
	}

	modelFilter := req.GetString("model", "")

	stats, err := m.telemetry.GetStats(modelFilter)
	if err != nil {
		return mcpgo.NewToolResultError(fmt.Sprintf("get stats: %v", err)), nil
	}

	b, err := json.Marshal(stats)
	if err != nil {
		return mcpgo.NewToolResultError(fmt.Sprintf("marshal result: %v", err)), nil
	}
	return mcpgo.NewToolResultText(string(b)), nil
}
