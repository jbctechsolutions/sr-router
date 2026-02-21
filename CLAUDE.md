# sr-router

Intelligent LLM request router. Routes requests to cheapest model meeting quality requirements.

## Build & Test
- Build: `go build -o sr-router ./cmd/`
- Test: `go test ./...`
- Lint: `go vet ./...`

## Architecture
- `cmd/` - CLI entrypoint (cobra)
- `config/` - YAML config loader + 3 YAML files
- `router/` - classify, route, failover, prompt injection
- `proxy/` - HTTP proxy with SSE streaming
- `mcp/` - MCP server (stdio)
- `telemetry/` - SQLite event logging

## Key Patterns
- Config-driven: models.yaml, tasks.yaml, route_classes.yaml
- Two-layer classification: route class then task type
- Weighted scoring: cost_weight * cost_score + quality_weight * quality_score
- Failover chains per tier
