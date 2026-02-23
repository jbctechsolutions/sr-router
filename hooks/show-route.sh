#!/bin/bash
INPUT=$(cat)
PROMPT=$(echo "$INPUT" | jq -r '.prompt // empty')
[ -z "$PROMPT" ] && exit 0

SR="${CLAUDE_PLUGIN_ROOT}/bin/sr-router"
[ ! -x "$SR" ] && exit 0

RESULT=$("$SR" route --json --config "${CLAUDE_PLUGIN_ROOT}/config" "$PROMPT" 2>/dev/null)
[ -z "$RESULT" ] && exit 0

MODEL=$(echo "$RESULT" | jq -r '.model')
TIER=$(echo "$RESULT" | jq -r '.tier')
TASK=$(echo "$RESULT" | jq -r '.task')

echo "{\"hookSpecificOutput\":{\"hookEventName\":\"UserPromptSubmit\",\"additionalContext\":\"[sr-router] model=$MODEL tier=$TIER task=$TASK\"}}"
exit 0
