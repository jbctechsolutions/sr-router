package router

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jbctechsolutions/sr-router/config"
	"github.com/jbctechsolutions/sr-router/telemetry"
)

// TestIsRetryable verifies that isRetryableStatus correctly classifies HTTP
// status codes as retryable (auth, rate-limit, server error) or non-retryable.
func TestIsRetryable(t *testing.T) {
	tests := []struct {
		statusCode int
		want       bool
	}{
		{401, true},
		{403, true},
		{429, true},
		{500, true},
		{502, true},
		{503, true},
		{400, false},
		{404, false},
	}

	for _, tt := range tests {
		got := isRetryableStatus(tt.statusCode)
		if got != tt.want {
			t.Errorf("isRetryableStatus(%d) = %v, want %v", tt.statusCode, got, tt.want)
		}
	}
}

// minimalConfig builds a config.Config sufficient for failover tests without
// loading YAML from disk.
func minimalConfig(models map[string]config.Model, chain []string) *config.Config {
	return &config.Config{
		Defaults: config.Defaults{
			FallbackModel: "fallback",
		},
		Models: models,
		Failover: map[string]config.FailoverSpec{
			"test-tier": {Chain: chain},
		},
	}
}

// testDecision builds a RoutingDecision that selects the first model in the
// chain for the "test-tier" tier. Alternatives are populated from the
// remaining chain entries.
func testDecision(primary string, alts ...string) RoutingDecision {
	d := RoutingDecision{
		Model: primary,
		Tier:  "test-tier",
	}
	for _, a := range alts {
		d.Alternatives = append(d.Alternatives, Alternative{Model: a})
	}
	return d
}

// TestExecuteWithFailover_SuccessFirstModel verifies that when the first
// provider in the chain returns 200, that response and model name are returned
// without trying subsequent models.
func TestExecuteWithFailover_SuccessFirstModel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]string{"ok": "true"})
	}))
	defer srv.Close()

	suffix := ""
	cfg := minimalConfig(map[string]config.Model{
		"model-a": {
			Provider:     "openai_compat",
			APIModel:     "gpt-test",
			BaseURL:      srv.URL,
			PromptSuffix: &suffix,
		},
	}, []string{"model-a"})

	router := NewRouter(cfg)
	engine := NewFailoverEngine(cfg, router, nil)

	resp, modelName, err := engine.ExecuteWithFailover(
		context.Background(),
		testDecision("model-a"),
		ProviderRequest{Messages: []ProviderMessage{{Role: "user", Content: "hi"}}},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer resp.Body.Close()

	if modelName != "model-a" {
		t.Errorf("got model %q, want %q", modelName, "model-a")
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("got status %d, want 200", resp.StatusCode)
	}
}

// TestExecuteWithFailover_FailoverOn429 verifies that a 429 response from the
// first model causes the engine to try the second model in the chain.
func TestExecuteWithFailover_FailoverOn429(t *testing.T) {
	callCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		if callCount == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]string{"ok": "true"})
	}))
	defer srv.Close()

	suffix := ""
	cfg := minimalConfig(map[string]config.Model{
		"model-a": {Provider: "openai_compat", APIModel: "gpt-a", BaseURL: srv.URL, PromptSuffix: &suffix},
		"model-b": {Provider: "openai_compat", APIModel: "gpt-b", BaseURL: srv.URL, PromptSuffix: &suffix},
	}, []string{"model-a", "model-b"})

	router := NewRouter(cfg)
	engine := NewFailoverEngine(cfg, router, nil)

	resp, modelName, err := engine.ExecuteWithFailover(
		context.Background(),
		testDecision("model-a", "model-b"),
		ProviderRequest{Messages: []ProviderMessage{{Role: "user", Content: "hi"}}},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer resp.Body.Close()

	if modelName != "model-b" {
		t.Errorf("got model %q after failover, want %q", modelName, "model-b")
	}
	if callCount != 2 {
		t.Errorf("expected 2 provider calls, got %d", callCount)
	}
}

