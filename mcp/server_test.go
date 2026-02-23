package mcp

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/jbctechsolutions/sr-router/config"
	"github.com/jbctechsolutions/sr-router/telemetry"
	"github.com/jbctechsolutions/sr-router/router"
	mcpgo "github.com/mark3labs/mcp-go/mcp"
)

// loadTestConfig loads the real YAML configuration used by other test suites.
func loadTestConfig(t *testing.T) *config.Config {
	t.Helper()
	cfg, err := config.Load("../config")
	if err != nil {
		t.Fatalf("failed to load config: %v", err)
	}
	return cfg
}

// newTestServer builds an MCPServer backed by the real config/classifier/router.
// telemetry is optional — pass nil to test the nil-telemetry path.
func newTestServer(t *testing.T, tel *telemetry.Collector) *MCPServer {
	t.Helper()
	cfg := loadTestConfig(t)
	c := router.NewClassifier(cfg)
	r := router.NewRouter(cfg)
	return NewMCPServer(cfg, c, r, tel)
}

// makeRequest builds a CallToolRequest with the given string arguments.
func makeRequest(args map[string]any) mcpgo.CallToolRequest {
	return mcpgo.CallToolRequest{
		Params: mcpgo.CallToolParams{
			Arguments: args,
		},
	}
}

// --- route tool tests ---

func TestHandleRouteCodePrompt(t *testing.T) {
	srv := newTestServer(t, nil)

	result, err := srv.handleRoute(context.Background(), makeRequest(map[string]any{
		"prompt": "Write a Go function for rate limiting",
	}))
	if err != nil {
		t.Fatalf("handleRoute returned error: %v", err)
	}
	if result.IsError {
		t.Fatalf("handleRoute returned tool error: %+v", result.Content)
	}

	var rr routeResult
	text := result.Content[0].(mcpgo.TextContent).Text
	if err := json.Unmarshal([]byte(text), &rr); err != nil {
		t.Fatalf("failed to unmarshal route result: %v", err)
	}

	if rr.Model == "" {
		t.Error("expected non-empty model")
	}
	if rr.Tier == "" {
		t.Error("expected non-empty tier")
	}
	if rr.Score == 0 {
		t.Error("expected non-zero score")
	}
	if rr.TaskType != "code" {
		t.Errorf("expected task_type 'code', got %q", rr.TaskType)
	}
}

func TestHandleRouteReturnsAlternatives(t *testing.T) {
	srv := newTestServer(t, nil)

	result, err := srv.handleRoute(context.Background(), makeRequest(map[string]any{
		"prompt": "What is a goroutine?",
	}))
	if err != nil {
		t.Fatalf("handleRoute returned error: %v", err)
	}

	var rr routeResult
	text := result.Content[0].(mcpgo.TextContent).Text
	if err := json.Unmarshal([]byte(text), &rr); err != nil {
		t.Fatalf("failed to unmarshal route result: %v", err)
	}

	if len(rr.Alternatives) == 0 {
		t.Error("expected alternatives to be populated for a general chat prompt")
	}
}

func TestHandleRouteModeOverride(t *testing.T) {
	srv := newTestServer(t, nil)

	result, err := srv.handleRoute(context.Background(), makeRequest(map[string]any{
		"prompt": "Process this batch of items",
		"mode":   "background",
	}))
	if err != nil {
		t.Fatalf("handleRoute returned error: %v", err)
	}

	var rr routeResult
	text := result.Content[0].(mcpgo.TextContent).Text
	if err := json.Unmarshal([]byte(text), &rr); err != nil {
		t.Fatalf("failed to unmarshal route result: %v", err)
	}

	if rr.RouteClass != "background" {
		t.Errorf("expected route_class 'background' with mode override, got %q", rr.RouteClass)
	}
}

