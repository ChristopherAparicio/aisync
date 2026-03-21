#!/bin/bash
# aisync hook for Claude Code — multi-event handler
#
# Reads the hook event from stdin JSON and dispatches accordingly.
# Supports: Stop, PostToolUse, PostToolUseFailure, UserPromptSubmit, PreCompact, SubagentStop
#
# Install: see README.md

CAPTURE_MODE="${AISYNC_CAPTURE_MODE:-compact}"
DEBUG="${AISYNC_PLUGIN_DEBUG:-}"
STATS_FILE="${HOME}/.aisync/claude-code-stats.json"

log() {
  [ -n "$DEBUG" ] && echo "[aisync] $*" >&2
}

# Find aisync binary
AISYNC=""
for bin in aisync /tmp/aisync-bin /usr/local/bin/aisync "$HOME/.local/bin/aisync" "$HOME/go/bin/aisync"; do
  if command -v "$bin" &>/dev/null || [ -x "$bin" ]; then
    AISYNC="$bin"
    break
  fi
done

if [ -z "$AISYNC" ]; then
  log "aisync not found"
  exit 0
fi

# Read JSON input from stdin
INPUT=$(cat)
EVENT=$(echo "$INPUT" | jq -r '.hook_event_name // empty' 2>/dev/null)
SESSION_ID=$(echo "$INPUT" | jq -r '.session_id // empty' 2>/dev/null)

log "event=$EVENT session=$SESSION_ID"

# Initialize stats file if needed
init_stats() {
  if [ ! -f "$STATS_FILE" ] || [ ! -s "$STATS_FILE" ]; then
    mkdir -p "$(dirname "$STATS_FILE")"
    echo '{"messages":0,"tool_calls":0,"tool_errors":0,"captures":0}' > "$STATS_FILE"
  fi
}

# Increment a counter in the stats file
inc_stat() {
  local key="$1"
  init_stats
  if command -v jq &>/dev/null; then
    local val=$(jq -r ".$key // 0" "$STATS_FILE" 2>/dev/null)
    val=$((val + 1))
    local tmp=$(mktemp)
    jq ".$key = $val" "$STATS_FILE" > "$tmp" 2>/dev/null && mv "$tmp" "$STATS_FILE"
  fi
}

case "$EVENT" in
  Stop|SubagentStop)
    # Agent finished — capture the full session
    log "capturing session (reason=$EVENT)"
    inc_stat "captures"
    $AISYNC capture --provider claude-code --mode "$CAPTURE_MODE" --auto &>/dev/null &
    ;;

  PostToolUse)
    # Tool call completed — track it
    TOOL_NAME=$(echo "$INPUT" | jq -r '.tool_name // empty' 2>/dev/null)
    log "tool completed: $TOOL_NAME"
    inc_stat "tool_calls"
    ;;

  PostToolUseFailure)
    # Tool call failed — track error
    TOOL_NAME=$(echo "$INPUT" | jq -r '.tool_name // empty' 2>/dev/null)
    log "tool FAILED: $TOOL_NAME"
    inc_stat "tool_errors"
    ;;

  UserPromptSubmit)
    # User sent a message — track it
    log "user message submitted"
    inc_stat "messages"
    ;;

  PreCompact)
    # About to compact — capture before losing history
    log "pre-compact: capturing before compaction"
    inc_stat "captures"
    $AISYNC capture --provider claude-code --mode "$CAPTURE_MODE" --auto &>/dev/null &
    ;;

  *)
    log "unhandled event: $EVENT"
    ;;
esac

exit 0
