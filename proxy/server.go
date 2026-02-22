package proxy

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/google/uuid"
	"github.com/jbctechsolutions/sr-router/config"
	"github.com/jbctechsolutions/sr-router/router"
	"github.com/jbctechsolutions/sr-router/telemetry"
)

// ProxyServer is an HTTP server that accepts Anthropic Messages API requests,
// classifies them, routes them to the best-fit model, and streams or returns
// responses in the Anthropic format.
type ProxyServer struct {
	classifier *router.Classifier
	router     *router.Router
	failover   *router.FailoverEngine
	telemetry  *telemetry.Collector
	cfg        *config.Config
	port       string
}

// NewProxyServer constructs a ProxyServer wired to the provided config. It
// initialises the classifier, router, and failover engine. Telemetry uses a
// SQLite database in the OS temp directory; if that fails, telemetry is
// disabled with a warning rather than preventing startup.
func NewProxyServer(cfg *config.Config, port string) (*ProxyServer, error) {
	classifier := router.NewClassifier(cfg)
	rtr := router.NewRouter(cfg)

	dbPath := filepath.Join(os.TempDir(), "sr-router-telemetry.db")
	tel, err := telemetry.NewCollector(dbPath)
	if err != nil {
		log.Printf("Warning: telemetry disabled: %v", err)
		tel = nil
	}

	failover := router.NewFailoverEngine(cfg, rtr, tel)

	return &ProxyServer{
		classifier: classifier,
		router:     rtr,
		failover:   failover,
		telemetry:  tel,
		cfg:        cfg,
		port:       port,
	}, nil
}

// Start registers all route handlers, wraps the mux in the logging middleware,
// and begins listening. It blocks until the server returns an error.
func (p *ProxyServer) Start() error {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/messages", p.handleMessages)
	mux.HandleFunc("/health", p.handleHealth)
	mux.HandleFunc("/healthz", p.handleHealth)
	mux.HandleFunc("/dashboard", p.handleDashboard)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			p.handleHealth(w, r)
			return
		}
		http.NotFound(w, r)
	})

	handler := loggingMiddleware(mux)

	log.Printf("sr-router proxy starting on port %s", p.port)
	log.Printf("Endpoint: http://localhost:%s/v1/messages", p.port)
	return http.ListenAndServe(":"+p.port, handler)
}

