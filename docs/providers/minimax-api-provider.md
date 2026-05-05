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

Secret reference/API key.

## Bootstrap

- load secret
- validate upstream URL
- discover or verify model list

## Refresh

Secret reload/key rotation only.

## Runtime / Local Server

Generic API-compatible shim.

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
