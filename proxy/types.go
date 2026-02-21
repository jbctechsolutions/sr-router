package proxy

import (
	"encoding/json"
	"strings"
)

// AnthropicRequest is the incoming request format (Anthropic Messages API).
type AnthropicRequest struct {
	Model       string          `json:"model"`
	MaxTokens   int             `json:"max_tokens"`
	Messages    []Message       `json:"messages"`
	System      json.RawMessage `json:"system,omitempty"`
	Temperature float64         `json:"temperature,omitempty"`
	Stream      bool            `json:"stream,omitempty"`
}

// Message is a single turn in an Anthropic conversation.
type Message struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

// ExtractText extracts text content from the Anthropic content format.
// It handles both the plain-string form and the array-of-content-blocks form.
func ExtractText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}

	// Try plain string first — the most common case for simple messages.
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}

	// Try array of typed content blocks.
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &blocks); err == nil {
		var sb strings.Builder
		for _, b := range blocks {
			if b.Type == "text" {
				sb.WriteString(b.Text)
			}
		}
		return sb.String()
	}

	// Fall back to raw bytes as a string (best-effort).
	return string(raw)
}

// ExtractSystemPrompt returns the system prompt text from the request.
// The system field can be a plain string or an array of content blocks.
func ExtractSystemPrompt(raw json.RawMessage) string {
	return ExtractText(raw)
}

// AnthropicResponse is the non-streaming response format returned to clients.
type AnthropicResponse struct {
	ID           string         `json:"id"`
	Type         string         `json:"type"`
	Role         string         `json:"role"`
	Content      []ContentBlock `json:"content"`
	Model        string         `json:"model"`
	StopReason   string         `json:"stop_reason"`
	StopSequence *string        `json:"stop_sequence"`
	Usage        Usage          `json:"usage"`
}

// ContentBlock is a single typed block within an Anthropic response.
type ContentBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// Usage carries token-count information in an Anthropic response.
type Usage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

// ErrorResponse is the Anthropic-format error envelope.
type ErrorResponse struct {
	Type  string `json:"type"`
	Error struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error"`
}
