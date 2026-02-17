#!/usr/bin/env bash
# aisync pre-commit hook — auto-capture AI session before commit.
# Managed by aisync. Do not edit the aisync section.

# ── aisync:start ──
if command -v aisync >/dev/null 2>&1; then
    aisync capture --auto 2>/dev/null || true
fi
# ── aisync:end ──
