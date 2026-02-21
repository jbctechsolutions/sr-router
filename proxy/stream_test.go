package proxy

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestStreamOpenAIToAnthropic verifies that OpenAI SSE chunks are correctly
// translated into Anthropic SSE event sequences.
func TestStreamOpenAIToAnthropic(t *testing.T) {
	sseData := `data: {"id":"chatcmpl-1","choices":[{"delta":{"content":"Hello"},"index":0}]}

data: {"id":"chatcmpl-1","choices":[{"delta":{"content":" world"},"index":0}]}

data: [DONE]

`
	resp := &http.Response{
		Body: io.NopCloser(strings.NewReader(sseData)),
	}

	w := httptest.NewRecorder()
	StreamOpenAIToAnthropic(w, resp, "test-id", "test-model")

	body := w.Body.String()

	tests := []struct {
		check string
		desc  string
	}{
		{"event: message_start", "message_start event"},
		{"event: content_block_start", "content_block_start event"},
		{"event: content_block_delta", "content_block_delta event"},
		{"Hello", "Hello text in delta"},
		{" world", "world text in delta"},
		{"event: content_block_stop", "content_block_stop event"},
		{"event: message_delta", "message_delta event"},
		{"event: message_stop", "message_stop event"},
		{"test-model", "model name in message_start"},
		{"test-id", "request ID in message_start"},
	}

	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			if !strings.Contains(body, tt.check) {
				t.Errorf("missing %s: %q not found in body:\n%s", tt.desc, tt.check, body)
			}
		})
	}
}

// TestStreamOpenAIToAnthropic_EmptyDelta verifies that chunks with no content
// in the delta (e.g. role-only chunks) are skipped gracefully.
func TestStreamOpenAIToAnthropic_EmptyDelta(t *testing.T) {
	sseData := `data: {"id":"chatcmpl-2","choices":[{"delta":{"role":"assistant"},"index":0}]}

data: {"id":"chatcmpl-2","choices":[{"delta":{"content":"Hi"},"index":0}]}

data: [DONE]

`
	resp := &http.Response{
		Body: io.NopCloser(strings.NewReader(sseData)),
	}

	w := httptest.NewRecorder()
	StreamOpenAIToAnthropic(w, resp, "test-id-2", "gpt-4o")

	body := w.Body.String()

	if !strings.Contains(body, "event: message_start") {
		t.Error("missing message_start event")
	}
	if !strings.Contains(body, "Hi") {
		t.Error("expected 'Hi' content in body")
	}
	if !strings.Contains(body, "event: message_stop") {
		t.Error("missing message_stop event")
	}
}

// TestStreamOpenAIToAnthropic_ContentType verifies the SSE content-type header
// is set correctly.
func TestStreamOpenAIToAnthropic_ContentType(t *testing.T) {
	sseData := "data: [DONE]\n\n"
	resp := &http.Response{
		Body: io.NopCloser(strings.NewReader(sseData)),
	}

	w := httptest.NewRecorder()
	StreamOpenAIToAnthropic(w, resp, "hdr-test", "gpt-4o")

	ct := w.Header().Get("Content-Type")
	if ct != "text/event-stream" {
		t.Errorf("expected Content-Type text/event-stream, got %q", ct)
	}
}

// TestStreamOllamaToAnthropic verifies that Ollama JSON-line chunks are
// correctly translated into Anthropic SSE event sequences.
func TestStreamOllamaToAnthropic(t *testing.T) {
	ollamaData := `{"model":"llama3.2","message":{"role":"assistant","content":"Hello"},"done":false}
{"model":"llama3.2","message":{"role":"assistant","content":" world"},"done":false}
{"model":"llama3.2","message":{"role":"assistant","content":""},"done":true,"eval_count":42}
`
	resp := &http.Response{
		Body: io.NopCloser(strings.NewReader(ollamaData)),
	}

	w := httptest.NewRecorder()
	StreamOllamaToAnthropic(w, resp, "ollama-req-id", "llama3.2")

	body := w.Body.String()

	tests := []struct {
		check string
		desc  string
	}{
		{"event: message_start", "message_start event"},
		{"event: content_block_start", "content_block_start event"},
		{"event: content_block_delta", "content_block_delta event"},
		{"Hello", "Hello text in delta"},
		{" world", "world text in delta"},
		{"event: content_block_stop", "content_block_stop event"},
		{"event: message_delta", "message_delta event"},
		{"event: message_stop", "message_stop event"},
		{"llama3.2", "model name in message_start"},
		{"ollama-req-id", "request ID in message_start"},
		{"42", "eval_count reflected in output_tokens"},
	}

	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			if !strings.Contains(body, tt.check) {
				t.Errorf("missing %s: %q not found in body:\n%s", tt.desc, tt.check, body)
			}
		})
	}
}

// TestStreamOllamaToAnthropic_ContentType verifies the SSE content-type header.
func TestStreamOllamaToAnthropic_ContentType(t *testing.T) {
	ollamaData := `{"model":"llama3.2","message":{"role":"assistant","content":""},"done":true}
`
	resp := &http.Response{
		Body: io.NopCloser(strings.NewReader(ollamaData)),
	}

	w := httptest.NewRecorder()
	StreamOllamaToAnthropic(w, resp, "hdr-ollama", "llama3.2")

	ct := w.Header().Get("Content-Type")
	if ct != "text/event-stream" {
		t.Errorf("expected Content-Type text/event-stream, got %q", ct)
	}
}

// TestStreamAnthropicPassthrough verifies that Anthropic SSE is passed through
// verbatim without transformation.
func TestStreamAnthropicPassthrough(t *testing.T) {
	sseData := `event: message_start
data: {"type":"message_start","message":{"id":"msg_abc","type":"message","role":"assistant","model":"claude-3-5-sonnet-20241022","content":[],"usage":{"input_tokens":10,"output_tokens":0}}}

event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hello"}}

event: message_stop
data: {"type":"message_stop"}

`
	resp := &http.Response{
		Body: io.NopCloser(strings.NewReader(sseData)),
	}

	w := httptest.NewRecorder()
	StreamAnthropicPassthrough(w, resp, "passthru-id")

	body := w.Body.String()

	checks := []string{
		"event: message_start",
		"event: content_block_start",
		"event: content_block_delta",
		"Hello",
		"event: message_stop",
		"msg_abc",
	}

	for _, check := range checks {
		t.Run(check, func(t *testing.T) {
			if !strings.Contains(body, check) {
				t.Errorf("%q not found in passthrough body:\n%s", check, body)
			}
		})
	}
}

// TestStreamAnthropicPassthrough_ContentType verifies SSE headers for the
// passthrough path.
func TestStreamAnthropicPassthrough_ContentType(t *testing.T) {
	resp := &http.Response{
		Body: io.NopCloser(strings.NewReader("event: message_stop\ndata: {}\n\n")),
	}

	w := httptest.NewRecorder()
	StreamAnthropicPassthrough(w, resp, "hdr-pass")

	ct := w.Header().Get("Content-Type")
	if ct != "text/event-stream" {
		t.Errorf("expected Content-Type text/event-stream, got %q", ct)
	}
}
