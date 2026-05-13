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
- `api.gemini.generateContent`
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
      - id: MiniMax-M2.7
        aliases: [minimax-default]
        capabilities: [api.openai.chat, api.anthropic.messages, api.gemini.generateContent, stream.sse]
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
      protocols: [openai, anthropic, gemini]
      capabilities: [api.openai.chat, api.anthropic.messages, api.gemini.generateContent, stream.sse, usage.read, models.read, auth.api_key]
```

## Bootstrap

- load secret
- validate upstream URL
- discover or verify model list

## Refresh

Secret reload/key rotation only.

## Runtime / Local Server

Generic `pangaea/provider-api-compatible` shim.

MiniMAX upstream is usually Anthropic-compatible, but Pangaea advertises
OpenAI, Anthropic, and Gemini public dialects for the provider. The shim
converts all public dialects through the canonical IR before calling the
Anthropic-compatible upstream endpoint.

## Models

Static config first, discovery if provider supports it.

## Usage

Token Plan quota is fetched with the same API key from MiniMAX's documented
usage endpoint:

```bash
curl --location 'https://www.minimax.io/v1/token_plan/remains' \
  --header 'Authorization: Bearer <API Key>' \
  --header 'Content-Type: application/json'
```

When the configured Anthropic-compatible base URL is
`https://api.minimax.io/anthropic`, Pangaea calls the base host root endpoint
`https://api.minimax.io/v1/token_plan/remains`. The `/anthropic` path only
hosts Anthropic-compatible model and message APIs.

The response exposes per-model current-window and weekly request quota fields
such as `current_interval_total_count`, `current_interval_usage_count`,
`current_weekly_total_count`, and `current_weekly_usage_count`. Pangaea maps
non-zero limits into `native_summary.windows` so the router dashboard and
notifiers can render progress bars.

MiniMAX does not currently expose an API-key-only account profile endpoint
through the Anthropic-compatible API. If no explicit account label is supplied,
Pangaea identifies the key with a non-reversible SHA-256 fingerprint such as
`minimax-key-<12hex>` and records the subscription as `MiniMAX Token Plan`.
This avoids misleading static labels like `minimax-prod` while keeping the key
secret.

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
