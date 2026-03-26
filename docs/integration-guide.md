# Integration Guide: aisync as a Monitoring Agent

Deploy aisync as a local agent on your dev machines to collect AI session
metrics and forward them to a central server. No custom plugins needed —
the CLI does everything.

---

## Architecture

```
Dev Machine 1 (VPC)              Dev Machine 2 (VPC)
┌──────────────────┐             ┌──────────────────┐
│ Claude Code      │             │ OpenCode         │
│   └─ hook ───────┤             │   └─ plugin ─────┤
│                  │             │                  │
│ OpenCode         │             │ Cursor           │
│   └─ plugin ─────┤             │   └─ capture ────┤
│                  │             │                  │
│ aisync CLI       │  HTTPS +    │ aisync CLI       │
│ (agent mode)     ├─ API key ──>│ (agent mode)     │
└──────────┬───────┘             └──────────┬───────┘
           │                                │
           │  POST /api/v1/ingest           │
           │  POST /api/v1/sessions/capture │
           ▼                                ▼
    ┌─────────────────────────────────────────┐
    │  aisync serve (central server)          │
    │  https://aisync.yourcompany.com         │
    │                                         │
    │  - Receives all sessions                │
    │  - Auto event extraction                │
    │  - Analytics dashboard (/analytics)     │
    │  - Webhooks → Slack / Discord           │
    │  - API keys for each dev machine        │
    └─────────────────────────────────────────┘
```

---

## Quick Start (5 minutes)

### Step 1: Start the central server

On the machine that will host the dashboard:

```bash
# Install
curl -fsSL https://github.com/ChristopherAparicio/aisync/releases/latest/download/aisync-$(uname -s)-$(uname -m) -o aisync
chmod +x aisync && sudo mv aisync /usr/local/bin/

# Enable auth + start server
aisync config set server.auth.enabled true
aisync config set server.auth.jwt_secret "$(openssl rand -hex 32)"
aisync serve --addr 0.0.0.0:8371
```

Create an admin account and API key:

```bash
# Register (first user = admin)
curl -X POST http://localhost:8371/api/v1/auth/register \
  -H 'Content-Type: application/json' \
  -d '{"username":"admin","password":"your-secure-password"}'

# Login to get a token
TOKEN=$(curl -s -X POST http://localhost:8371/api/v1/auth/login \
  -H 'Content-Type: application/json' \
  -d '{"username":"admin","password":"your-secure-password"}' | jq -r .token)

# Create an API key for dev machines
curl -X POST http://localhost:8371/api/v1/auth/keys \
  -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"name":"dev-machine-1"}' | jq .key
# → "sk-xxxxxxxxxxxx"  ← save this!
```

### Step 2: Configure each dev machine

On every machine running AI coding agents:

```bash
# Install aisync CLI
curl -fsSL https://github.com/ChristopherAparicio/aisync/releases/latest/download/aisync-$(uname -s)-$(uname -m) -o aisync
chmod +x aisync && sudo mv aisync /usr/local/bin/

# Point to the central server
aisync config set server.url https://aisync.yourcompany.com:8371

# Or via environment variable (useful in containers/CI)
export AISYNC_SERVER_URL=https://aisync.yourcompany.com:8371
export AISYNC_API_KEY=sk-xxxxxxxxxxxx
```

### Step 3: Install provider plugins

#### Claude Code (automatic hook)

```bash
# One-line install: adds a Stop hook to Claude Code
aisync hooks install
```

Or manually — add to `~/.claude/settings.json`:

```json
{
  "hooks": {
    "Stop": [{
      "matcher": "",
      "hooks": [{
        "type": "command",
        "command": "aisync capture --provider claude-code --auto"
      }]
    }]
  }
}
```

**How it works:** Every time Claude Code finishes a response, the hook
runs `aisync capture`. Since `server.url` is configured, the session is
automatically forwarded to the central server.

#### OpenCode (plugin)

```bash
# Symlink the plugin
ln -s $(which aisync | xargs dirname)/../plugins/opencode \
  ~/.config/opencode/plugins/opencode-aisync
```

Or copy manually:

```bash
mkdir -p ~/.config/opencode/plugins
cp -r /path/to/aisync/plugins/opencode ~/.config/opencode/plugins/opencode-aisync
```

**How it works:** The plugin hooks into OpenCode events (`session.idle`,
`session.error`, `tool.execute.after`). On idle/error, it runs
`aisync capture`. The CLI forwards to the server automatically.

#### Cursor

```bash
# No plugin needed — just schedule periodic capture
aisync config set scheduler.capture_all.enabled true
aisync config set scheduler.capture_all.interval 5m
aisync config set scheduler.capture_all.provider cursor
```

#### Ollama / Custom LLMs

Push sessions directly via the Ingest API:

```bash
curl -X POST https://aisync.yourcompany.com:8371/api/v1/ingest \
  -H "X-API-Key: sk-xxxxxxxxxxxx" \
  -H 'Content-Type: application/json' \
  -d '{
    "provider": "ollama",
    "agent": "codestral",
    "project_path": "/home/dev/myproject",
    "messages": [
      {"role": "user", "content": "Fix the login bug"},
      {"role": "assistant", "content": "I will fix...", "model": "codestral:latest",
       "tool_calls": [{"name": "bash", "input": "git diff", "output": "...", "state": "completed"}]}
    ]
  }'
```

Or use Ollama's native format:

```bash
curl -X POST https://aisync.yourcompany.com:8371/api/v1/ingest/ollama \
  -H "X-API-Key: sk-xxxxxxxxxxxx" \
  -H 'Content-Type: application/json' \
  -d @ollama-conversation.json
```

### Step 4: Verify it works

```bash
# From a dev machine — should show sessions on the central server
aisync list

# Open the dashboard
open https://aisync.yourcompany.com:8371/analytics
```

---

## Deployment Options

### Option A: Background agent (recommended)

Run `aisync serve` on the dev machine itself as a background agent that
periodically captures and forwards:

```bash
# Start the local agent with auto-capture every 5 min
aisync serve --scheduler \
  --capture-all-interval 5m \
  --capture-all-provider claude-code,opencode
```

The scheduler will:
1. Detect new/updated sessions every 5 minutes
2. Capture them locally
3. Forward to the central server (if `server.url` is configured)

### Option B: Cron job

```cron
# Capture all sessions every 5 minutes
*/5 * * * * AISYNC_SERVER_URL=https://aisync.yourcompany.com:8371 AISYNC_API_KEY=sk-xxx aisync capture --all --provider claude-code,opencode 2>/dev/null
```

### Option C: Docker sidecar

```yaml
# docker-compose.yml
services:
  aisync-agent:
    image: ghcr.io/christopheraparicio/aisync:latest
    command: ["serve", "--scheduler", "--capture-all-interval", "5m"]
    environment:
      AISYNC_SERVER_URL: https://aisync.yourcompany.com:8371
      AISYNC_API_KEY: sk-xxxxxxxxxxxx
    volumes:
      - ~/.claude:/root/.claude:ro          # Claude Code sessions
      - ~/.local/share/opencode:/root/.local/share/opencode:ro  # OpenCode sessions
```

### Option D: MCP integration (for AI assistants)

If your AI assistant supports MCP (Model Context Protocol), aisync
exposes 30+ tools via stdio:

```json
{
  "mcpServers": {
    "aisync": {
      "command": "aisync",
      "args": ["mcp"],
      "env": {
        "AISYNC_SERVER_URL": "https://aisync.yourcompany.com:8371",
        "AISYNC_API_KEY": "sk-xxxxxxxxxxxx"
      }
    }
  }
}
```

This lets the AI assistant itself query session history, capture,
search, and analyze — all forwarded to the central server.

---

## What Gets Collected

Every captured session automatically extracts:

| Data | How | Dashboard View |
|---|---|---|
| **Tool calls** | Every tool invocation (name, duration, state) | `/analytics` → Top Tools |
| **Skills** | `load_skill` tool calls + `<skill_content>` tags | `/analytics` → Skills |
| **Agents** | Provider + agent name from session metadata | `/analytics` → Agents |
| **Commands** | Bash/shell commands with base command extraction | `/analytics` → Top Commands |
| **Errors** | Tool errors, rate limits, provider errors | `/analytics` → Errors |
| **Images** | Image attachments with size and token estimates | `/analytics` → Images |
| **Tokens** | Input/output/cache tokens per message | `/costs` |
| **Cost** | Computed from token usage × model pricing | `/costs` |

All data is aggregated into hourly and daily buckets for fast
dashboard queries.

---

## Configuration Reference

### Environment Variables

| Variable | Description |
|---|---|
| `AISYNC_SERVER_URL` | Central server URL (enables remote mode) |
| `AISYNC_API_KEY` | API key for authentication |
| `AISYNC_DATABASE_PATH` | Custom SQLite path (default: `~/.config/aisync/aisync.db`) |
| `AISYNC_CAPTURE_MODE` | `full`, `compact` (default), or `summary` |
| `AISYNC_PLUGIN_DEBUG` | Set to `1` for plugin debug logs |

### Config File (`~/.config/aisync/config.json`)

```json
{
  "server": {
    "url": "https://aisync.yourcompany.com:8371",
    "auth": {
      "enabled": true,
      "jwt_secret": "..."
    }
  },
  "scheduler": {
    "capture_all": {
      "enabled": true,
      "interval": "5m",
      "provider": "claude-code,opencode"
    }
  },
  "webhooks": {
    "hooks": [
      {
        "url": "https://hooks.slack.com/services/xxx",
        "events": ["session.captured", "session.analyzed"]
      }
    ]
  },
  "telemetry": {
    "enabled": true
  }
}
```

---

## Backfilling Existing Sessions

If you have sessions captured before setting up the central server:

```bash
# Push all local sessions to the server
aisync push

# Or backfill event analytics for all sessions
aisync backfill events

# Just recompute analytics buckets (faster)
aisync backfill events --recompute-only
```

---

## Troubleshooting

### Sessions not appearing on the server

```bash
# Check the CLI is in remote mode
aisync config get server.url
# Should print your server URL

# Test connectivity
curl -s https://aisync.yourcompany.com:8371/api/v1/health
# Should return {"status":"ok"}

# Test auth
curl -s -H "X-API-Key: sk-xxx" https://aisync.yourcompany.com:8371/api/v1/stats
# Should return JSON stats
```

### Plugin not triggering capture

```bash
# Debug Claude Code hook
AISYNC_PLUGIN_DEBUG=1 aisync capture --provider claude-code

# Debug OpenCode plugin
AISYNC_PLUGIN_DEBUG=1 opencode
# Look for [aisync] lines in output
```

### Analytics dashboard empty

```bash
# Backfill events for all sessions
aisync backfill events

# Check event count
aisync stats --tools
```
