# sr-router Design Document

**Date:** 2026-02-21
**Status:** Approved
**Target:** Alpha-ready by end of day

## Purpose

A Go binary that intelligently routes LLM requests to the cheapest model meeting quality requirements. Two modes: MCP server (stdio, for Claude Code/Cursor) and transparent HTTP proxy (drop-in for Anthropic API consumers).

## Non-Goals

- Session management, workspace orchestration, context injection
- Chat interface, file editing, code execution
- Anything from "full SkillRunner"

## Architecture

```
sr-router/
  cmd/main.go           - CLI entrypoint (cobra)
  router/
    classify.go         - Route class + task type classification
    route.go            - Model scoring + selection
    failover.go         - Cascading failover with provider calls
    prompt.go           - Model-specific prompt suffix injection
  proxy/
    server.go           - HTTP proxy (Anthropic Messages API compatible)
    stream.go           - SSE streaming for all providers
  mcp/server.go         - MCP server (stdio transport)
  config/
    config.go           - YAML loader + typed structs
    models.yaml         - Model capabilities, tiers, failover chains
    tasks.yaml          - Task type patterns + quality floors
    route_classes.yaml  - Interactive/background/compaction detection
  telemetry/collector.go - Local SQLite logging
```

## Key Decisions

1. **Skip groq-llama for alpha** - removed from speed tier failover chain
2. **SSE streaming for all providers** - Anthropic passthrough, OpenAI-compat translation, Ollama translation
3. **Config-driven routing** - 3 YAML files, no hardcoded model logic
4. **Two-layer classification** - route class (interactive/background/compaction) then task type
5. **Weighted scoring** - cost 0.4, quality 0.6, pre-filtered by tier

## Streaming Strategy

- Anthropic to Anthropic: raw SSE passthrough
- Anthropic to OpenAI-compat: translate SSE chunks to Anthropic event format
- Anthropic to Ollama: translate Ollama streaming JSON to Anthropic SSE events

## Providers (Alpha)

| Provider | Models | Auth |
|----------|--------|------|
| anthropic | claude-opus, claude-sonnet | ANTHROPIC_API_KEY |
| openai_compat | minimax-m2, cerebras-glm | MINIMAX_API_KEY, CEREBRAS_API_KEY |
| ollama | llama3.2, codellama | None (localhost) |

## Dependencies

- github.com/spf13/cobra (CLI)
- gopkg.in/yaml.v3 (config)
- github.com/mattn/go-sqlite3 (telemetry)
- github.com/google/uuid (event IDs)
- github.com/mark3labs/mcp-go (MCP server)
- golang.org/x/term (terminal detection)
