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

	// RawAnthropicBody, when non-nil, is the original Anthropic API request
	// body. For Anthropic-provider targets this is forwarded directly —
	// preserving tool_use, tool_result, images, thinking blocks, etc. — with
	// only the model name and system-prompt suffix patched.
	RawAnthropicBody []byte
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
// model.Provider. When RawAnthropicBody is set and the target is an Anthropic
// provider, the raw body is forwarded directly (preserving rich content).
// The returned *http.Response body is NOT consumed — the caller is responsible
// for reading and closing it.
func callProvider(ctx context.Context, model config.Model, req ProviderRequest) (*http.Response, error) {
	switch model.Provider {
	case "anthropic":
		if len(req.RawAnthropicBody) > 0 {
			return callAnthropicRaw(ctx, model, req.RawAnthropicBody)
		}
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

// callAnthropicRaw sends a pre-built JSON body to the Anthropic Messages API.
// The body is forwarded as-is — the caller is responsible for patching the
// model name and injecting any prompt suffix before calling this function.
func callAnthropicRaw(ctx context.Context, model config.Model, patchedBody []byte) (*http.Response, error) {
	endpoint := "https://api.anthropic.com/v1/messages"

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(patchedBody))
	if err != nil {
		return nil, fmt.Errorf("creating anthropic raw request: %w", err)
	}

	apiKey := resolveAPIKey("anthropic", "")
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-api-key", apiKey)
	httpReq.Header.Set("anthropic-version", "2023-06-01")

	return http.DefaultClient.Do(httpReq)
}

// PatchAnthropicRawBody takes an original Anthropic API request body and
// returns a copy with the "model" field set to apiModel and the optional
// suffix appended to the "system" field. All other fields (messages with
// tool_use, tool_result, images, thinking blocks, etc.) are preserved
// byte-for-byte.
func PatchAnthropicRawBody(rawBody []byte, apiModel string, suffix string) ([]byte, error) {
	var body map[string]json.RawMessage
	if err := json.Unmarshal(rawBody, &body); err != nil {
		return nil, fmt.Errorf("unmarshalling raw body: %w", err)
	}

	// Patch the model field.
	modelJSON, err := json.Marshal(apiModel)
	if err != nil {
		return nil, fmt.Errorf("marshalling api_model: %w", err)
	}
	body["model"] = modelJSON

	// Inject suffix into system if needed.
	if suffix != "" {
		if existing, ok := body["system"]; ok {
			// Try as plain string.
			var s string
			if err := json.Unmarshal(existing, &s); err == nil {
				if s == "" {
					s = suffix
				} else {
					s += "\n\n" + suffix
				}
				patched, _ := json.Marshal(s)
				body["system"] = patched
			} else {
				// Try as array of content blocks.
				var blocks []json.RawMessage
				if err := json.Unmarshal(existing, &blocks); err == nil {
					newBlock, _ := json.Marshal(map[string]string{
						"type": "text",
						"text": "\n\n" + suffix,
					})
					blocks = append(blocks, newBlock)
					patched, _ := json.Marshal(blocks)
					body["system"] = patched
				}
			}
		} else {
			// No system field — add it as a plain string.
			patched, _ := json.Marshal(suffix)
			body["system"] = patched
		}
	}

	return json.Marshal(body)
}

// getModelSuffix returns the trimmed prompt suffix for a model, or "" if none.
func getModelSuffix(cfg *config.Config, modelName string) string {
	m, ok := cfg.Models[modelName]
	if !ok || m.PromptSuffix == nil {
		return ""
	}
	return strings.TrimSpace(*m.PromptSuffix)
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