// handleMessages is the primary handler for /v1/messages. It:
//  1. Parses the incoming Anthropic Messages API request.
//  2. Classifies the prompt (route class + task type).
//  3. Routes to the best-fit model within the classified tier.
//  4. Forwards the request to the provider via the failover engine.
//  5. Translates the provider response back to Anthropic format and writes it.
func (p *ProxyServer) handleMessages(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		sendError(w, "invalid_request_error", "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// 1. Read and parse request body.
	body, err := io.ReadAll(r.Body)
	if err != nil {
		sendError(w, "invalid_request_error", "Failed to read request body", http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	var req AnthropicRequest
	if err := json.Unmarshal(body, &req); err != nil {
		sendError(w, "invalid_request_error", "Invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}

	if len(req.Messages) == 0 {
		sendError(w, "invalid_request_error", "messages is required", http.StatusBadRequest)
		return
	}

	// 2. Extract text for classification.
	systemPrompt := ExtractSystemPrompt(req.System)
	var promptText string
	for _, msg := range req.Messages {
		if msg.Role == "user" {
			promptText += ExtractText(msg.Content) + " "
		}
	}

	// 3. Collect headers that influence route-class detection.
	headers := make(map[string]string)
	if rt := r.Header.Get("x-request-type"); rt != "" {
		headers["x-request-type"] = rt
	}

	// 4. Classify.
	classification := p.classifier.Classify(promptText, headers)

	// 5. Route.
	decision := p.router.Route(classification)

	eventID := uuid.New().String()
	start := time.Now()

	log.Printf("Routing: class=%s task=%s tier=%s model=%s",
		classification.RouteClass, classification.TaskType, classification.Tier, decision.Model)

	// 6. Build the normalised provider request.
	var messages []router.ProviderMessage
	for _, msg := range req.Messages {
		messages = append(messages, router.ProviderMessage{
			Role:    msg.Role,
			Content: ExtractText(msg.Content),
		})
	}

	// Inject the model-specific prompt suffix into the system prompt.
	modifiedSystem := p.router.InjectSuffix(decision.Model, systemPrompt)

	// Capture incoming auth headers to forward to Anthropic.
	authHeader := make(http.Header)
	if auth := r.Header.Get("Authorization"); auth != "" {
		authHeader.Set("Authorization", auth)
	}
	if key := r.Header.Get("X-Api-Key"); key != "" {
		authHeader.Set("X-Api-Key", key)
	}

	provReq := router.ProviderRequest{
		SystemPrompt:        modifiedSystem,
		Messages:            messages,
		MaxTokens:           req.MaxTokens,
		Temperature:         req.Temperature,
		Stream:              req.Stream,
		RawAnthropicBody:    body,
		AnthropicAuthHeader: authHeader,
	}

	// 7. Execute with failover.
	resp, usedModel, err := p.failover.ExecuteWithFailover(r.Context(), decision.Tier, provReq)
	if err != nil {
		sendError(w, "api_error", "All providers failed: "+err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	latencyMs := int(time.Since(start).Milliseconds())

	// 8. Record telemetry (non-fatal if it fails).
	if p.telemetry != nil {
		if telErr := p.telemetry.RecordRouting(telemetry.RoutingEvent{
			ID:            eventID,
			RouteClass:    classification.RouteClass,
			TaskType:      classification.TaskType,
			Tier:          classification.Tier,
			SelectedModel: usedModel,
			LatencyMs:     latencyMs,
			EstimatedCost: decision.EstCost,
		}); telErr != nil {
			log.Printf("telemetry: failed to record routing event: %v", telErr)
		}
	}

	// 9. Determine provider type and write response.
	model := p.cfg.Models[usedModel]

	if req.Stream {
		switch model.Provider {
		case "anthropic":
			StreamAnthropicPassthrough(w, resp, eventID)
		case "openai_compat":
			StreamOpenAIToAnthropic(w, resp, eventID, usedModel)
		case "ollama":
			StreamOllamaToAnthropic(w, resp, eventID, usedModel)
		default:
			StreamAnthropicPassthrough(w, resp, eventID)
		}
		return
	}

	// Non-streaming: read full response body, translate to Anthropic format.
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		sendError(w, "api_error", "Failed to read provider response", http.StatusBadGateway)
		return
	}

	switch model.Provider {
	case "anthropic":
		w.Header().Set("Content-Type", "application/json")
		w.Write(respBody) //nolint:errcheck
	case "openai_compat":
		translateOpenAIResponseToAnthropic(w, respBody, eventID, usedModel)
	case "ollama":
		translateOllamaResponseToAnthropic(w, respBody, eventID, usedModel)
	default:
		w.Header().Set("Content-Type", "application/json")
		w.Write(respBody) //nolint:errcheck
	}
}

// translateOpenAIResponseToAnthropic converts a non-streaming OpenAI chat
// completions response into the Anthropic Messages API response format.
func translateOpenAIResponseToAnthropic(w http.ResponseWriter, body []byte, eventID string, model string) {
	var openaiResp struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
		Usage struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
		} `json:"usage"`
	}

	if err := json.Unmarshal(body, &openaiResp); err != nil || len(openaiResp.Choices) == 0 {
		sendError(w, "api_error", "Failed to parse provider response", http.StatusBadGateway)
		return
	}

	anthropicResp := AnthropicResponse{
		ID:   "msg_" + eventID[:8],
		Type: "message",
		Role: "assistant",
		Content: []ContentBlock{
			{Type: "text", Text: openaiResp.Choices[0].Message.Content},
		},
		Model:      model,
		StopReason: "end_turn",
		Usage: Usage{
			InputTokens:  openaiResp.Usage.PromptTokens,
			OutputTokens: openaiResp.Usage.CompletionTokens,
		},
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(anthropicResp) //nolint:errcheck
}

// translateOllamaResponseToAnthropic converts a non-streaming Ollama /api/chat
// response into the Anthropic Messages API response format.
func translateOllamaResponseToAnthropic(w http.ResponseWriter, body []byte, eventID string, model string) {
	var ollamaResp struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
		PromptEvalCount int `json:"prompt_eval_count"`
		EvalCount       int `json:"eval_count"`
	}

	if err := json.Unmarshal(body, &ollamaResp); err != nil {
		sendError(w, "api_error", "Failed to parse provider response", http.StatusBadGateway)
		return
	}

	anthropicResp := AnthropicResponse{
		ID:   "msg_" + eventID[:8],
		Type: "message",
		Role: "assistant",
		Content: []ContentBlock{
			{Type: "text", Text: ollamaResp.Message.Content},
		},
		Model:      model,
		StopReason: "end_turn",
		Usage: Usage{
			InputTokens:  ollamaResp.PromptEvalCount,
			OutputTokens: ollamaResp.EvalCount,
		},
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(anthropicResp) //nolint:errcheck
}

// handleHealth returns a simple JSON status payload for liveness probes.
func (p *ProxyServer) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{ //nolint:errcheck
		"status":  "ok",
		"service": "sr-router",
		"models":  len(p.cfg.Models),
	})
}

// handleDashboard returns aggregate routing statistics from telemetry.
func (p *ProxyServer) handleDashboard(w http.ResponseWriter, r *http.Request) {
	if p.telemetry == nil {
		sendError(w, "api_error", "Telemetry not available", http.StatusServiceUnavailable)
		return
	}
	stats, err := p.telemetry.GetStats("")
	if err != nil {
		sendError(w, "api_error", "Failed to get stats: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(stats) //nolint:errcheck
}

// sendError writes an Anthropic-format error response with the given HTTP status.
func sendError(w http.ResponseWriter, errorType string, message string, statusCode int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	resp := ErrorResponse{Type: "error"}
	resp.Error.Type = errorType
	resp.Error.Message = message
	json.NewEncoder(w).Encode(resp) //nolint:errcheck
}

// loggingMiddleware logs the method, path, remote address, and elapsed time
// for every request.
func loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		log.Printf("<- %s %s from %s", r.Method, r.URL.Path, r.RemoteAddr)
		next.ServeHTTP(w, r)
		log.Printf("-> %s %s completed in %v", r.Method, r.URL.Path, time.Since(start))
	})
}
