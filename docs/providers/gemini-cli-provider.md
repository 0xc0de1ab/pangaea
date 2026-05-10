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

Typical container layout:

```text
HOME=<provider-home>/gemini
<provider-home>/gemini/.gemini/oauth_creds.json
<provider-home>/gemini/.gemini/settings.json
```

Explicit host path is required for multiple accounts on one host.
The runtime generates a minimal `settings.json` with
`oauth-personal` selected when no explicit settings file is supplied, because
current Gemini CLI versions refuse to run with only `oauth_creds.json`.

## Bootstrap

1. Copy configured `oauth_creds.json`.
2. Copy or generate a minimal `settings.json` that selects OAuth personal auth.
3. Set `HOME` or Gemini config path.
4. Validate `expiry_date` with eager refresh window.
5. Register provider account and usage state.

## Refresh

Refresh runs inside container.

Command should support login shell behavior only inside controlled image.

Example:

```text
gemini -p "Reply with OK only." --skip-trust --approval-mode plan --output-format json
```

## Runtime / Local Server

Possible bridge modes:

- `provider_mode: cli-adapter`, implemented by running
  `gemini -p <prompt> --skip-trust --approval-mode plan --output-format json`
  per routed request
- `provider_mode: acp` (planned), implemented through Gemini CLI
  `--acp` JSON-RPC
- `provider_mode: http-direct`, implemented by making Gemini CLI Code Assist
  HTTPS requests directly from the shim
- HTTP hook/MITM observation from `cli-sidecar` for fixture capture

`cli-adapter` registers as a Gemini-capable provider. For Gemini CLI versions
that support it, streaming uses `--output-format stream-json`; older or custom
commands fall back to wrapping the completed response.

`direct-http` is based on captured Gemini CLI traffic. It reads the copied
`oauth_creds.json` for the current `access_token`, calls
`v1internal:loadCodeAssist` to discover the Code Assist project, then uses:

- `v1internal:generateContent` for buffered calls
- `v1internal:streamGenerateContent?alt=sse` for native SSE streaming
- `v1internal:retrieveUserQuota` for per-model quota enrichment

The stream decoder intentionally drops Gemini `thought: true` parts so hidden
thinking chunks captured from the upstream SSE stream do not leak into router
responses.

`internal/geminidirect/testdata/acp_stream_request_shape.json` fixes a
redacted `gemini --acp` `streamGenerateContent` request shape. The direct-http
unit test captures the outgoing request with a fake `RoundTripper`, without
sending traffic to Google, and compares the normalized HTTP method/path/query,
headers, Code Assist envelope, `generationConfig`, `session_id`, and contents
shape against that fixture.

Tool/MCP parity is covered by
`internal/geminidirect/testdata/acp_tool_callback_request_shape.json`. The
canonical request now carries provider tool declarations, including MCP tools
such as `mcp_pangaea-fixture_fixture_echo`, and direct-http serializes them as
Gemini Code Assist `tools.functionDeclarations[].parametersJsonSchema`.
Callback results are represented as canonical `tool` messages and serialized
back into Gemini `functionResponse` parts with `name`, `id`, and `response`,
matching the ACP continuation shape.

For runtime MCP execution, `direct-http` can start stdio MCP servers directly
from the provider shim. Configure `PANGAEA_MCP_SERVERS_JSON` with either a
Gemini-style `{"mcpServers": {...}}` object or a `servers` array. When that
environment variable is omitted, the shim reads `mcpServers` from
`PANGAEA_GEMINI_SETTINGS_PATH` or `$HOME/.gemini/settings.json`. Tools are
registered with Gemini CLI-compatible names:

```text
mcp_<server-name>_<tool-name>
```

When a streamed response returns a `functionCall` for a configured MCP tool,
the shim executes `tools/call`, appends a canonical tool result message, and
issues the ACP-shaped continuation request internally. Intermediate tool-call
stream events are not forwarded to downstream clients; only the final response
round is streamed. `PANGAEA_MCP_TOOL_ROUNDS` limits the continuation loop and
defaults to 4.

## Models

Gemini model aliases should map to route policy:

- `gemini-default` (defaults to the `auto-gemini-3` group)
- `flash`
- `flash-lite`
- `pro`
- `auto-gemini-3`
- `auto-gemini-2.5`

Current CLI model IDs are discovered from Code Assist `retrieveUserQuota`
buckets. `PANGAEA_MODEL`/`PANGAEA_MODEL_ALIAS` only seed the initial default
model before the first discovery pass; `PANGAEA_MODELS` is not required for
Gemini direct-http providers.

Gemini CLI also exposes grouped auto model choices. Pangaea marks these models
with `kind: group` and reports the fixed group member list so the dashboard can
show them differently from concrete model IDs:

| Model ID | Display | Members |
| --- | --- | --- |
| `auto-gemini-3` | `Auto (Gemini 3)` | `gemini-3.1-pro-preview`, `gemini-3-flash-preview` |
| `auto-gemini-2.5` | `Auto (Gemini 2.5)` | `gemini-2.5-pro`, `gemini-2.5-flash` |

`gemini-default` is attached to `auto-gemini-3`, so dashboards and API clients
use the grouped auto model by default. Concrete Flash/Pro IDs remain available
for explicit routing.

These values mirror the Gemini CLI model picker. Pangaea derives the concrete
members from the quota bucket model IDs returned for the authenticated account,
then adds grouped auto entries when matching Gemini 3 or Gemini 2.5 members are
available.

## Usage

Use existing Pangaea Gemini usage probe where possible:

- Code Assist load/quota endpoint
- Flash/Flash Lite/Pro windows

The provider shim reads the copied `oauth_creds.json` and reports probe output
as `usage.native_summary`.

## Routing Notes

Gemini provider may be used for Gemini-compatible public API routes and OpenAI
chat routes after canonical transform.

Router policy may expose public group aliases with ordered `canonical_models`.
The router tries each canonical model in order and only falls through to the
next item when no provider can serve the current item. This lets operators
publish stable names such as `gemini-auto`, `gemini-auto-3`, and
`gemini-auto-2.5` while preserving a priority hierarchy across provider-native
names.

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

## Kind E2E

The local kind deployment is `deploy/kind/gemini-runtime.yaml`.

```bash
make kind-gemini-e2e
```

The script checks `assets/.gemini/oauth_creds.json` first and then
`~/.gemini/oauth_creds.json`. Set `PANGAEA_GEMINI_AUTH_PATH` to use an explicit
account file. Set `PANGAEA_E2E_INVOKE=1` to run real OpenAI, Anthropic, and
Gemini compatibility calls through the provider.