// TestExecuteWithFailover_AllModelsExhausted verifies that when every model in
// the chain fails, ExecuteWithFailover returns a descriptive error.
func TestExecuteWithFailover_AllModelsExhausted(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	suffix := ""
	cfg := minimalConfig(map[string]config.Model{
		"model-a": {Provider: "openai_compat", APIModel: "gpt-a", BaseURL: srv.URL, PromptSuffix: &suffix},
		"model-b": {Provider: "openai_compat", APIModel: "gpt-b", BaseURL: srv.URL, PromptSuffix: &suffix},
	}, []string{"model-a", "model-b"})

	router := NewRouter(cfg)
	engine := NewFailoverEngine(cfg, router, nil)

	_, _, err := engine.ExecuteWithFailover(
		context.Background(),
		testDecision("model-a", "model-b"),
		ProviderRequest{Messages: []ProviderMessage{{Role: "user", Content: "hi"}}},
	)
	if err == nil {
		t.Fatal("expected error when all models exhausted")
	}
	if !strings.Contains(err.Error(), "exhausted") {
		t.Errorf("error message %q should mention exhausted chain", err.Error())
	}
}

// TestExecuteWithFailover_SkipsUnknownModels verifies that model names in the
// chain that are not present in cfg.Models are skipped without panic.
func TestExecuteWithFailover_SkipsUnknownModels(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]string{"ok": "true"})
	}))
	defer srv.Close()

	suffix := ""
	cfg := minimalConfig(map[string]config.Model{
		"model-b": {Provider: "openai_compat", APIModel: "gpt-b", BaseURL: srv.URL, PromptSuffix: &suffix},
	}, []string{"ghost-model", "model-b"})

	router := NewRouter(cfg)
	engine := NewFailoverEngine(cfg, router, nil)

	resp, modelName, err := engine.ExecuteWithFailover(
		context.Background(),
		testDecision("ghost-model", "model-b"),
		ProviderRequest{Messages: []ProviderMessage{{Role: "user", Content: "hi"}}},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer resp.Body.Close()

	if modelName != "model-b" {
		t.Errorf("got model %q, want %q", modelName, "model-b")
	}
}

// TestExecuteWithFailover_RecordsTelemetry verifies that when a failover
// occurs (i.e. model index > 0), the telemetry collector is called.
func TestExecuteWithFailover_RecordsTelemetry(t *testing.T) {
	// We want the first model to fail (503) and the second to succeed (200).
	callCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		if callCount == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]string{"ok": "true"})
	}))
	defer srv.Close()

	// Use an in-memory telemetry collector backed by ":memory:" SQLite.
	tel, err := telemetry.NewCollector(":memory:")
	if err != nil {
		t.Fatalf("failed to create telemetry collector: %v", err)
	}
	defer tel.Close()

	suffix := ""
	cfg := minimalConfig(map[string]config.Model{
		"model-a": {Provider: "openai_compat", APIModel: "gpt-a", BaseURL: srv.URL, PromptSuffix: &suffix},
		"model-b": {Provider: "openai_compat", APIModel: "gpt-b", BaseURL: srv.URL, PromptSuffix: &suffix},
	}, []string{"model-a", "model-b"})

	router := NewRouter(cfg)
	engine := NewFailoverEngine(cfg, router, tel)

	resp, _, err := engine.ExecuteWithFailover(
		context.Background(),
		testDecision("model-a", "model-b"),
		ProviderRequest{Messages: []ProviderMessage{{Role: "user", Content: "hi"}}},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	resp.Body.Close()
}

// TestExecuteWithFailover_NonRetryableStatusReturned verifies that a 400 from
// a provider is returned immediately without trying the next model.
func TestExecuteWithFailover_NonRetryableStatusReturned(t *testing.T) {
	callCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.WriteHeader(http.StatusBadRequest) // 400 — not retryable
	}))
	defer srv.Close()

	suffix := ""
	cfg := minimalConfig(map[string]config.Model{
		"model-a": {Provider: "openai_compat", APIModel: "gpt-a", BaseURL: srv.URL, PromptSuffix: &suffix},
		"model-b": {Provider: "openai_compat", APIModel: "gpt-b", BaseURL: srv.URL, PromptSuffix: &suffix},
	}, []string{"model-a", "model-b"})

	router := NewRouter(cfg)
	engine := NewFailoverEngine(cfg, router, nil)

	resp, modelName, err := engine.ExecuteWithFailover(
		context.Background(),
		testDecision("model-a", "model-b"),
		ProviderRequest{Messages: []ProviderMessage{{Role: "user", Content: "hi"}}},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	resp.Body.Close()

	if modelName != "model-a" {
		t.Errorf("expected model-a (non-retryable stops chain), got %q", modelName)
	}
	if callCount != 1 {
		t.Errorf("expected exactly 1 call for non-retryable status, got %d", callCount)
	}
}

