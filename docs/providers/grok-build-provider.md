# Grok Build Provider

Grok Build provider exposes xAI Grok Build CLI through ACP.

## Purpose

- Represent `grok agent stdio` as a Pangaea CLI-container provider.
- Support SuperGrok / X Premium+ cached login state and `XAI_API_KEY`.
- Keep Grok-specific ACP details behind the provider shim contract.

## Kind

- `kind: cli-container`
- `service: grok-build`
- `provider_mode: acp`
- default `provider_type: grok-build-cli`

## Capabilities

Initial capabilities:

- `api.openai.chat`
- `api.anthropic.messages`
- `api.gemini.generateContent`
- `stream.sse`
- `usage.read`
- `models.read`

Do not advertise workspace write, terminal, or tool-use capabilities until the
adapter implements and audits those flows.

## Auth

The provider supports two auth inputs:

- cached Grok CLI login at `~/.grok/auth.json`
- `XAI_API_KEY`

`GROK_CODE_XAI_API_KEY` is accepted as an alias and is mapped to `XAI_API_KEY`
inside the provider entrypoint.

`setup-provider --auth-path ~/.grok/auth.json` copies the cached login into:

```text
/var/lib/pangaea/home/grok/.grok/auth.json
```

The auth file is interpreted with `grok-auth-json-format`.

## Bootstrap

Generate a Kubernetes manifest:

```bash
pangaeactl setup-provider \
  --type k8s \
  --service grok-build \
  --mode acp \
  --auth-path ~/.grok/auth.json
```

Generate a Docker node-agent config:

```bash
pangaeactl setup-provider \
  --type docker \
  --service grok-build \
  --mode acp \
  --auth-path ~/.grok/auth.json
```

The provider image installs the official `grok` binary and runs:

```text
grok --no-auto-update agent stdio
```

## Refresh

Automatic OAuth refresh is not implemented. Operators should run `grok login`
against the provider state volume or refresh the copied `~/.grok/auth.json`.

## Runtime / Local Server

ACP mode starts one Grok subprocess per invoke. Pangaea sends JSON-RPC over
stdio, calls `initialize`, selects `cached_token` or `xai.api_key`, calls
`authenticate`, creates a session, then sends `session/prompt`.

The adapter drains `session/update` text chunks briefly after `session/prompt`
returns because Grok documents assistant text as streaming through update
events.

## Models

Default model:

- `grok-build`

Aliases:

- `grok-build-default`
- `grok-build-0.1`
- `grok-default`

## Usage

Usage is request-count-only for now. Native Grok quota probing is not
implemented.

## Routing Notes

The kind policy routes `grok-build-default`, `grok-default`, `grok-build`, and
`grok-build-0.1` to `provider_type: grok-build-cli`.

## Limitations

- Grok Build CLI is in beta and ACP behavior may change.
- Auth automation is limited to cached CLI auth or API keys.
- Workspace and terminal ACP capabilities are intentionally not exposed yet.

## Tests

- Grok auth format parse/validate/redact
- ACP provider registration and model aliases
- setup-provider manifest generation
- providerfactory adapter selection
