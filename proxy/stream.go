// Package proxy provides HTTP streaming translators that read provider
// responses and emit Anthropic-format SSE events to an http.ResponseWriter.
package proxy

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// --- Anthropic SSE event types -----------------------------------------------

// messageStartEvent is the first event emitted in every streaming response.
type messageStartEvent struct {
	Type    string             `json:"type"`
	Message messageStartPayload `json:"message"`
}

type messageStartPayload struct {
	ID      string        `json:"id"`
	Type    string        `json:"type"`
	Role    string        `json:"role"`
	Model   string        `json:"model"`
	Content []interface{} `json:"content"`
	Usage   struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
}

// contentBlockStart signals the opening of a text content block.
type contentBlockStart struct {
	Type         string `json:"type"`
	Index        int    `json:"index"`
	ContentBlock struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content_block"`
}

// contentBlockDelta carries an incremental text chunk.
type contentBlockDelta struct {
	Type  string `json:"type"`
	Index int    `json:"index"`
	Delta struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"delta"`
}

// contentBlockStop signals the end of a content block.
type contentBlockStop struct {
	Type  string `json:"type"`
	Index int    `json:"index"`
}

// messageDelta carries the stop reason and final token usage.
type messageDelta struct {
	Type  string `json:"type"`
	Delta struct {
		StopReason string `json:"stop_reason"`
	} `json:"delta"`
	Usage struct {
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
}

// messageStop is the terminal event.
type messageStop struct {
	Type string `json:"type"`
}

// --- helpers -----------------------------------------------------------------

// sseHeaders sets the standard headers required for Server-Sent Events.
// Headers must be written before the first call to Write or Flush.
func sseHeaders(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
}

// writeSSEEvent serialises data as JSON and writes a single SSE event frame,
// then flushes the connection.
func writeSSEEvent(w http.ResponseWriter, flusher http.Flusher, event string, data interface{}) {
	b, err := json.Marshal(data)
	if err != nil {
		// Best-effort: emit an error comment so the client can see it.
		fmt.Fprintf(w, ": marshal error: %v\n\n", err)
		flusher.Flush()
		return
	}
	fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, b)
	flusher.Flush()
}

// buildMessageStart constructs the opening event payload.
func buildMessageStart(id, model string) messageStartEvent {
	payload := messageStartPayload{
		ID:      id,
		Type:    "message",
		Role:    "assistant",
		Model:   model,
		Content: []interface{}{},
	}
	return messageStartEvent{Type: "message_start", Message: payload}
}

// buildContentBlockStart constructs the content block opening event.
func buildContentBlockStart() contentBlockStart {
	cbs := contentBlockStart{
		Type:  "content_block_start",
		Index: 0,
	}
	cbs.ContentBlock.Type = "text"
	cbs.ContentBlock.Text = ""
	return cbs
}

// buildContentBlockDelta constructs a delta event for the given text chunk.
func buildContentBlockDelta(text string) contentBlockDelta {
	cbd := contentBlockDelta{
		Type:  "content_block_delta",
		Index: 0,
	}
	cbd.Delta.Type = "text_delta"
	cbd.Delta.Text = text
	return cbd
}

// buildContentBlockStop constructs the content block closing event.
func buildContentBlockStop() contentBlockStop {
	return contentBlockStop{Type: "content_block_stop", Index: 0}
}

// buildMessageDelta constructs the stop-reason event with token counts.
func buildMessageDelta(stopReason string, outputTokens int) messageDelta {
	md := messageDelta{Type: "message_delta"}
	md.Delta.StopReason = stopReason
	md.Usage.OutputTokens = outputTokens
	return md
}

// buildMessageStop constructs the terminal event.
func buildMessageStop() messageStop {
	return messageStop{Type: "message_stop"}
}

// emitPreamble writes message_start and content_block_start then flushes.
func emitPreamble(w http.ResponseWriter, f http.Flusher, requestID, model string) {
	writeSSEEvent(w, f, "message_start", buildMessageStart(requestID, model))
	writeSSEEvent(w, f, "content_block_start", buildContentBlockStart())
}

// emitEpilogue writes content_block_stop, message_delta, and message_stop.
func emitEpilogue(w http.ResponseWriter, f http.Flusher, outputTokens int) {
	writeSSEEvent(w, f, "content_block_stop", buildContentBlockStop())
	writeSSEEvent(w, f, "message_delta", buildMessageDelta("end_turn", outputTokens))
	writeSSEEvent(w, f, "message_stop", buildMessageStop())
}

// --- OpenAI SSE types --------------------------------------------------------

// openAIChunk is the minimal representation of an OpenAI streaming chunk.
type openAIChunk struct {
	Choices []struct {
		Delta struct {
			Content string `json:"content"`
		} `json:"delta"`
		Index int `json:"index"`
	} `json:"choices"`
}

// --- Ollama streaming types --------------------------------------------------

// ollamaChunk is one JSON line from an Ollama /api/chat streaming response.
type ollamaChunk struct {
	Message struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	} `json:"message"`
	Done      bool `json:"done"`
	EvalCount int  `json:"eval_count"`
}

// --- Public streaming translators --------------------------------------------

// StreamAnthropicPassthrough copies Anthropic SSE from resp.Body directly to
// w, preserving all event lines verbatim. It only flushes on data lines to
// keep the output latency low.
//
// This is used when the upstream provider is Anthropic itself — no translation
// is needed.
func StreamAnthropicPassthrough(w http.ResponseWriter, resp *http.Response, _ string) {
	sseHeaders(w)

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	defer resp.Body.Close()

	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		line := scanner.Text()
		fmt.Fprintf(w, "%s\n", line)
		// Flush after every data line so the client receives it immediately.
		if strings.HasPrefix(line, "data:") {
			flusher.Flush()
		}
	}
	// Final flush to ensure any trailing blank lines are sent.
	flusher.Flush()
}

// StreamOpenAIToAnthropic reads OpenAI-format SSE from resp.Body and translates
// each chunk into Anthropic SSE events written to w.
//
// The translation emits:
//  1. message_start  — once at the start
//  2. content_block_start — once at the start
//  3. content_block_delta — once per OpenAI chunk that contains text
//  4. content_block_stop, message_delta, message_stop — once at [DONE]
func StreamOpenAIToAnthropic(w http.ResponseWriter, resp *http.Response, requestID string, model string) {
	sseHeaders(w)

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	defer resp.Body.Close()

	emitPreamble(w, flusher, requestID, model)

	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		line := scanner.Text()

		// Skip blank lines and non-data lines.
		if !strings.HasPrefix(line, "data:") {
			continue
		}

		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))

		if payload == "[DONE]" {
			break
		}

		var chunk openAIChunk
		if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
			// Malformed chunk: skip it but keep scanning.
			continue
		}

		for _, choice := range chunk.Choices {
			if choice.Delta.Content == "" {
				continue
			}
			writeSSEEvent(w, flusher, "content_block_delta",
				buildContentBlockDelta(choice.Delta.Content))
		}
	}

	emitEpilogue(w, flusher, 0)
}

// StreamOllamaToAnthropic reads Ollama streaming JSON lines from resp.Body and
// translates each line into Anthropic SSE events written to w.
//
// Ollama streams newline-delimited JSON objects (not SSE). Each line is
// unmarshalled and translated. The final line (done == true) carries token
// counts that are forwarded in the message_delta event.
func StreamOllamaToAnthropic(w http.ResponseWriter, resp *http.Response, requestID string, model string) {
	sseHeaders(w)

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	defer resp.Body.Close()

	emitPreamble(w, flusher, requestID, model)

	outputTokens := 0

	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}

		var chunk ollamaChunk
		if err := json.Unmarshal([]byte(line), &chunk); err != nil {
			continue
		}

		if chunk.Done {
			// The done chunk carries the final eval_count (output tokens).
			outputTokens = chunk.EvalCount
			break
		}

		if chunk.Message.Content != "" {
			writeSSEEvent(w, flusher, "content_block_delta",
				buildContentBlockDelta(chunk.Message.Content))
		}
	}

	emitEpilogue(w, flusher, outputTokens)
}