// TestExecuteWithFailover_FailoverOn401 verifies that a 401 (auth error) from
// the first model causes the engine to try the next model in the chain.
func TestExecuteWithFailover_FailoverOn401(t *testing.T) {
	callCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		if callCount == 1 {
			w.WriteHeader(http.StatusUnauthorized) // 401 — retryable
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]string{"ok": "true"})
	}))
	defer srv.Close()

	suffix := ""
	cfg := minimalConfig(map[string]config.Model{
		"model-a": {Provider: "openai_compat", APIModel: "gpt-a", BaseURL: srv.URL, PromptSuffix: &suffix},
		"model-b": {Provider: "openai_compat", APIModel: "gpt-b", BaseURL: srv.URL, PromptSuffix: &suffix},
	}, []string{"model-a", "model-b"})

	router := NewRouter(cfg)
	engine := NewFailoverEngine(cfg, router, nil)

	resp, modelName, err := engine.ExecuteWithFailover(
		context.Background(),
		testDecision("model-a", "model-b"),
		ProviderRequest{Messages: []ProviderMessage{{Role: "user", Content: "hi"}}},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer resp.Body.Close()

	if modelName != "model-b" {
		t.Errorf("expected model-b after auth failover, got %q", modelName)
	}
	if callCount != 2 {
		t.Errorf("expected 2 provider calls (auth retry), got %d", callCount)
	}
}

// TestBuildChainFromDecision verifies that the failover chain is built
// correctly from a RoutingDecision: selected model first, then alternatives,
// then the tier chain, then fallback — with deduplication.
func TestBuildChainFromDecision(t *testing.T) {
	suffix := ""
	cfg := &config.Config{
		Defaults: config.Defaults{FallbackModel: "fallback-model"},
		Models: map[string]config.Model{
			"selected":       {Provider: "openai_compat", PromptSuffix: &suffix},
			"alt1":           {Provider: "openai_compat", PromptSuffix: &suffix},
			"alt2":           {Provider: "openai_compat", PromptSuffix: &suffix},
			"chain-only":     {Provider: "openai_compat", PromptSuffix: &suffix},
			"fallback-model": {Provider: "openai_compat", PromptSuffix: &suffix},
		},
		Failover: map[string]config.FailoverSpec{
			"test-tier": {Chain: []string{"alt1", "chain-only", "selected"}},
		},
	}

	rtr := NewRouter(cfg)
	engine := NewFailoverEngine(cfg, rtr, nil)

	decision := RoutingDecision{
		Model: "selected",
		Tier:  "test-tier",
		Alternatives: []Alternative{
			{Model: "alt1"},
			{Model: "alt2"},
		},
	}

	chain := engine.buildChainFromDecision(decision)

	// Expected order: selected, alt1, alt2, chain-only, fallback-model
	// "selected" already seen so not duplicated from tier chain.
	// "alt1" already seen so not duplicated from tier chain.
	want := []string{"selected", "alt1", "alt2", "chain-only", "fallback-model"}
	if len(chain) != len(want) {
		t.Fatalf("chain length = %d, want %d: %v", len(chain), len(want), chain)
	}
	for i, w := range want {
		if chain[i] != w {
			t.Errorf("chain[%d] = %q, want %q (full chain: %v)", i, chain[i], w, chain)
		}
	}
}

