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

API key secret reference.

No file bootstrap.

## Bootstrap

- load secret
- validate base URL allowlist
- perform health/model probe

## Refresh

No OAuth refresh. Supports secret reload/key rotation.

## Runtime / Local Server

Shim acts as egress proxy and compatibility normalizer.

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
