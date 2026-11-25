#!/usr/bin/env bash
set -e

find_engram_json_up() {
  local dir="$1"
  [ -n "$dir" ] || return 1
  dir=$(cd "$dir" 2>/dev/null && pwd) || return 1

  while true; do
    if [ -f "$dir/.engram.json" ]; then
      printf '%s\n' "$dir/.engram.json"
      return 0
    fi
    local parent
    parent=$(dirname "$dir")
    if [ "$parent" = "$dir" ]; then
      break
    fi
    dir="$parent"
  done
  return 1
}

# 1. Determine active project_id
PROJECT_ID=""

if [ -n "${ENGRAM_PROJECT_ID:-}" ]; then
  PROJECT_ID="$ENGRAM_PROJECT_ID"
fi

GIT_ROOT=$(git rev-parse --show-toplevel 2>/dev/null || true)
GIT_COMMON_DIR=$(git rev-parse --git-common-dir 2>/dev/null || true)

if [ -n "$GIT_COMMON_DIR" ]; then
  case "$GIT_COMMON_DIR" in
    /*) ;;
    *) GIT_COMMON_DIR="$PWD/$GIT_COMMON_DIR" ;;
  esac
  GIT_COMMON_DIR=$(cd "$GIT_COMMON_DIR" 2>/dev/null && pwd || true)
  if [ -n "$GIT_COMMON_DIR" ] && [ "$(basename "$GIT_COMMON_DIR")" = ".git" ]; then
    GIT_COMMON_ROOT=$(dirname "$GIT_COMMON_DIR")
  else
    GIT_COMMON_ROOT="$GIT_COMMON_DIR"
  fi
else
  GIT_COMMON_ROOT=""
fi

if [ -z "$PROJECT_ID" ]; then
  CANDIDATE_JSON=""
  CANDIDATE_JSON=$(find_engram_json_up "$PWD" || true)
  if [ -z "$CANDIDATE_JSON" ] && [ -n "$GIT_ROOT" ]; then
    CANDIDATE_JSON=$(find_engram_json_up "$GIT_ROOT" || true)
  fi
  if [ -z "$CANDIDATE_JSON" ] && [ -n "$GIT_COMMON_ROOT" ]; then
    CANDIDATE_JSON=$(find_engram_json_up "$GIT_COMMON_ROOT" || true)
  fi

  if [ -n "$CANDIDATE_JSON" ]; then
    PROJECT_ID=$(grep -oE '"(project_id|projectId)"[[:space:]]*:[[:space:]]*"[^"]+"' "$CANDIDATE_JSON" 2>/dev/null | head -n1 | sed -E 's/.*"([^"]+)".*/\1/' || true)
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
  echo "[engram] State & Memory Plane active (Project: ${PROJECT_ID}). Tools available via CLI ('engram') and MCP."
else
  echo "[engram] Daemon offline at ${SERVER_URL}. Run 'docker compose up -d' or use local filesystem mode."
fi

exit 0