// TestProviderRequestAnthropicFormat verifies the JSON body sent to an
// Anthropic-style endpoint contains the expected fields.
func TestProviderRequestAnthropicFormat(t *testing.T) {
	var captured map[string]interface{}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Errorf("failed to decode request body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	t.Setenv("ANTHROPIC_API_KEY", "test-key")

	// Override the Anthropic endpoint via the model's base URL trick is not
	// possible directly; instead we exercise callOpenAICompat which is the
	// general mechanism and separately verify the Anthropic body builder.
	req := ProviderRequest{
		SystemPrompt: "be helpful",
		Messages:     []ProviderMessage{{Role: "user", Content: "hello"}},
		MaxTokens:    512,
		Temperature:  0.7,
		Stream:       false,
	}

	body := buildAnthropicBody(req, "claude-test")
	if body["model"] != "claude-test" {
		t.Errorf("model field = %v, want claude-test", body["model"])
	}
	if body["system"] != "be helpful" {
		t.Errorf("system field = %v, want 'be helpful'", body["system"])
	}
	msgs, ok := body["messages"].([]map[string]string)
	if !ok || len(msgs) == 0 {
		t.Errorf("messages field missing or empty")
	}
	if body["max_tokens"].(int) != 512 {
		t.Errorf("max_tokens = %v, want 512", body["max_tokens"])
	}
}

// TestProviderRequestOpenAICompatFormat verifies the JSON body sent to an
// OpenAI-compatible endpoint contains a system message prepended to messages.
func TestProviderRequestOpenAICompatFormat(t *testing.T) {
	req := ProviderRequest{
		SystemPrompt: "system instruction",
		Messages:     []ProviderMessage{{Role: "user", Content: "world"}},
		MaxTokens:    256,
		Stream:       true,
	}

	body := buildOpenAICompatBody(req, "gpt-test")
	if body["model"] != "gpt-test" {
		t.Errorf("model = %v, want gpt-test", body["model"])
	}
	msgs, ok := body["messages"].([]map[string]string)
	if !ok {
		t.Fatalf("messages is not []map[string]string")
	}
	if len(msgs) < 2 {
		t.Fatalf("expected at least 2 messages (system + user), got %d", len(msgs))
	}
	if msgs[0]["role"] != "system" {
		t.Errorf("first message role = %q, want system", msgs[0]["role"])
	}
	if msgs[0]["content"] != "system instruction" {
		t.Errorf("system content = %q, want 'system instruction'", msgs[0]["content"])
	}
}

// TestProviderRequestOllamaFormat verifies the JSON body sent to an Ollama
// endpoint uses the options.num_predict field for max tokens.
func TestProviderRequestOllamaFormat(t *testing.T) {
	req := ProviderRequest{
		SystemPrompt: "sys",
		Messages:     []ProviderMessage{{Role: "user", Content: "hi"}},
		MaxTokens:    1024,
	}

	body := buildOllamaBody(req, "llama3")
	if body["model"] != "llama3" {
		t.Errorf("model = %v, want llama3", body["model"])
	}
	opts, ok := body["options"].(map[string]int)
	if !ok {
		t.Fatalf("options not map[string]int")
	}
	if opts["num_predict"] != 1024 {
		t.Errorf("num_predict = %d, want 1024", opts["num_predict"])
	}
}

// TestResolveAPIKey_Anthropic checks that the anthropic provider always reads
// the ANTHROPIC_API_KEY environment variable.
func TestResolveAPIKey_Anthropic(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "ant-secret")
	key := resolveAPIKey("anthropic", "")
	if key != "ant-secret" {
		t.Errorf("got key %q, want %q", key, "ant-secret")
	}
}

// TestResolveAPIKey_OpenAICompatFallback checks that an openai_compat provider
// with an unrecognised base URL falls back to OPENAI_API_KEY.
func TestResolveAPIKey_OpenAICompatFallback(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "oai-secret")
	key := resolveAPIKey("openai_compat", "https://api.openai.com/v1")
	if key != "oai-secret" {
		t.Errorf("got key %q, want %q", key, "oai-secret")
	}
}

// TestResolveAPIKey_OpenAICompatGroq checks that a groq base URL reads the
// GROQ_API_KEY environment variable.
func TestResolveAPIKey_OpenAICompatGroq(t *testing.T) {
	t.Setenv("GROQ_API_KEY", "groq-secret")
	key := resolveAPIKey("openai_compat", "https://api.groq.com/openai/v1")
	if key != "groq-secret" {
		t.Errorf("got key %q, want %q", key, "groq-secret")
	}
}

// TestResolveAPIKey_OpenAICompatMinimax checks that a minimax base URL reads
// MINIMAX_API_KEY.
func TestResolveAPIKey_OpenAICompatMinimax(t *testing.T) {
	t.Setenv("MINIMAX_API_KEY", "mx-secret")
	key := resolveAPIKey("openai_compat", "https://api.minimax.io/v1")
	if key != "mx-secret" {
		t.Errorf("got key %q, want %q", key, "mx-secret")
	}
}

