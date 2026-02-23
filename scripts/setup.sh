#!/bin/bash
cd "${CLAUDE_PLUGIN_ROOT}"
mkdir -p bin
if command -v go &>/dev/null; then
  go build -o bin/sr-router ./cmd/
else
  echo "Go not found. Install Go or download a pre-built binary."
  echo "See: https://github.com/jbctechsolutions/sr-router/releases"
  exit 1
fi
