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

Auth/session state comes from Antigravity local state.

Shim must report account and auth status without leaking tokens.

## Bootstrap

May require:

- server bundle
- local state copy
- sidecar process launch
- protocol verification

## Refresh

Provider-specific. If unsupported, report non-refreshable.

## Runtime / Local Server

Reference implementation:

- `/workspace/antigravity-cli/antigravity-compat-proxy`

Bridge likely uses local server files, state DB, and sidecar protocol.

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
