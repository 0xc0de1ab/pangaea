# Codex CLI Provider

Codex CLI provider runs OpenAI Codex CLI in an isolated container and exposes it
through Pangaea provider shim.

## Purpose

- Isolate Codex auth and runtime per account.
- Support multiple Codex accounts on one host.
- Refresh Codex auth inside container using official Codex CLI behavior.
- Expose Codex as OpenAI/Anthropic/Gemini-compatible route candidate where supported.

## Kind

- `kind`: `app-server` when Codex runs through `codex app-server`; use
  `cli-container` only for direct one-shot CLI execution.
- `service`: `codex`

## Capabilities

Expected:

- `api.openai.chat`
- `api.anthropic.messages`
- `api.gemini.generateContent`
- `usage.read`
- `models.read`
- `auth.file`
- `auth.refresh.oneshot`
- `stream.sse`
- `provider.codex.app_server`

## Auth

Auth source is Codex `auth.json`.

Container path:

```text
CODEX_HOME=/var/lib/pangaea/auth/codex
/var/lib/pangaea/auth/codex/auth.json
```

Host path must be configurable per provider instance:

```yaml
auth:
  bootstrap: copy
  host_path: /srv/pangaea/auth/codex/samtest/auth.json
  container_path: /var/lib/pangaea/auth/codex/auth.json
```

For the kind e2e bootstrap, when a codex file auth provider omits
`auth.host_path`, node-agent resolves the first non-empty file from:

1. `assets/.codex/auth.json`, searched from the config directory upward
2. `~/.codex/auth.json`

This is only a source selection rule for the host side. The selected file is
still copied into the container path above and must not be mounted.

Validity rule:

- `access_token` JWT expiry is primary.
- `id_token` expiry does not invalidate runtime auth by itself.
- refresh window follows Codex access-token safety skew.

## Bootstrap

1. Copy configured host `auth.json` into container.
2. Set `CODEX_HOME`.
3. Validate account id/email from auth file.
4. Start Codex local bridge/app-server if available.
5. Register provider with `host_name`, service, account, models, auth state.

## Refresh

Refresh command runs inside container.

Example:

```text
codex exec --skip-git-repo-check --sandbox read-only --ephemeral --ignore-user-config --color never "Reply with OK only."
```

Flow:

- mark provider `refreshing`
- run oneshot
- watch `auth.json` fingerprint
- validate new access token
- report `auth.refresh.result`

## Runtime / Local Server

Preferred:

- Codex app-server/JSON-RPC bridge when stable
- Current CLI listen syntax uses a WebSocket URL, for example
  `codex app-server --listen ws://127.0.0.1:8080`

Pangaea shim adapter selection:

```yaml
upstream:
  adapter: websocket
  base_url: ws://127.0.0.1:8080
  compat: openai
```

`adapter: websocket` makes the provider shim speak Codex AppServer JSON-RPC
directly. `adapter: reverse-http` keeps the generic HTTP-compatible provider
path and expects `base_url` to point at a local OpenAI/Anthropic/Gemini
compatibility bridge.

Fallback:

- oneshot `codex exec`
- stdio/pty bridge if needed

Existing reference:

- `/workspace/antigravity-cli/codex-compat-proxy`

## Models

Models may come from:

- Codex local server
- usage/rate-limit endpoint
- static route policy

## Usage

Use existing Pangaea Codex usage probe where possible:

- ChatGPT usage endpoint
- 5h/weekly windows
- model-specific windows

Usage reports must include host/service/account/provider dimensions.

## Routing Notes

Codex provider is a strong candidate for coding model aliases.

Route scoring should consider:

- account quota windows
- auth freshness
- queue depth
- first-token latency
- account affinity if configured

## Limitations

- Codex local server protocol may change.
- CLI versions must be pinned for production.
- Auth file schema may evolve.

## Tests

- expired id_token with valid access_token remains routable
- refresh window oneshot
- explicit host auth path
- two Codex providers on one host
- usage probe
- stream cancellation
- local bridge protocol drift
