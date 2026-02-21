package router

import (
	"context"
	"fmt"
	"log"
	"net/http"

	"github.com/jbctechsolutions/sr-router/config"
	"github.com/jbctechsolutions/sr-router/telemetry"
)

// FailoverEngine executes provider calls with cascading failover across the
// model chain defined for a tier. It records failover events in telemetry when
// a model other than the first in the chain ultimately handles the request.
type FailoverEngine struct {
	cfg       *config.Config
	router    *Router
	telemetry *telemetry.Collector
}

// NewFailoverEngine returns a FailoverEngine wired to the given config,
// router (for prompt suffix injection), and optional telemetry collector.
// Pass nil for tel to disable telemetry recording.
func NewFailoverEngine(cfg *config.Config, router *Router, tel *telemetry.Collector) *FailoverEngine {
	return &FailoverEngine{cfg: cfg, router: router, telemetry: tel}
}

// ExecuteWithFailover attempts each model in the tier's failover chain in
// order. It returns the first successful *http.Response together with the name
// of the model that produced it. The response body is NOT consumed — the
// caller is responsible for reading and closing it.
//
// A provider call is considered successful when the HTTP status code is in the
// 2xx range. Retryable status codes (429, 5xx) cause the engine to advance to
// the next model. Non-retryable error responses (e.g. 401, 400) are returned
// immediately so the caller can surface the original error to the client.
//
// When a network-level error occurs the engine logs it and continues to the
// next model in the chain.
//
// If all models in the chain are exhausted without a successful response,
// ExecuteWithFailover returns a non-nil error describing the tier.
func (f *FailoverEngine) ExecuteWithFailover(ctx context.Context, tier string, req ProviderRequest) (*http.Response, string, error) {
	chain := f.cfg.GetFailoverChain(tier)

	for i, modelName := range chain {
		model, ok := f.cfg.Models[modelName]
		if !ok {
			log.Printf("failover: model %q not found in config, skipping", modelName)
			continue
		}

		// Inject the model-specific prompt suffix before each attempt so that
		// each provider in the chain receives an appropriately decorated prompt.
		req.SystemPrompt = f.router.InjectSuffix(modelName, req.SystemPrompt)

		resp, err := callProvider(ctx, model, req)
		if err != nil {
			log.Printf("failover: provider call failed for %s: %v", modelName, err)
			if i < len(chain)-1 {
				log.Printf("failover: failing over from %s to %s", modelName, chain[i+1])
			}
			continue
		}

		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			// Success — record a failover event in telemetry when we did not
			// use the primary model.
			if i > 0 && f.telemetry != nil {
				if err := f.telemetry.RecordFailover("", chain[0], modelName); err != nil {
					log.Printf("failover: telemetry record error: %v", err)
				}
			}
			return resp, modelName, nil
		}

		if isRetryableStatus(resp.StatusCode) {
			resp.Body.Close()
			log.Printf("failover: %s returned %d, trying next in chain", modelName, resp.StatusCode)
			if i < len(chain)-1 {
				log.Printf("failover: failing over from %s to %s", modelName, chain[i+1])
			}
			continue
		}

		// Non-retryable HTTP error (e.g. 400, 401, 403) — return it directly
		// so the caller can surface the original provider response.
		return resp, modelName, nil
	}

	return nil, "", fmt.Errorf("all models in %s chain exhausted", tier)
}

// isRetryableStatus reports whether an HTTP status code warrants trying the
// next provider in the chain. Rate-limit (429) and all server-error (5xx)
// responses are considered retryable.
func isRetryableStatus(code int) bool {
	return code == 429 || (code >= 500 && code < 600)
}