func TestHandleRouteEmptyPrompt(t *testing.T) {
	srv := newTestServer(t, nil)

	// Empty prompt should still succeed (the classifier defaults to "chat").
	result, err := srv.handleRoute(context.Background(), makeRequest(map[string]any{
		"prompt": "",
	}))
	if err != nil {
		t.Fatalf("handleRoute returned error: %v", err)
	}
	if result.IsError {
		t.Fatalf("handleRoute returned tool error for empty prompt: %+v", result.Content)
	}

	var rr routeResult
	text := result.Content[0].(mcpgo.TextContent).Text
	if err := json.Unmarshal([]byte(text), &rr); err != nil {
		t.Fatalf("failed to unmarshal route result: %v", err)
	}

	if rr.Model == "" {
		t.Error("expected a model even for empty prompt")
	}
}

func TestHandleRouteMissingPrompt(t *testing.T) {
	srv := newTestServer(t, nil)

	// No "prompt" key at all -- should return a tool error, not a Go error.
	result, err := srv.handleRoute(context.Background(), makeRequest(map[string]any{}))
	if err != nil {
		t.Fatalf("handleRoute returned Go error: %v", err)
	}
	if !result.IsError {
		t.Error("expected tool error when prompt is missing")
	}
}

// --- classify tool tests ---

func TestHandleClassifyCodePrompt(t *testing.T) {
	srv := newTestServer(t, nil)

	result, err := srv.handleClassify(context.Background(), makeRequest(map[string]any{
		"prompt": "Write a Go function for rate limiting",
	}))
	if err != nil {
		t.Fatalf("handleClassify returned error: %v", err)
	}

	var cr classifyResult
	text := result.Content[0].(mcpgo.TextContent).Text
	if err := json.Unmarshal([]byte(text), &cr); err != nil {
		t.Fatalf("failed to unmarshal classify result: %v", err)
	}

	if cr.TaskType != "code" {
		t.Errorf("expected task_type 'code', got %q", cr.TaskType)
	}
	if cr.RouteClass == "" {
		t.Error("expected non-empty route_class")
	}
}

func TestHandleClassifyArchitecturePrompt(t *testing.T) {
	srv := newTestServer(t, nil)

	result, err := srv.handleClassify(context.Background(), makeRequest(map[string]any{
		"prompt": "Design a microservice architecture",
	}))
	if err != nil {
		t.Fatalf("handleClassify returned error: %v", err)
	}

	var cr classifyResult
	text := result.Content[0].(mcpgo.TextContent).Text
	if err := json.Unmarshal([]byte(text), &cr); err != nil {
		t.Fatalf("failed to unmarshal classify result: %v", err)
	}

	if cr.TaskType != "architecture" {
		t.Errorf("expected task_type 'architecture', got %q", cr.TaskType)
	}
	if cr.MinQuality != 0.90 {
		t.Errorf("expected min_quality 0.90 for architecture, got %.2f", cr.MinQuality)
	}
}

func TestHandleClassifySummarizationDetectsCompaction(t *testing.T) {
	srv := newTestServer(t, nil)

	result, err := srv.handleClassify(context.Background(), makeRequest(map[string]any{
		"prompt": "Please summarize this conversation history",
	}))
	if err != nil {
		t.Fatalf("handleClassify returned error: %v", err)
	}

	var cr classifyResult
	text := result.Content[0].(mcpgo.TextContent).Text
	if err := json.Unmarshal([]byte(text), &cr); err != nil {
		t.Fatalf("failed to unmarshal classify result: %v", err)
	}

	if cr.RouteClass != "compaction" {
		t.Errorf("expected route_class 'compaction', got %q", cr.RouteClass)
	}
}

func TestHandleClassifyMissingPrompt(t *testing.T) {
	srv := newTestServer(t, nil)

	result, err := srv.handleClassify(context.Background(), makeRequest(map[string]any{}))
	if err != nil {
		t.Fatalf("handleClassify returned Go error: %v", err)
	}
	if !result.IsError {
		t.Error("expected tool error when prompt is missing")
	}
}

