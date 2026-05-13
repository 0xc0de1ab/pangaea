# GitHub Copilot Provider

GitHub Copilot provider exposes Copilot-backed chat or code completion through
two explicit provider modes:

- `sdk`: GitHub Copilot SDK relay mode. This is the default and uses a local
  OpenAI-compatible HTTP relay.
- `acp`: GitHub Copilot CLI ACP mode. This uses `copilot --acp --stdio` through
  a Pangaea ACP adapter.

## Purpose

- Represent Copilot chat/code completion as Pangaea provider capability.
- Keep Copilot-specific implementation behind sidecar provider contract.

## Kinds

- `sdk`: `kind: sidecar-agent`
- `acp`: `kind: cli-container`
- `service`: `github-copilot`

## Capabilities

Initial `sdk` capabilities:

- `api.openai.chat`
- `api.anthropic.messages`
- `api.gemini.generateContent`
- `code.completion`
- `usage.read`
- `models.read`
- `stream.sse`

Initial `acp` capabilities:

- `api.openai.chat`
- `api.anthropic.messages`
- `api.gemini.generateContent`
- `code.completion`
- `usage.read`
- `models.read`

Do not advertise workspace write, terminal, or tool-use capabilities until the
adapter implements and audits those flows.

## Auth

Auth comes from GitHub/Copilot CLI account state or token environment variables.
The provider image persists `HOME=/var/lib/pangaea/home/copilot`.

Must report:

- account id/display
- auth state
- subscription/entitlement state if available

Current auth refresh is not automated by Pangaea. Operators should sign in with
`copilot login` in the provider state volume or inject a supported token such as
`COPILOT_GITHUB_TOKEN`.

## Bootstrap

The Pangaea image provides a `providers/github-copilot-sidecar` runtime. In
`sdk` mode it supervises `/usr/local/bin/copilot-relay`, which is a local
OpenAI-compatible bridge backed by the GitHub Copilot SDK.

Default container settings:

```text
PANGAEA_SHIM_MODE=sidecar-agent
PANGAEA_SERVICE=github-copilot
PANGAEA_PROVIDER_MODE=sdk
PANGAEA_UPSTREAM_DIALECT=openai
PANGAEA_UPSTREAM_BASE_URL=http://127.0.0.1:4141
```

Node-agent example:

```yaml
providers:
  - id: copilot-default
    kind: sidecar-agent
    provider_mode: sdk
    image: pangaea/provider-github-copilot-sidecar:dev
    service: github-copilot
    account_hint: operator@example.test
    shim:
      entrypoint: [/usr/local/bin/provider-entrypoint]
      command: [/usr/local/bin/copilot-relay, --listen, 127.0.0.1:4141]
      protocols: [openai, anthropic, gemini]
      capabilities:
        - api.openai.chat
        - api.anthropic.messages
        - api.gemini.generateContent
        - code.completion
        - stream.sse
        - usage.read
        - models.read
    upstream:
      base_url: http://127.0.0.1:4141
      compat: openai
```

ACP mode example:

```yaml
providers:
  - id: copilot-acp
    kind: cli-container
    provider_mode: acp
    image: pangaea/provider-github-copilot-sidecar:dev
    service: github-copilot
    shim:
      entrypoint: [/usr/local/bin/provider-entrypoint]
      protocols: [openai, anthropic, gemini]
      capabilities:
        - api.openai.chat
        - api.anthropic.messages
        - api.gemini.generateContent
        - code.completion
        - usage.read
        - models.read
```

## Refresh

Not yet defined. May require GitHub device/browser login and may not be
automatable.

## Runtime / Local Server

`sdk` mode uses a local OpenAI-compatible HTTP relay. Pangaea only sees the
normalized HTTP-compatible endpoint.
The public `copilot-default` / `github-copilot-default` model aliases map to
the Copilot SDK `auto` model by default. Override with `COPILOT_RELAY_MODEL`
when a deployment needs a specific Copilot model id.

`acp` mode starts one Copilot ACP subprocess per invoke. The first
implementation is non-streaming and does not support tool messages.

## Models

May expose chat models, completion models, or opaque Copilot capabilities.

## Usage

Provider usage may be unavailable. The sidecar shim reports request/token
counts observed by Pangaea. A relay can expose richer usage through compatible
metadata later.

## Routing Notes

Copilot provider should initially be opt-in and limited to code completion or
operator-approved users.

## Limitations

- Vendor ToS and account sharing risks require explicit policy.
- Auth automation may not be feasible.
- ACP support in Copilot CLI is public preview and may change.
- `acp` mode does not support streaming or tool calls yet.

## Tests

- sidecar provider registration
- ACP provider registration
- OpenAI-compatible bridge routing
- ACP JSON-RPC session fixtures
- capability-only routing
- entitlement unavailable state
- code completion contract fixture