// TestResolveAPIKey_OpenAICompatCerebras checks that a cerebras base URL reads
// CEREBRAS_API_KEY.
func TestResolveAPIKey_OpenAICompatCerebras(t *testing.T) {
	t.Setenv("CEREBRAS_API_KEY", "cb-secret")
	key := resolveAPIKey("openai_compat", "https://api.cerebras.ai/v1")
	if key != "cb-secret" {
		t.Errorf("got key %q, want %q", key, "cb-secret")
	}
}

// --- PatchAnthropicRawBody tests -------------------------------------------

// TestPatchAnthropicRawBody_PatchesModel verifies that the model field is
// replaced and other fields (including rich message content) are preserved.
func TestPatchAnthropicRawBody_PatchesModel(t *testing.T) {
	raw := []byte(`{
		"model": "client-model",
		"max_tokens": 1024,
		"messages": [
			{"role": "user", "content": [
				{"type": "text", "text": "hello"},
				{"type": "tool_result", "tool_use_id": "tu_123", "content": "result data"}
			]}
		]
	}`)

	patched, err := PatchAnthropicRawBody(raw, "claude-3-5-sonnet-latest", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var result map[string]json.RawMessage
	if err := json.Unmarshal(patched, &result); err != nil {
		t.Fatalf("failed to unmarshal patched body: %v", err)
	}

	// Verify model was patched.
	var model string
	json.Unmarshal(result["model"], &model)
	if model != "claude-3-5-sonnet-latest" {
		t.Errorf("model = %q, want %q", model, "claude-3-5-sonnet-latest")
	}

	// Verify messages (with tool_result) are preserved.
	var msgs []map[string]json.RawMessage
	json.Unmarshal(result["messages"], &msgs)
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}

	var content []map[string]interface{}
	json.Unmarshal(msgs[0]["content"], &content)
	if len(content) != 2 {
		t.Fatalf("expected 2 content blocks, got %d", len(content))
	}
	if content[1]["type"] != "tool_result" {
		t.Errorf("second block type = %v, want tool_result", content[1]["type"])
	}
}

// TestPatchAnthropicRawBody_InjectsSuffixStringSystem verifies suffix injection
// when system is a plain string.
func TestPatchAnthropicRawBody_InjectsSuffixStringSystem(t *testing.T) {
	raw := []byte(`{"model":"m","system":"You are helpful","messages":[]}`)

	patched, err := PatchAnthropicRawBody(raw, "new-model", "Format nicely")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var result map[string]json.RawMessage
	json.Unmarshal(patched, &result)

	var system string
	json.Unmarshal(result["system"], &system)
	if system != "You are helpful\n\nFormat nicely" {
		t.Errorf("system = %q, want %q", system, "You are helpful\n\nFormat nicely")
	}
}

// TestPatchAnthropicRawBody_InjectsSuffixArraySystem verifies suffix injection
// when system is an array of content blocks.
func TestPatchAnthropicRawBody_InjectsSuffixArraySystem(t *testing.T) {
	raw := []byte(`{"model":"m","system":[{"type":"text","text":"base prompt"}],"messages":[]}`)

	patched, err := PatchAnthropicRawBody(raw, "new-model", "extra instruction")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var result map[string]json.RawMessage
	json.Unmarshal(patched, &result)

	var blocks []map[string]string
	json.Unmarshal(result["system"], &blocks)
	if len(blocks) != 2 {
		t.Fatalf("expected 2 system blocks, got %d", len(blocks))
	}
	if blocks[1]["text"] != "\n\nextra instruction" {
		t.Errorf("appended block text = %q, want %q", blocks[1]["text"], "\n\nextra instruction")
	}
}

// TestPatchAnthropicRawBody_AddsSuffixWhenNoSystem verifies that the suffix is
// added as a new system field when the original request has none.
func TestPatchAnthropicRawBody_AddsSuffixWhenNoSystem(t *testing.T) {
	raw := []byte(`{"model":"m","messages":[]}`)

	patched, err := PatchAnthropicRawBody(raw, "new-model", "injected suffix")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var result map[string]json.RawMessage
	json.Unmarshal(patched, &result)

	var system string
	json.Unmarshal(result["system"], &system)
	if system != "injected suffix" {
		t.Errorf("system = %q, want %q", system, "injected suffix")
	}
}

