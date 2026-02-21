# sr-router

Pick the right brain for every job. Stop burning API credits.

---

sr-router intelligently routes LLM requests to the cheapest model that meets quality requirements. Use it as a transparent HTTP proxy (drop-in for the Anthropic API) or as an MCP server.

## Quick Start

```bash
# Install
go install github.com/jbctechsolutions/sr-router/cmd@latest

# Route a prompt
sr-router route "Write a Go function for rate limiting"

# Start the proxy (point Claude Code at it)
sr-router proxy --port 8889
```

## How It Works

```
                                        +-------------------+
                                        |    Provider APIs   |
                                        |  (Anthropic, etc.) |
                                        +---------+---------+
                                                  ^
                                                  |
Request --> Classify --> Score Models --> Select --> Failover Chain --> Provider --> Response
            |               |              |
            v               v              v
       Route Class     Weighted        Best model
       + Task Type     formula         for the job
```

sr-router uses a **two-layer classification** system to pick the right model:

1. **Route class** -- Is this interactive, background, or compaction? Determined from HTTP headers, content patterns, CLI flags, and environment variables. Each class has its own tier, latency budget, and quality floor.

2. **Task type** -- What kind of work is this? Code generation, architecture design, summarization, data extraction, etc. Detected via regex pattern matching against the prompt. Each task type specifies required model strengths and a minimum quality threshold.

3. **Model scoring** -- Every eligible model is scored with a weighted formula:
   ```
   score = (cost_weight * cost_score) + (quality_weight * quality_score)
   ```
   Default weights: cost 40%, quality 60%. Models are filtered by tier membership and required strengths before scoring.

4. **Failover** -- If the primary model fails (429 rate limit, 5xx server error, or timeout), sr-router cascades to the next model in the tier's failover chain automatically.

## Modes

### Transparent Proxy

Drop-in replacement for the Anthropic API. Point any client that speaks the Anthropic HTTP protocol at sr-router and it will classify, route, and proxy the request transparently -- including SSE streaming.

```bash
sr-router proxy --port 8889

# Then point Claude Code at it:
ANTHROPIC_API_BASE=http://localhost:8889 claude
```

### MCP Server

Run sr-router as an MCP server over stdio for use with Claude Code, Cursor, or any MCP-compatible client. Exposes `route`, `classify`, `models`, and `stats` as MCP tools.

```bash
sr-router mcp
```

## Cost Savings

| Task | Without sr-router | With sr-router | Savings |
|------|-------------------|----------------|---------|
| Architecture review | claude-opus ($0.075/1k) | claude-opus ($0.075/1k) | 0% (quality required) |
| Code generation | claude-opus ($0.075/1k) | claude-sonnet ($0.015/1k) | 80% |
| Batch summarization | claude-opus ($0.075/1k) | minimax-m2 ($0.0003/1k) | 99.6% |
| Context compaction | claude-opus ($0.075/1k) | cerebras-glm ($0.0006/1k) | 99.2% |
| Simple code / tests | claude-opus ($0.075/1k) | ollama/codellama ($0.00/1k) | 100% |

The router never downgrades quality for tasks that need it -- architecture reviews still go to Opus. But summarization, boilerplate, and batch work get routed to models that cost a fraction of a cent.

## CLI Reference

| Command | Description | Example |
|---------|-------------|---------|
| `route <prompt>` | Classify and route a prompt to the best model | `sr-router route "Write a Go function for rate limiting"` |
| `classify <prompt>` | Classify a prompt without routing | `sr-router classify "Summarize this document"` |
| `models` | List all configured models | `sr-router models --tier premium` |
| `proxy` | Start the transparent HTTP proxy | `sr-router proxy --port 8889` |
| `mcp` | Start the MCP server (stdio) | `sr-router mcp` |
| `stats` | Show routing statistics from telemetry | `sr-router stats --model claude-sonnet` |
| `feedback <id>` | Record feedback for a routing event | `sr-router feedback abc123 --rating 5` |
| `config validate` | Validate YAML configuration files | `sr-router config validate` |
| `config init` | Show the resolved config directory | `sr-router config init` |

### Global Flags

| Flag | Description |
|------|-------------|
| `--config <dir>` | Override the config directory (default: `./config`, then `~/.config/sr-router/config`) |

### Route Flags

| Flag | Description |
|------|-------------|
| `--background` | Force the background route class |
| `--interactive` | Force the interactive route class |

## Configuration

sr-router is fully config-driven via three YAML files in the `config/` directory:

| File | Purpose |
|------|---------|
| `models.yaml` | Model definitions (provider, cost, quality, strengths), tier groupings, failover chains, and scoring weights |
| `tasks.yaml` | Task type definitions with regex patterns, required model strengths, and minimum quality thresholds |
| `route_classes.yaml` | Route class definitions (interactive, background, compaction) with detection rules and quality floors |

See the [`config/`](config/) directory for the full configuration files with inline comments.

## Environment Variables

```bash
# Anthropic (required for claude-opus, claude-sonnet)
export ANTHROPIC_API_KEY=sk-ant-...

# MiniMax (required for minimax-m2)
export MINIMAX_API_KEY=...

# Cerebras (required for cerebras-glm)
export CEREBRAS_API_KEY=...

# Ollama: no key needed (runs locally on http://localhost:11434)
```

## Alpha Status

This is an **alpha** build. It works, routes requests, and saves money -- but there are known limitations:

- **Telemetry is local SQLite only** -- no remote export, no dashboards beyond `sr-router stats`
- **No auth on the proxy endpoint** -- anyone who can reach the port can route requests
- **No rate limiting on the proxy itself** -- the proxy relies on upstream provider rate limits
- **Classification is regex-based** -- no LLM-in-the-loop classification yet; complex edge cases may misclassify

## License

See [LICENSE](LICENSE) for details.
