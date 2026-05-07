# GLM API Provider

GLM API provider represents GLM-compatible external endpoints, including
Anthropic-compatible or OpenAI-compatible modes.

## Purpose

- Add GLM as an API fallback/candidate without CLI runtime.
- Normalize GLM response, usage, and errors into Pangaea canonical model.

## Kind

- `kind`: `api-compatible`
- `service`: `glm`

## Capabilities

Depends on configured upstream:

- `api.anthropic.messages`
- `api.openai.chat`
- `usage.read`
- `models.read`
- `stream.sse`
- `auth.api_key`

## Auth

GLM uses API-key auth. For isolated containers, configure `auth.mode: api_key`
with copy bootstrap:

```yaml
providers:
  - id: glm-anthropic
    instance_id: glm-anthropic-a1
    kind: api-compatible
    image: pangaea/provider-api-compatible:2026.05.1
    service: glm
    account_hint: glm-prod
    models:
      - id: glm-4.6
        aliases: [glm-default]
        capabilities: [api.anthropic.messages, stream.sse]
    upstream:
      base_url: https://open.bigmodel.cn/api/anthropic
      compat: anthropic
      api_key_mode: bearer
    auth:
      mode: api_key
      bootstrap: copy
      host_path: /srv/pangaea/secrets/glm.key
      container_path: /run/pangaea/secrets/glm.key
    shim:
      protocols: [anthropic]
      capabilities: [api.anthropic.messages, stream.sse, usage.read, models.read, auth.api_key]
```

Node-agent copies the key once during container creation and can re-copy it on
reconcile when `auth.sync.host_to_container: reconcile` is set.

## Bootstrap

- load secret
- validate base URL allowlist
- perform health/model probe

## Refresh

No OAuth refresh. Supports secret reload/key rotation.

## Runtime / Local Server

Shim uses the generic `api-compatible` image and acts as egress proxy plus
compatibility normalizer.

## Models

Models may be discovered from upstream or configured statically.

## Usage

Use per-request usage if available. Otherwise estimate router-side.

## Routing Notes

Useful as fallback for Anthropic/OpenAI-compatible routes.

## Limitations

- Compatible response shape may drift.
- Tool/multimodal support must be tested per model.

## Tests

- Anthropic-compatible request
- OpenAI-compatible request
- response drift
- missing usage
- 429/5xx mapping