func TestHandleClassifyEmptyPrompt(t *testing.T) {
	srv := newTestServer(t, nil)

	result, err := srv.handleClassify(context.Background(), makeRequest(map[string]any{
		"prompt": "",
	}))
	if err != nil {
		t.Fatalf("handleClassify returned error: %v", err)
	}
	if result.IsError {
		t.Fatal("handleClassify should not error on empty prompt")
	}

	var cr classifyResult
	text := result.Content[0].(mcpgo.TextContent).Text
	if err := json.Unmarshal([]byte(text), &cr); err != nil {
		t.Fatalf("failed to unmarshal classify result: %v", err)
	}

	// Empty prompt defaults to "chat" with interactive route class.
	if cr.TaskType != "chat" {
		t.Errorf("expected task_type 'chat' for empty prompt, got %q", cr.TaskType)
	}
	if cr.RouteClass != "interactive" {
		t.Errorf("expected route_class 'interactive' for empty prompt, got %q", cr.RouteClass)
	}
}

// --- models tool tests ---

func TestHandleModelsReturnsAll(t *testing.T) {
	srv := newTestServer(t, nil)

	result, err := srv.handleModels(context.Background(), makeRequest(map[string]any{}))
	if err != nil {
		t.Fatalf("handleModels returned error: %v", err)
	}
	if result.IsError {
		t.Fatalf("handleModels returned tool error: %+v", result.Content)
	}

	var entries []modelEntry
	text := result.Content[0].(mcpgo.TextContent).Text
	if err := json.Unmarshal([]byte(text), &entries); err != nil {
		t.Fatalf("failed to unmarshal models result: %v", err)
	}

	cfg := loadTestConfig(t)
	if len(entries) != len(cfg.Models) {
		t.Errorf("expected %d models, got %d", len(cfg.Models), len(entries))
	}

	// Every entry should have a non-empty name and provider.
	for _, e := range entries {
		if e.Name == "" {
			t.Error("model entry has empty name")
		}
		if e.Provider == "" {
			t.Errorf("model %q has empty provider", e.Name)
		}
	}
}

func TestHandleModelsFilterByTier(t *testing.T) {
	srv := newTestServer(t, nil)
	cfg := loadTestConfig(t)

	// Count only tier models that actually have entries in cfg.Models,
	// since the handler skips names without a corresponding model definition.
	countTierModels := func(tier string) int {
		n := 0
		for _, name := range cfg.Tiers[tier].Models {
			if _, ok := cfg.Models[name]; ok {
				n++
			}
		}
		return n
	}

	tests := []struct {
		tier      string
		wantCount int
	}{
		{"premium", countTierModels("premium")},
		{"budget", countTierModels("budget")},
		{"speed", countTierModels("speed")},
		{"free", countTierModels("free")},
	}

	for _, tt := range tests {
		t.Run(tt.tier, func(t *testing.T) {
			result, err := srv.handleModels(context.Background(), makeRequest(map[string]any{
				"tier": tt.tier,
			}))
			if err != nil {
				t.Fatalf("handleModels returned error: %v", err)
			}
			if result.IsError {
				t.Fatalf("handleModels returned tool error: %+v", result.Content)
			}

			var entries []modelEntry
			text := result.Content[0].(mcpgo.TextContent).Text
			if err := json.Unmarshal([]byte(text), &entries); err != nil {
				t.Fatalf("failed to unmarshal models result: %v", err)
			}

			if len(entries) != tt.wantCount {
				t.Errorf("tier %q: expected %d models, got %d", tt.tier, tt.wantCount, len(entries))
			}
		})
	}
}

func TestHandleModelsUnknownTier(t *testing.T) {
	srv := newTestServer(t, nil)

	result, err := srv.handleModels(context.Background(), makeRequest(map[string]any{
		"tier": "nonexistent",
	}))
	if err != nil {
		t.Fatalf("handleModels returned Go error: %v", err)
	}
	if !result.IsError {
		t.Error("expected tool error for unknown tier")
	}
}

