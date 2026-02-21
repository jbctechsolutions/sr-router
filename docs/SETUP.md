# Setup Guide

A step-by-step walkthrough for building, configuring, and running sr-router.

---

## Table of Contents

- [Prerequisites](#prerequisites)
- [Step 1: Clone and Build](#step-1-clone-and-build)
- [Step 2: Configure API Keys](#step-2-configure-api-keys)
- [Step 3: Verify Configuration](#step-3-verify-configuration)
- [Step 4: Test Routing (No API Calls)](#step-4-test-routing-no-api-calls)
- [Step 5: Using as Claude Code Proxy](#step-5-using-as-claude-code-proxy)
- [Step 6: Using as MCP Server](#step-6-using-as-mcp-server)
- [Step 7: Customizing Configuration](#step-7-customizing-configuration)
- [Step 8: Monitoring](#step-8-monitoring)
- [Troubleshooting](#troubleshooting)

---

## Prerequisites

Before you begin, make sure you have the following installed:

| Requirement | Version | Notes |
|-------------|---------|-------|
| **Go** | 1.22+ | [Install Go](https://go.dev/dl/) |
| **Git** | any recent | [Install Git](https://git-scm.com/) |
| **Ollama** (optional) | any recent | For free local models. [Install Ollama](https://ollama.ai/) |

> **CGO note**: sr-router uses SQLite for telemetry, which requires CGO. On macOS, CGO is enabled by default. On Linux, you may need to set `CGO_ENABLED=1` and install a C compiler (`gcc` or `musl-gcc`).

---

## Step 1: Clone and Build

Clone the repository and build the binary:

```bash
git clone https://github.com/jbctechsolutions/sr-router.git
cd sr-router
go build -o sr-router ./cmd/
```

Verify the build succeeded:

```bash
./sr-router --help
```

You should see a list of available commands (`route`, `classify`, `models`, `proxy`, `mcp`, `stats`, `feedback`, `config`).

### Optional: Install to PATH

If you want the `sr-router` command available system-wide:

```bash
go install ./cmd/
```

This places the binary in `$GOPATH/bin` (or `$HOME/go/bin` by default). Make sure that directory is in your `PATH`.

---

## Step 2: Configure API Keys

sr-router supports four providers. You only need to configure the providers you want to use. At a minimum, set up **Anthropic** for the premium tier.

### Anthropic (required for premium tier)

Anthropic powers `claude-opus` and `claude-sonnet`, the premium-tier models used for interactive work, code generation, and complex reasoning.

1. Sign up or log in at [console.anthropic.com](https://console.anthropic.com/).
2. Navigate to **API Keys** and create a new key.
3. Export it in your shell:

```bash
export ANTHROPIC_API_KEY=sk-ant-...
```

Add this to your shell profile (`~/.zshrc`, `~/.bashrc`, etc.) to persist across sessions.

### MiniMax (optional, for budget tier)

MiniMax provides `minimax-m2`, a low-cost model good for bulk text processing, summarization, and data extraction.

1. Sign up at [minimax.io](https://www.minimax.io/).
2. Generate an API key from your dashboard.
3. Export it:

```bash
export MINIMAX_API_KEY=your-key-here
```

### Cerebras (optional, for speed tier)

Cerebras provides `cerebras-glm`, an ultra-fast model used for context compaction and compression tasks.

1. Sign up at [cerebras.ai](https://www.cerebras.ai/).
2. Generate an API key from your dashboard.
3. Export it:

```bash
export CEREBRAS_API_KEY=your-key-here
```

### Ollama (optional, free tier)

Ollama runs models locally on your machine at zero cost. sr-router uses `llama3.2` and `codellama` from Ollama as free-tier fallbacks.

**Install Ollama:**

```bash
# macOS (via Homebrew)
brew install ollama

# Linux
curl -fsSL https://ollama.ai/install.sh | sh
```

**Pull the models sr-router uses:**

```bash
ollama pull llama3.2
ollama pull codellama
```

**Start Ollama** (if it is not already running):

```bash
ollama serve
```

**Verify Ollama is accessible:**

```bash
curl http://localhost:11434/api/tags
```

You should see a JSON response listing the models you pulled. No API key is needed -- Ollama runs entirely on your local machine.

---

## Step 3: Verify Configuration

sr-router ships with a default configuration in the `config/` directory at the root of the repository. Validate that everything loads correctly:

```bash
sr-router config validate
```

Expected output:

```
Config is valid!
```

You can also check which config directory sr-router is using:

```bash
sr-router config init
```

And list all configured models:

```bash
sr-router models
```

This displays every model with its provider, cost per 1k tokens, quality ceiling, and strengths.

> **Config resolution order**: sr-router looks for configuration in this order:
> 1. The path passed via `--config <dir>`
> 2. `./config` (relative to your current working directory)
> 3. `~/.config/sr-router/config`

---

## Step 4: Test Routing (No API Calls)

The `route` and `classify` commands only perform local classification and scoring. They do **not** make any API calls, so you can test them freely without burning tokens or credits.

**Route a prompt** (classifies and selects the best model):

```bash
sr-router route "Write a Go function for rate limiting"
```

Example output:

```
Route Class:  interactive
Task Type:    code
Tier:         premium
Model:        claude-sonnet
Score:        0.87
Est. Cost:    $0.0150/1k tokens
Reasoning:    code task; claude-sonnet has required strength [code] at lower cost
```

**Force the background route class** (simulates a batch/cron scenario):

```bash
sr-router route --background "Summarize these 50 files"
```

**Classify without routing** (shows classification details only):

```bash
sr-router classify "Design a microservice architecture"
```

Example output:

```
Route Class:       interactive
Task Type:         architecture
Tier:              premium
Min Quality:       0.90
Latency Budget:    30000ms
Confidence:        0.85
Required Strengths: architecture, complex_reasoning
```

Try different prompts to see how the classifier and router respond. This is a good way to verify that task patterns and route classes are working as expected before connecting real API traffic.

---

## Step 5: Using as Claude Code Proxy

The proxy mode is the primary integration point for Claude Code. It acts as a transparent HTTP proxy that speaks the Anthropic API protocol, so Claude Code does not know sr-router is in the middle.

### How it works

1. You start sr-router in proxy mode on a local port.
2. You point Claude Code at that port instead of the real Anthropic API.
3. When Claude Code sends a request, sr-router intercepts it, classifies the prompt, picks the best model, and forwards the request to the appropriate provider.
4. The response (including SSE streaming) is proxied back to Claude Code transparently.

For example, if sr-router classifies a prompt as a simple summarization task, it may route the request to `minimax-m2` instead of `claude-opus`, saving you significant API costs -- all without Claude Code being aware of the switch.

### Start the proxy

```bash
sr-router proxy --port 8889
```

The proxy will start listening on `http://localhost:8889`.

### Connect Claude Code

In a **separate terminal**, launch Claude Code and point it at the proxy:

```bash
ANTHROPIC_API_BASE=http://localhost:8889 claude
```

That is it. Claude Code now sends all its API requests through sr-router. You can use Claude Code exactly as you normally would -- the routing happens automatically behind the scenes.

---

## Step 6: Using as MCP Server

As an alternative to proxy mode, sr-router can run as an MCP (Model Context Protocol) server. This integrates directly with Claude Code, Cursor, or any MCP-compatible client via stdio transport.

### Add to Claude Code MCP configuration

Add sr-router to your Claude Code settings. Edit `~/.claude/settings.json` or create a project-local `.claude/settings.local.json`:

```json
{
  "mcpServers": {
    "sr-router": {
      "command": "/path/to/sr-router",
      "args": ["mcp", "--config", "/path/to/sr-router/config"]
    }
  }
}
```

Replace `/path/to/sr-router` with the absolute path to your built binary (e.g., the output of `which sr-router` if you ran `go install`), and `/path/to/sr-router/config` with the absolute path to the `config/` directory.

### Available MCP tools

Once connected, sr-router exposes four tools to the MCP client:

| Tool | Description |
|------|-------------|
| `route` | Classify a prompt and return the best model, score, cost, and reasoning. |
| `classify` | Classify a prompt and return the route class, task type, tier, and required strengths. |
| `models` | List all configured models with their providers, costs, and strengths. |
| `stats` | Show routing statistics (total requests, total cost, failover count, breakdown by model and tier). |

These tools allow the MCP client to query sr-router's routing logic on demand.

---

## Step 7: Customizing Configuration

sr-router is fully config-driven via three YAML files in the `config/` directory. You can customize every aspect of how requests are classified and routed.

### Adding a new model

To add a new model, append an entry to the `models` section in `config/models.yaml`:

```yaml
models:
  # ... existing models ...

  my-new-model:
    provider: openai_compat         # Provider type: anthropic, openai_compat, or ollama
    api_model: "model-name-v1"      # The model identifier used in API calls
    base_url: "https://api.example.com/v1"  # Required for openai_compat and ollama
    strengths:                      # What this model is good at (used for task matching)
      - summarization
      - simple_code
      - bulk_text
    weaknesses:                     # What this model is bad at (informational)
      - complex_reasoning
      - architecture
    cost_per_1k_tokens: 0.001       # Cost in USD per 1,000 tokens
    avg_latency_ms: 1500            # Average response latency in milliseconds
    quality_ceiling: 0.75           # Maximum quality score (0.0 to 1.0)
    max_context: 64000              # Maximum context window in tokens
    prompt_suffix: null             # Optional text appended to every prompt sent to this model
```

Then add the model name to the appropriate tier(s) in the `tiers` section and optionally to a `failover` chain.

### Adjusting routing weights

The routing formula is:

```
score = (cost_weight * cost_score) + (quality_weight * quality_score)
```

The defaults are in `config/models.yaml`:

```yaml
defaults:
  cost_weight: 0.4
  quality_weight: 0.6
```

- **Increase `cost_weight`** to prefer cheaper models more aggressively. For example, `cost_weight: 0.7` and `quality_weight: 0.3` will heavily favor low-cost models.
- **Increase `quality_weight`** to prefer higher-quality models. For example, `cost_weight: 0.2` and `quality_weight: 0.8` will route most requests to premium models.

The weights should sum to 1.0 but this is not strictly enforced.

### Adding custom task types

To add a new task type, append an entry to `config/tasks.yaml`:

```yaml
tasks:
  # ... existing tasks ...

  documentation:
    patterns:
      - "write.*docs"
      - "document"
      - "README"
      - "API.*reference"
      - "changelog"
    required_strengths: [bulk_text]
    min_quality: 0.65
```

Each task type has:

| Field | Description |
|-------|-------------|
| `patterns` | A list of regex patterns matched against the prompt text. If any pattern matches, the task type is selected. |
| `required_strengths` | Model strengths required to handle this task type. Only models listing these strengths are eligible. |
| `min_quality` | Minimum quality ceiling a model must have to be considered for this task type. |

### Creating a custom route class

Route classes control how requests are categorized at the highest level. To add a new class, append to `config/route_classes.yaml`:

```yaml
route_classes:
  # ... existing classes ...

  ci_pipeline:
    description: "Automated CI/CD pipeline tasks"
    detection:
      env: ["CI=true", "GITHUB_ACTIONS=true"]
      headers: ["x-request-type: ci"]
    default_tier: budget
    latency_budget_ms: 60000
    quality_floor: 0.55
```

Detection rules can match on:

| Rule | Description |
|------|-------------|
| `stdin` | Whether the request comes from an interactive terminal (`true`/`false`). |
| `flags` | CLI flags that force this route class (e.g., `--background`). |
| `headers` | HTTP headers that trigger this class (e.g., `x-request-type: ci`). |
| `env` | Environment variables that trigger this class (e.g., `CI=true`). |
| `content_patterns` | Regex patterns matched against the prompt content. |
| `system_prompt_patterns` | Regex patterns matched against the system prompt. |

---

## Step 8: Monitoring

sr-router logs every routing decision to a local SQLite database for analysis.

### View routing statistics

```bash
# Overall stats
sr-router stats

# Filter by a specific model
sr-router stats --model claude-sonnet
```

Example output:

```
Total Requests: 142
Total Cost:     $0.034200
Failovers:      3

By Model:
  cerebras-glm                   12
  claude-opus                    8
  claude-sonnet                  67
  minimax-m2                     41
  ollama/codellama               6
  ollama/llama3.2                8

By Tier:
  budget              41
  free                14
  premium             75
  speed               12
```

### Leave feedback on a routing decision

If sr-router picked the wrong model for a task, you can record feedback to help tune future routing:

```bash
sr-router feedback <event-id> --rating 4
```

Ratings are on a 1-5 scale. You can also suggest which model should have been used:

```bash
sr-router feedback <event-id> --rating 2 --override claude-opus
```

---

## Troubleshooting

### "config validation failed"

- Check your YAML files for syntax errors (indentation, missing colons, unclosed quotes).
- Verify the config directory path: run `sr-router config init` to see which directory is being used.
- Make sure all three files exist: `models.yaml`, `tasks.yaml`, and `route_classes.yaml`.

### "all models in X chain exhausted"

- Check that the API keys for the relevant providers are set and valid.
- Verify the provider is reachable (e.g., `curl https://api.anthropic.com/v1/messages` returns a response, even if it is an auth error).
- For Ollama models, confirm Ollama is running: `curl http://localhost:11434/api/tags`.

### CGO_ENABLED errors

sr-router depends on `go-sqlite3`, which requires CGO. On macOS, CGO is enabled by default. On Linux:

```bash
CGO_ENABLED=1 go build -o sr-router ./cmd/
```

You may also need to install `gcc` or `musl-dev` if your system does not have a C compiler.

### Proxy not routing to the expected model

Use the `route` command to debug how sr-router classifies and scores a prompt:

```bash
sr-router route "your prompt here"
```

Check the output for `Route Class`, `Task Type`, `Tier`, and `Model`. If the classification is wrong, review the patterns in `tasks.yaml` and `route_classes.yaml` to see which pattern matched (or failed to match).

### Streaming not working through the proxy

- Ensure the client is sending `"stream": true` in the request body. sr-router only enables SSE streaming when the client explicitly requests it.
- Check that the upstream provider supports streaming for the selected model.

### "unknown tier" error with `sr-router models --tier`

- Verify the tier name matches exactly what is defined in `config/models.yaml` (e.g., `premium`, `budget`, `speed`, `free`).
