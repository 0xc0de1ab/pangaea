# Antigravity Sidecar Provider

Antigravity sidecar provider wraps Antigravity local runtime/sidecar.

## Purpose

- Expose Antigravity models and agent capabilities through Pangaea.
- Reuse existing Antigravity bridge/scraper work from `antigravity-compat-proxy`.

## Kind

- `kind`: `sidecar-agent`
- `service`: `antigravity`

## Capabilities

Possible:

- `api.openai.chat`
- `api.anthropic.messages`
- `api.gemini.generateContent`
- `agent.tool_use`
- `usage.read`
- `models.read`
- `stream.sse`
- `provider.antigravity.sidecar`

## Auth

Auth/session state comes from Antigravity local state. Container bootstrap copies
`state.vscdb` into the runtime state volume when a source file is available.
Supported bootstrap sources include an explicit `PANGAEA_ANTIGRAVITY_AUTH_PATH`,
repo assets, Linux local Antigravity state, and WSL Windows user state under
`<wsl-windows-users-root>/<USER>/AppData/Roaming/Antigravity/User/globalStorage/state.vscdb`.
The shim reads the copied DB file to derive the user email for the provider
`Account` field.

Shim must report account and auth status without leaking tokens.

## Bootstrap

Current wrapper image:

- `pangaea/provider-antigravity-sidecar`

Node-agent passes `shim.command` as the Antigravity relay/local server command.
The container entrypoint starts that command in the background, starts
`pangaeactl provider-shim run` in `sidecar-agent` mode, and exits if either
process exits.

Example:

```yaml
providers:
  - id: antigravity-default
    instance_id: antigravity-a1
    kind: sidecar-agent
    image: pangaea/provider-antigravity-sidecar:2026.05.1
    service: antigravity
    host_name: snowbox
    account_hint: operator@example.test
    models:
      - id: antigravity-default
        aliases: [antigravity-default]
        capabilities: [api.openai.chat, stream.sse]
    shim:
      command: [/usr/local/bin/antigravity-compat-proxy, serve, --proxy-addr, 127.0.0.1:8080]
      protocols: [openai]
      capabilities:
        - api.openai.chat
        - stream.sse
        - usage.read
        - models.read
        - provider.antigravity.sidecar
        - agent.tool_use
        - agent.workspace.read
        - agent.workspace.write
    upstream:
      base_url: http://127.0.0.1:8080
      compat: openai
```

Additional bootstrap may require:

- relay/server bundle supplied in the image or bind-free artifact layer
- local state copy
- sidecar process launch
- protocol verification

## Refresh

Provider-specific. If unsupported, report non-refreshable.

## Runtime / Local Server

Reference implementation:

- `../antigravity-cli/antigravity-compat-proxy`

The Pangaea repo provides the wrapper/supervisor image. The actual
Antigravity-compatible relay binary can be supplied by an image extension or
through the configured `shim.command` path.

The wrapper exports `PANGAEA_SHIM_PROTOCOLS=openai,anthropic,gemini` by
default. It also makes the local proxy and Pangaea shim share one upstream key:

- `OPENAI_API_KEY` defaults to `PANGAEA_UPSTREAM_API_KEY` or
  `pangaea-antigravity-openai`
- `PANGAEA_UPSTREAM_API_KEY` defaults to `OPENAI_API_KEY`
- `ANTHROPIC_API_KEY` and `GOOGLE_API_KEY` get deterministic container-local
  defaults for direct proxy protocol checks

When `upstream.compat: openai` is used, Pangaea can still expose OpenAI,
Anthropic, and Gemini router endpoints because the router/shim converts public
requests into the configured upstream dialect before calling the local proxy.

## Models

Antigravity model list may include Claude, Gemini, GPT-OSS style aliases.

## Usage

Use Antigravity usage endpoints or router estimation if unavailable.

## Routing Notes

Antigravity provider can expose multiple underlying model families. Route policy
must still treat it as one provider instance with explicit capabilities/models.

## Limitations

- Local sidecar protocol may be brittle.
- State DB handling requires careful redaction.

## Tests

- sidecar startup
- model discovery
- protocol verification
- usage read
- streaming
- state redaction