// --- stats tool tests ---

func TestHandleStatsWithTelemetry(t *testing.T) {
	// Create a temporary in-memory SQLite database for telemetry.
	tel, err := telemetry.NewCollector(":memory:")
	if err != nil {
		t.Fatalf("failed to create telemetry collector: %v", err)
	}
	defer tel.Close()

	srv := newTestServer(t, tel)

	result, toolErr := srv.handleStats(context.Background(), makeRequest(map[string]any{}))
	if toolErr != nil {
		t.Fatalf("handleStats returned error: %v", toolErr)
	}
	if result.IsError {
		t.Fatalf("handleStats returned tool error: %+v", result.Content)
	}

	var stats telemetry.Stats
	text := result.Content[0].(mcpgo.TextContent).Text
	if err := json.Unmarshal([]byte(text), &stats); err != nil {
		t.Fatalf("failed to unmarshal stats result: %v", err)
	}

	// Empty database should yield zero counts.
	if stats.TotalRequests != 0 {
		t.Errorf("expected 0 total requests, got %d", stats.TotalRequests)
	}
	if stats.TotalCost != 0 {
		t.Errorf("expected 0 total cost, got %f", stats.TotalCost)
	}
}

func TestHandleStatsWithRecordedEvents(t *testing.T) {
	tel, err := telemetry.NewCollector(":memory:")
	if err != nil {
		t.Fatalf("failed to create telemetry collector: %v", err)
	}
	defer tel.Close()

	// Record a couple of events so stats are non-trivial.
	if err := tel.RecordRouting(telemetry.RoutingEvent{
		ID:            "evt-1",
		RouteClass:    "interactive",
		TaskType:      "code",
		Tier:          "premium",
		SelectedModel: "claude-sonnet",
		EstimatedCost: 0.015,
	}); err != nil {
		t.Fatalf("failed to record event: %v", err)
	}
	if err := tel.RecordRouting(telemetry.RoutingEvent{
		ID:            "evt-2",
		RouteClass:    "background",
		TaskType:      "summarization",
		Tier:          "budget",
		SelectedModel: "minimax-m2",
		EstimatedCost: 0.0003,
	}); err != nil {
		t.Fatalf("failed to record event: %v", err)
	}

	srv := newTestServer(t, tel)

	// Unfiltered stats.
	result, toolErr := srv.handleStats(context.Background(), makeRequest(map[string]any{}))
	if toolErr != nil {
		t.Fatalf("handleStats returned error: %v", toolErr)
	}

	var stats telemetry.Stats
	text := result.Content[0].(mcpgo.TextContent).Text
	if err := json.Unmarshal([]byte(text), &stats); err != nil {
		t.Fatalf("failed to unmarshal stats result: %v", err)
	}

	if stats.TotalRequests != 2 {
		t.Errorf("expected 2 total requests, got %d", stats.TotalRequests)
	}

	// Filtered by model.
	result, toolErr = srv.handleStats(context.Background(), makeRequest(map[string]any{
		"model": "claude-sonnet",
	}))
	if toolErr != nil {
		t.Fatalf("handleStats with model filter returned error: %v", toolErr)
	}

	text = result.Content[0].(mcpgo.TextContent).Text
	if err := json.Unmarshal([]byte(text), &stats); err != nil {
		t.Fatalf("failed to unmarshal filtered stats: %v", err)
	}

	if stats.TotalRequests != 1 {
		t.Errorf("expected 1 request for claude-sonnet, got %d", stats.TotalRequests)
	}
}

func TestHandleStatsNilTelemetry(t *testing.T) {
	srv := newTestServer(t, nil)

	result, err := srv.handleStats(context.Background(), makeRequest(map[string]any{}))
	if err != nil {
		t.Fatalf("handleStats returned Go error: %v", err)
	}
	if !result.IsError {
		t.Error("expected tool error when telemetry collector is nil")
	}
}
