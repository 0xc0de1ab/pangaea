# Claude CLI Provider

Claude CLI provider runs Claude Code CLI in an isolated container and exposes it
through Pangaea provider shim.

## Purpose

- Isolate Claude CLI credentials per account.
- Expose Claude as Anthropic/OpenAI-compatible route candidate where possible.
- Track Claude account/subscription and usage windows separately from auth file validity.

## Kind

- `kind`: `cli-container`
- `service`: `claude`

## Capabilities

Expected:

- `api.anthropic.messages`
- `api.openai.chat`
- `usage.read`
- `models.read`
- `auth.file`
- `auth.refresh.oneshot`
- `stream.sse`

If workspace/tool use is enabled:

- `agent.workspace.read`
- `agent.workspace.write`
- `agent.terminal`
- `agent.tool_use`

## Auth

Auth source is Claude credentials file.

Typical container path:

```text
CLAUDE_CONFIG_DIR=/var/lib/pangaea/auth/claude
/var/lib/pangaea/auth/claude/.credentials.json
```

Account metadata may need a separate copied file depending on Claude CLI state.

## Bootstrap

1. Copy configured credentials file.
2. Copy account metadata if configured.
3. Validate expiry/subscription state.
4. Register provider with account display and auth state.

## Refresh

Possible refresh paths:

- `claude auth login` with refresh token environment if supported
- Claude CLI oneshot prompt

Refresh failure must distinguish:

- credential expired
- account/subscription expired
- provider revoked
- CLI unavailable

## Runtime / Local Server

Possible bridge modes:

- `upstream.adapter: cli-oneshot`, implemented by running
  `claude -p <prompt> --permission-mode plan --tools '' --output-format text`
  per routed request
- local server if available
- pty/tmux adapter

`cli-oneshot` registers as an Anthropic-capable provider and can be reached
through router Anthropic/OpenAI compatibility transforms. Streaming is wrapped
from the completed response until a native Claude local server adapter is added.

## Models

Models may be static policy aliases or discovered from provider metadata.

## Usage

Use existing Pangaea Claude usage probe where available. The provider shim
reads the copied credentials file and reports the probe output as
`usage.native_summary`.

Usage must separate provider native plan/limit from router user quota.

## Routing Notes

Claude CLI provider may be high-risk when agent/workspace capabilities are
enabled. Route policies should require explicit user/group permission.

## Limitations

- Claude account/subscription expiry can be validly reported as no viable route.
- CLI output parser must tolerate version changes.

## Tests

- expired credentials
- account expired vs token expired
- refresh nudge
- usage windows
- stream cancellation
- workspace capability gating
