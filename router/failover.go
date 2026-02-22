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

// ExecuteWithFailover builds a failover chain from the routing decision — the
// selected model first, then alternatives by score, then remaining tier chain
// entries, and finally the global fallback. It attempts each model in order
// and returns the first successful *http.Response together with the name of
// the model that produced it. The response body is NOT consumed — the caller
// is responsible for reading and closing it.
//
// A provider call is considered successful when the HTTP status code is in the
// 2xx range. Retryable status codes (401, 403, 429, 5xx) cause the engine to
// advance to the next model. Non-retryable error responses (e.g. 400) are
// returned immediately so the caller can surface the original provider error.
//
// When a network-level error occurs the engine logs it and continues to the
// next model in the chain.
//
// If all models in the chain are exhausted without a successful response,
// ExecuteWithFailover returns a non-nil error describing the tier.
func (f *FailoverEngine) ExecuteWithFailover(ctx context.Context, decision RoutingDecision, req ProviderRequest) (*http.Response, string, error) {
	chain := f.buildChainFromDecision(decision)

	// Preserve the original raw body so each iteration patches from a clean
	// copy, avoiding accumulated model-name or suffix mutations.
	originalRawBody := req.RawAnthropicBody

	for i, modelName := range chain {
		model, ok := f.cfg.Models[modelName]
		if !ok {
			log.Printf("failover: model %q not found in config, skipping", modelName)
			continue
		}

		// Inject the model-specific prompt suffix before each attempt so that
		// each provider in the chain receives an appropriately decorated prompt.
		req.SystemPrompt = f.router.InjectSuffix(modelName, req.SystemPrompt)

		// For Anthropic providers with raw body available, patch the original
		// body with this model's API name and suffix for direct passthrough
		// (preserving tool_use, tool_result, images, etc.). Non-Anthropic
		// providers always use the normalised text path.
		if len(originalRawBody) > 0 && model.Provider == "anthropic" {
			suffix := getModelSuffix(f.cfg, modelName)
			patched, patchErr := PatchAnthropicRawBody(originalRawBody, model.APIModel, suffix)
			if patchErr != nil {
				log.Printf("failover: raw body patch failed for %s: %v, falling back to normalised", modelName, patchErr)
				req.RawAnthropicBody = nil
			} else {
				req.RawAnthropicBody = patched
			}
		} else {
			req.RawAnthropicBody = nil
		}

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

	return nil, "", fmt.Errorf("all models in %s chain exhausted", decision.Tier)
}

// buildChainFromDecision constructs the failover chain: selected model first,
// then alternatives sorted by score, then remaining models from the tier's
// static chain, and finally the global fallback. Duplicates are removed.
func (f *FailoverEngine) buildChainFromDecision(d RoutingDecision) []string {
	seen := make(map[string]bool)
	var chain []string

	add := func(name string) {
		if name != "" && !seen[name] {
			seen[name] = true
			chain = append(chain, name)
		}
	}

	// 1. Router-selected model first.
	add(d.Model)

	// 2. Alternatives from the router (already sorted by score).
	for _, alt := range d.Alternatives {
		add(alt.Model)
	}

	// 3. Remaining models from the tier's static failover chain.
	for _, m := range f.cfg.GetFailoverChain(d.Tier) {
		add(m)
	}

	// 4. Global fallback.
	add(f.cfg.Defaults.FallbackModel)

	return chain
}

// isRetryableStatus reports whether an HTTP status code warrants trying the
// next provider in the chain. Auth errors (401, 403), rate-limit (429), and
// all server-error (5xx) responses are considered retryable.
func isRetryableStatus(code int) bool {
	return code == 401 || code == 403 || code == 429 || (code >= 500 && code < 600)
}
