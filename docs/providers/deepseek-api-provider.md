# DeepSeek API Provider

DeepSeek API provider represents DeepSeek OpenAI-compatible external endpoints.

## Purpose

- Add DeepSeek as OpenAI-compatible route candidate.
- Normalize model, usage, and errors into Pangaea canonical model.

## Kind

- `kind`: `api-compatible`
- `service`: `deepseek`

## Capabilities

Expected:

- `api.openai.chat`
- `models.read`
- `usage.read`
- `stream.sse`
- `auth.api_key`

## Auth

API key secret reference.

## Bootstrap

- load secret
- validate upstream URL
- health/model probe

## Refresh

Secret reload/key rotation only.

## Runtime / Local Server

Generic OpenAI-compatible egress shim.

## Models

Route policy maps public aliases to DeepSeek native model names.

## Usage

Use OpenAI-compatible usage fields if present.

## Routing Notes

DeepSeek may be a fallback for general chat/coding aliases depending on policy.

## Limitations

- Some OpenAI features may be unsupported or approximate.
- Error and usage fields may drift.

## Tests

- OpenAI chat non-streaming
- OpenAI chat streaming
- error mapping
- usage normalization
- model alias mapping
