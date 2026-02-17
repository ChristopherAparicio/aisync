#!/usr/bin/env bash
# aisync post-checkout hook — notify if a session is available for the new branch.
# Managed by aisync. Do not edit the aisync section.

# ── aisync:start ──
# $1 = previous HEAD, $2 = new HEAD, $3 = 1 if branch checkout (0 = file checkout)
BRANCH_CHECKOUT="$3"

if [ "$BRANCH_CHECKOUT" = "1" ] && command -v aisync >/dev/null 2>&1; then
    SESSION_ID=$(aisync list --quiet 2>/dev/null | head -1)
    if [ -n "$SESSION_ID" ]; then
        echo "[aisync] AI session available for this branch. Run 'aisync restore' to load context."
    fi
fi
# ── aisync:end ──
