package router

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/jbctechsolutions/sr-router/config"
)

// ProviderRequest is a normalized request that can be translated to any
// provider's wire format.
type ProviderRequest struct {
	SystemPrompt string
	Messages     []ProviderMessage
	MaxTokens    int
	Temperature  float64
	Stream       bool
}

// ProviderMessage is a single turn in the conversation.
type ProviderMessage struct {
	Role    string
	Content string
}

// ProviderResponse holds the result of a fully-consumed, non-streaming call.
// The failover engine works with raw *http.Response bodies; this type is
// provided for callers that want to decode the body themselves.
type ProviderResponse struct {
	Content      string
	Model        string
	InputTokens  int
	OutputTokens int
	StopReason   string
}

// callProvider dispatches to the correct provider implementation based on
// model.Provider. The returned *http.Response body is NOT consumed — the
// caller is responsible for reading and closing it.
func callProvider(ctx context.Context, model config.Model, req ProviderRequest) (*http.Response, error) {
	switch model.Provider {
	case "anthropic":
		return callAnthropic(ctx, model, req)
	case "openai_compat":
		return callOpenAICompat(ctx, model, req)
	case "ollama":
		return callOllama(ctx, model, req)
	default:
		return nil, fmt.Errorf("unknown provider %q", model.Provider)
	}
}

// callAnthropic sends a request to the Anthropic Messages API.
// It uses the ANTHROPIC_API_KEY environment variable for authentication.
func callAnthropic(ctx context.Context, model config.Model, req ProviderRequest) (*http.Response, error) {
	endpoint := "https://api.anthropic.com/v1/messages"

	body := buildAnthropicBody(req, model.APIModel)
	data, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshalling anthropic request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("creating anthropic request: %w", err)
	}

	apiKey := resolveAPIKey("anthropic", "")
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-api-key", apiKey)
	httpReq.Header.Set("anthropic-version", "2023-06-01")

	return http.DefaultClient.Do(httpReq)
}

// callOpenAICompat sends a request to any OpenAI-compatible chat/completions
// endpoint. The base URL is taken from model.BaseURL; the API key is resolved
// from environment variables based on the base URL domain.
func callOpenAICompat(ctx context.Context, model config.Model, req ProviderRequest) (*http.Response, error) {
	endpoint := strings.TrimRight(model.BaseURL, "/") + "/chat/completions"

	body := buildOpenAICompatBody(req, model.APIModel)
	data, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshalling openai_compat request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("creating openai_compat request: %w", err)
	}

	apiKey := resolveAPIKey("openai_compat", model.BaseURL)
	httpReq.Header.Set("Content-Type", "application/json")
	if apiKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+apiKey)
	}

	return http.DefaultClient.Do(httpReq)
}

// callOllama sends a request to an Ollama /api/chat endpoint.
// Ollama typically runs locally and requires no API key.
func callOllama(ctx context.Context, model config.Model, req ProviderRequest) (*http.Response, error) {
	endpoint := strings.TrimRight(model.BaseURL, "/") + "/api/chat"

	body := buildOllamaBody(req, model.APIModel)
	data, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshalling ollama request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("creating ollama request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")

	return http.DefaultClient.Do(httpReq)
}

// resolveAPIKey returns the environment variable value appropriate for the
// given provider and (for openai_compat) base URL.
func resolveAPIKey(provider, baseURL string) string {
	switch provider {
	case "anthropic":
		return os.Getenv("ANTHROPIC_API_KEY")
	case "openai_compat":
		lower := strings.ToLower(baseURL)
		switch {
		case strings.Contains(lower, "minimax"):
			return os.Getenv("MINIMAX_API_KEY")
		case strings.Contains(lower, "cerebras"):
			return os.Getenv("CEREBRAS_API_KEY")
		case strings.Contains(lower, "groq"):
			return os.Getenv("GROQ_API_KEY")
		default:
			return os.Getenv("OPENAI_API_KEY")
		}
	default:
		return ""
	}
}

// buildAnthropicBody constructs the JSON-serialisable map for the Anthropic
// Messages API. It is exported for testing purposes within the package.
func buildAnthropicBody(req ProviderRequest, apiModel string) map[string]interface{} {
	msgs := make([]map[string]string, 0, len(req.Messages))
	for _, m := range req.Messages {
		msgs = append(msgs, map[string]string{
			"role":    m.Role,
			"content": m.Content,
		})
	}

	maxTok := req.MaxTokens
	if maxTok <= 0 {
		maxTok = 4096
	}

	body := map[string]interface{}{
		"model":      apiModel,
		"max_tokens": maxTok,
		"messages":   msgs,
		"stream":     req.Stream,
	}

	if req.SystemPrompt != "" {
		body["system"] = req.SystemPrompt
	}

	return body
}

// buildOpenAICompatBody constructs the JSON-serialisable map for any
// OpenAI-compatible chat/completions endpoint.
func buildOpenAICompatBody(req ProviderRequest, apiModel string) map[string]interface{} {
	msgs := make([]map[string]string, 0, len(req.Messages)+1)

	if req.SystemPrompt != "" {
		msgs = append(msgs, map[string]string{
			"role":    "system",
			"content": req.SystemPrompt,
		})
	}

	for _, m := range req.Messages {
		msgs = append(msgs, map[string]string{
			"role":    m.Role,
			"content": m.Content,
		})
	}

	maxTok := req.MaxTokens
	if maxTok <= 0 {
		maxTok = 4096
	}

	return map[string]interface{}{
		"model":      apiModel,
		"max_tokens": maxTok,
		"messages":   msgs,
		"stream":     req.Stream,
	}
}

// buildOllamaBody constructs the JSON-serialisable map for the Ollama
// /api/chat endpoint. Token limit is conveyed via options.num_predict.
func buildOllamaBody(req ProviderRequest, apiModel string) map[string]interface{} {
	msgs := make([]map[string]string, 0, len(req.Messages)+1)

	if req.SystemPrompt != "" {
		msgs = append(msgs, map[string]string{
			"role":    "system",
			"content": req.SystemPrompt,
		})
	}

	for _, m := range req.Messages {
		msgs = append(msgs, map[string]string{
			"role":    m.Role,
			"content": m.Content,
		})
	}

	maxTok := req.MaxTokens
	if maxTok <= 0 {
		maxTok = 4096
	}

	return map[string]interface{}{
		"model":    apiModel,
		"messages": msgs,
		"stream":   req.Stream,
		"options": map[string]int{
			"num_predict": maxTok,
		},
	}
}
