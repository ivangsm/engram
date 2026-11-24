#!/usr/bin/env bash
set -e

# 1. Determine active project_id
PROJECT_ID=""

if [ -n "${ENGRAM_PROJECT_ID:-}" ]; then
  PROJECT_ID="$ENGRAM_PROJECT_ID"
fi

GIT_ROOT=$(git rev-parse --show-toplevel 2>/dev/null || true)

if [ -z "$PROJECT_ID" ]; then
  CANDIDATE_JSON=""
  if [ -f ".engram.json" ]; then
    CANDIDATE_JSON=".engram.json"
  elif [ -n "$GIT_ROOT" ] && [ -f "$GIT_ROOT/.engram.json" ]; then
    CANDIDATE_JSON="$GIT_ROOT/.engram.json"
  fi

  if [ -n "$CANDIDATE_JSON" ]; then
    PROJECT_ID=$(grep -oE '"(project_id|projectId)"\s*:\s*"[^"]+"' "$CANDIDATE_JSON" 2>/dev/null | head -n1 | sed -E 's/.*"([^"]+)".*/\1/' || true)
  fi
fi

if [ -z "$PROJECT_ID" ]; then
  ORIGIN_URL=$(git config --get remote.origin.url 2>/dev/null || true)
  if [ -n "$ORIGIN_URL" ]; then
    PROJECT_ID=$(basename -s .git "$ORIGIN_URL" 2>/dev/null || true)
  fi
fi

if [ -z "$PROJECT_ID" ]; then
  if [ -n "$GIT_ROOT" ]; then
    PROJECT_ID=$(basename "$GIT_ROOT")
  else
    PROJECT_ID=$(basename "$PWD")
  fi
fi

PROJECT_ID="${PROJECT_ID:-default}"

# 2. Check Engram daemon availability
SERVER_URL="${ENGRAM_SERVER_URL:-http://127.0.0.1:8085}"
HEALTH_URL="${SERVER_URL%/}/healthz"

STATUS=0
curl -s -f -m 1 "$HEALTH_URL" >/dev/null 2>&1 || STATUS=$?

if [ $STATUS -eq 0 ]; then
  echo "[engram] State & Memory Plane active (Project: ${PROJECT_ID}). Tools available via MCP."
else
  echo "[engram] Daemon offline at :8085. Run 'docker compose up -d' or use local filesystem mode."
fi

exit 0
