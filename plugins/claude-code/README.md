# aisync — Claude Code Hook

Automatic session capture for [Claude Code](https://claude.ai/code).

## How it works

Claude Code supports [hooks](https://docs.anthropic.com/en/docs/claude-code/hooks) that run at specific lifecycle points. This hook triggers on `Stop` (when the agent finishes) and calls `aisync capture` to snapshot the session.

| Hook | When | Action |
|------|------|--------|
| `Stop` | Agent finishes its response | `aisync capture --provider claude-code --auto` (background) |

## Install

### Automatic

```bash
# Add the hook to ~/.claude/settings.json
cat ~/.claude/settings.json | jq '.hooks.Stop = [{"matcher": "", "hooks": [{"type": "command", "command": "'$(pwd)'/plugins/claude-code/aisync-hook.sh"}]}]' | tee ~/.claude/settings.json
```

### Manual

Add this to `~/.claude/settings.json` under `hooks`:

```json
{
  "hooks": {
    "Stop": [
      {
        "matcher": "",
        "hooks": [
          {
            "type": "command",
            "command": "/path/to/aisync/plugins/claude-code/aisync-hook.sh"
          }
        ]
      }
    ]
  }
}
```

## Environment Variables

| Variable | Default | Description |
|---|---|---|
| `AISYNC_CAPTURE_MODE` | `compact` | Storage mode: `full`, `compact`, or `summary` |
| `AISYNC_PLUGIN_DEBUG` | _(unset)_ | Set to `1` for debug logs |

## How Claude Code sessions are stored

Claude Code stores sessions as JSONL files in:
```
~/.claude/projects/<project-hash>/sessions/<session-id>.jsonl
```

The aisync Claude Code provider (`internal/provider/claude/`) reads these files and parses:
- Message roles (user, assistant)
- Content blocks (text, thinking, tool_use, tool_result, image)
- Token usage (input, output, cache read/write)
- Tool calls (name, input, output, duration, state)
- File changes

## Difference with OpenCode plugin

| Feature | Claude Code (hook) | OpenCode (plugin) |
|---------|-------------------|-------------------|
| Mechanism | Bash hook via `settings.json` | TypeScript plugin via API |
| Trigger | `Stop` event only | `session.idle`, `chat.message`, `tool.execute.after`, `session.compacting` |
| Real-time tracking | No | Yes (message count, tool errors) |
| Background capture | Yes (non-blocking) | Yes |
