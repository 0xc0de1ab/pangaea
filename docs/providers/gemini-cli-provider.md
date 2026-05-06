# Gemini CLI Provider

Gemini CLI provider runs Gemini CLI in an isolated container and exposes it
through Pangaea provider shim.

## Purpose

- Isolate Gemini OAuth credentials per account.
- Support multiple Gemini accounts on one host via explicit auth paths.
- Use official Gemini CLI to refresh OAuth state.

## Kind

- `kind`: `cli-container`
- `service`: `gemini`

## Capabilities

Expected:

- `api.gemini.generateContent`
- `api.openai.chat`
- `usage.read`
- `models.read`
- `auth.file`
- `auth.refresh.oneshot`
- `stream.sse`

Provider-specific:

- `provider.gemini.acp`

## Auth

Auth source is Gemini OAuth credentials.

Typical container path:

```text
HOME=/var/lib/pangaea/home/gemini
/var/lib/pangaea/home/gemini/.gemini/oauth_creds.json
```

Explicit host path is required for multiple accounts on one host.

## Bootstrap

1. Copy configured `oauth_creds.json`.
2. Set `HOME` or Gemini config path.
3. Validate `expiry_date` with eager refresh window.
4. Register provider account and usage state.

## Refresh

Refresh runs inside container.

Command should support login shell behavior only inside controlled image.

Example:

```text
gemini -p "Reply with OK only." --skip-trust --approval-mode plan --output-format json
```

## Runtime / Local Server

Possible bridge modes:

- `upstream.adapter: cli-oneshot`, implemented by running
  `gemini -p <prompt> --skip-trust --approval-mode plan --output-format json`
  per routed request
- ACP/local protocol if available
- HTTP hook/MITM observation from `cli-sidecar`

`cli-oneshot` registers as a Gemini-capable provider. Streaming is wrapped from
the completed response until ACP/local streaming is wired directly.

## Models

Gemini model aliases should map to route policy:

- flash
- flash-lite
- pro

## Usage

Use existing Pangaea Gemini usage probe where possible:

- Code Assist load/quota endpoint
- Flash/Flash Lite/Pro windows

The provider shim reads the copied `oauth_creds.json` and reports probe output
as `usage.native_summary`.

## Routing Notes

Gemini provider may be used for Gemini-compatible public API routes and OpenAI
chat routes after canonical transform.

## Limitations

- OAuth refresh behavior may depend on installed Gemini CLI version.
- Node/npm version should be pinned in image.

## Tests

- explicit auth path
- eager refresh window
- refresh command through controlled shell
- usage probe
- multiple Gemini accounts on one host
- Gemini streaming transform
