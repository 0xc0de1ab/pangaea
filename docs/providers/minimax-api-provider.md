# MiniMAX API Provider

MiniMAX API provider represents MiniMAX external API endpoints.

## Purpose

- Add MiniMAX as API-compatible provider candidate.
- Normalize MiniMAX-compatible responses and usage.

## Kind

- `kind`: `api-compatible`
- `service`: `minimax`

## Capabilities

Likely:

- `api.anthropic.messages`
- `api.openai.chat`
- `usage.read`
- `models.read`
- `stream.sse`
- `auth.api_key`

## Auth

MiniMAX uses API-key auth. In isolated mode, copy a host key file into the
container instead of mounting it:

```yaml
providers:
  - id: minimax-anthropic
    instance_id: minimax-anthropic-a1
    kind: api-compatible
    image: pangaea/provider-api-compatible:2026.05.1
    service: minimax
    account_hint: minimax-prod
    models:
      - id: minimax-m1
        aliases: [minimax-default]
        capabilities: [api.anthropic.messages, stream.sse]
    upstream:
      base_url: https://api.minimax.io/anthropic
      compat: anthropic
      api_key_mode: bearer
    auth:
      mode: api_key
      bootstrap: copy
      host_path: /srv/pangaea/secrets/minimax.key
      container_path: /run/pangaea/secrets/minimax.key
    shim:
      protocols: [anthropic]
      capabilities: [api.anthropic.messages, stream.sse, usage.read, models.read, auth.api_key]
```

## Bootstrap

- load secret
- validate upstream URL
- discover or verify model list

## Refresh

Secret reload/key rotation only.

## Runtime / Local Server

Generic `pangaea/provider-api-compatible` shim.

## Models

Static config first, discovery if provider supports it.

## Usage

Prefer provider-reported usage. Store router estimate when missing.

## Routing Notes

Can act as lower-cost or fallback provider depending on route policy.

## Limitations

- Compatibility behavior must be verified against actual upstream.
- Streaming chunks may not exactly match Anthropic/OpenAI shape.

## Tests

- model list
- streaming
- missing usage
- upstream timeout
- rate limit mapping