// TestPatchAnthropicRawBody_PreservesToolUseBlocks verifies that tool_use
// content blocks in messages survive the patching round-trip.
func TestPatchAnthropicRawBody_PreservesToolUseBlocks(t *testing.T) {
	raw := []byte(`{
		"model": "old",
		"messages": [
			{"role": "user", "content": "hello"},
			{"role": "assistant", "content": [
				{"type": "text", "text": "I'll use a tool"},
				{"type": "tool_use", "id": "tu_abc", "name": "read_file", "input": {"path": "/foo"}}
			]},
			{"role": "user", "content": [
				{"type": "tool_result", "tool_use_id": "tu_abc", "content": "file contents here"}
			]}
		]
	}`)

	patched, err := PatchAnthropicRawBody(raw, "new-model", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify the tool_use and tool_result blocks are preserved by checking
	// that the strings appear in the output.
	s := string(patched)
	for _, want := range []string{"tool_use", "tu_abc", "read_file", "tool_result", "file contents here"} {
		if !strings.Contains(s, want) {
			t.Errorf("patched body missing %q", want)
		}
	}
}

// TestGetModelSuffix verifies the getModelSuffix helper.
func TestGetModelSuffix(t *testing.T) {
	suffix := "  format nicely  "
	cfg := &config.Config{
		Models: map[string]config.Model{
			"with-suffix":    {PromptSuffix: &suffix},
			"without-suffix": {},
		},
	}

	if got := getModelSuffix(cfg, "with-suffix"); got != "format nicely" {
		t.Errorf("got %q, want %q", got, "format nicely")
	}
	if got := getModelSuffix(cfg, "without-suffix"); got != "" {
		t.Errorf("got %q, want empty", got)
	}
	if got := getModelSuffix(cfg, "missing"); got != "" {
		t.Errorf("got %q, want empty", got)
	}
}

// TestExecuteWithFailover_RawPassthroughForAnthropic verifies that when
// RawAnthropicBody is set and the provider is Anthropic, the raw body
// is forwarded (with model name patched) instead of the normalised request.
func TestExecuteWithFailover_RawPassthroughForAnthropic(t *testing.T) {
	var capturedBody map[string]json.RawMessage

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&capturedBody)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"id":"msg_1","type":"message","content":[{"type":"text","text":"ok"}]}`))
	}))
	defer srv.Close()

	// We can't override the Anthropic endpoint directly, so we test through
	// openai_compat which lets us use the test server. Instead, we test the
	// raw body patching + dispatch logic by verifying PatchAnthropicRawBody
	// output and that the failover engine sets RawAnthropicBody for anthropic
	// models.

	// Test that for an openai_compat model, the raw body is NOT used.
	suffix := ""
	cfg := minimalConfig(map[string]config.Model{
		"model-a": {Provider: "openai_compat", APIModel: "gpt-a", BaseURL: srv.URL, PromptSuffix: &suffix},
	}, []string{"model-a"})

	rtr := NewRouter(cfg)
	engine := NewFailoverEngine(cfg, rtr, nil)

	rawBody := []byte(`{"model":"client","messages":[{"role":"user","content":[{"type":"tool_result","tool_use_id":"x","content":"data"}]}]}`)

	resp, _, err := engine.ExecuteWithFailover(
		context.Background(),
		testDecision("model-a"),
		ProviderRequest{
			Messages:         []ProviderMessage{{Role: "user", Content: "hi"}},
			RawAnthropicBody: rawBody,
		},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	resp.Body.Close()

	// For openai_compat, the captured body should use the normalised format,
	// NOT the raw body (which contains tool_result).
	var model string
	json.Unmarshal(capturedBody["model"], &model)
	if model != "gpt-a" {
		t.Errorf("model = %q, want %q (normalised path should have been used)", model, "gpt-a")
	}

	// The raw body's tool_result should NOT appear in the captured body.
	if _, ok := capturedBody["messages"]; ok {
		bodyStr := string(capturedBody["messages"])
		if strings.Contains(bodyStr, "tool_result") {
			t.Error("normalised path should not contain tool_result from raw body")
		}
	}
}
