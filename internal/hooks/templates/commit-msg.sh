#!/usr/bin/env bash
# aisync commit-msg hook — append AI-Session trailer to commit message.
# Managed by aisync. Do not edit the aisync section.

# ── aisync:start ──
COMMIT_MSG_FILE="$1"

if command -v aisync >/dev/null 2>&1; then
    # Get the latest captured session ID for the current branch
    SESSION_ID=$(aisync list --quiet 2>/dev/null | head -1)
    if [ -n "$SESSION_ID" ]; then
        # Only add trailer if not already present
        if ! grep -q "^AI-Session:" "$COMMIT_MSG_FILE" 2>/dev/null; then
            # Ensure there's a blank line before the trailer
            echo "" >> "$COMMIT_MSG_FILE"
            echo "AI-Session: $SESSION_ID" >> "$COMMIT_MSG_FILE"
        fi
    fi
fi
# ── aisync:end ──
