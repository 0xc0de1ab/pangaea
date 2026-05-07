# API-Compatible Provider

API-compatible provider는 외부 LLM API endpoint를 provider shim으로 감싸는
provider kind다.

## Purpose

- GLM, MiniMAX, DeepSeek 같은 compatible endpoint를 Pangaea route pool에 포함한다.
- Router-side user/quota/model policy를 provider-native account와 분리한다.
- Provider별 response drift와 usage 차이를 normalization한다.

## Kind

- `kind`: `api-compatible`

Examples:

- GLM Anthropic-compatible API
- MiniMAX Anthropic-compatible API
- DeepSeek OpenAI-compatible API
- OpenAI-compatible private endpoint
- Anthropic-compatible private endpoint

## Capabilities

Depending on upstream:

- `api.openai.chat`
- `api.openai.responses`
- `api.anthropic.messages`
- `api.gemini.generateContent`
- `usage.read`
- `models.read`
- `auth.api_key`
- `stream.sse`

## Auth

API provider auth is secret/API-key based, not file-auth based.
The current container image is `pangaea/provider-api-compatible`.

Config:

```yaml
providers:
  - id: glm-anthropic
    kind: api-compatible
    image: pangaea/provider-api-compatible:2026.05.1
    service: glm
    upstream:
      base_url: https://api.example.invalid/anthropic
      compat: anthropic
      api_key_mode: bearer
    auth:
      mode: api_key
      bootstrap: copy
      host_path: /srv/pangaea/secrets/glm_api_key
      container_path: /run/pangaea/secrets/glm_api_key
```

Node-agent copies the key file into the container before startup and sets
`PANGAEA_UPSTREAM_API_KEY_FILE` to `auth.container_path` when no explicit
`upstream.api_key_file` is configured. Raw secrets are never reported to router.

Supported API key placements:

- `api_key_mode: bearer`: `Authorization: Bearer <key>`; default.
- `api_key_mode: header` with `api_key_header`: raw header value, for example
  `x-goog-api-key`.
- `api_key_mode: query` with `api_key_query_param`: query parameter value, for
  example `key`.
- `api_key_mode: none`: no automatic API key injection; use explicit headers or
  network-local auth.

## Bootstrap

API-key providers may bootstrap in either of two modes:

- `upstream.api_key_file`: a container-local path already provided by runtime.
- `auth.mode: api_key` with `auth.host_path` and `auth.container_path`: node-agent
  copies the host key file into the container, then shim reads it per request.

Bootstrap validates:

- API key source exists
- upstream base URL is allowed
- model discovery or configured model list works
- provider shim can reach upstream

## Refresh

API providers do not use OAuth file refresh by default.

Supported operations:

- secret reload
- API key rotation
- upstream health recheck

## Runtime / Local Server

Shim can run as lightweight egress proxy.

It may:

- pass through compatible requests
- convert canonical request to upstream dialect
- normalize streaming events
- normalize errors and usage

Current implementation note:

- The shim advertises `stream.sse` because router can expose public SSE
  responses.
- OpenAI/Anthropic/Gemini-compatible upstreams use native SSE when router
  invokes the provider through the streaming data plane.

## Models

Model list may come from:

- upstream `/models`
- static config
- router alias policy

For OpenAI-compatible and Anthropic-compatible providers, the generic shim uses
`GET /v1/models` when available. For Gemini-compatible providers, it uses
`GET /v1beta/models` and strips the `models/` prefix before reporting model ids.
If startup config omits a static model, the shim registers with discovered
models when discovery succeeds and keeps reporting static/router policy aliases
as fallback.

## Usage

Usage may be:

- returned per request
- fetched from upstream account/usage API
- estimated by router

Provider-reported usage and router-estimated usage are both stored.

## Routing Notes

API providers are useful fallback candidates for CLI providers.

Routing should consider:

- provider API quota
- cost class
- latency
- region
- error rate
- response compatibility drift

## Limitations

- “Compatible” APIs often drift in error shapes, streaming chunks, and usage fields.
- Some providers have model names that conflict with public aliases.
- Tool-call and multimodal support may be partial.

## Tests

- response shape drift
- missing usage
- upstream timeout
- 429/5xx normalization
- API key rotation
- model list mismatch
- streaming transform
