# GitHub Copilot Sidecar Provider

GitHub Copilot sidecar provider is a future sidecar provider for Copilot-backed
chat or code completion capability.

## Purpose

- Represent Copilot chat/code completion as Pangaea provider capability.
- Keep Copilot-specific implementation behind sidecar provider contract.

## Kind

- `kind`: `sidecar-agent`
- `service`: `github-copilot`

## Capabilities

Initial Pangaea capabilities:

- `code.completion`
- `api.openai.chat`
- `usage.read`
- `models.read`
- `agent.workspace.read`
- `stream.sse`

## Auth

Auth likely comes from GitHub/Copilot account state.

Must report:

- account id/display
- auth state
- subscription/entitlement state if available

## Bootstrap

The current Pangaea port provides a `providers/github-copilot-sidecar` image
and `sidecar-agent` shim mode. The image does not implement a Copilot relay by
itself; it supervises an optional sidecar command and connects the Pangaea shim
to a local HTTP-compatible bridge.

Default container settings:

```text
PANGAEA_SHIM_MODE=sidecar-agent
PANGAEA_SERVICE=github-copilot
PANGAEA_UPSTREAM_DIALECT=openai
PANGAEA_UPSTREAM_BASE_URL=http://127.0.0.1:4141
```

Node-agent example:

```yaml
providers:
  - id: copilot-default
    kind: sidecar-agent
    image: pangaea/provider-github-copilot-sidecar:dev
    service: github-copilot
    account_hint: operator@example.test
    shim:
      entrypoint: [/usr/local/bin/provider-entrypoint]
      command: [/usr/local/bin/copilot-relay, --listen, 127.0.0.1:4141]
      protocols: [openai]
      capabilities:
        - api.openai.chat
        - code.completion
        - usage.read
        - models.read
    upstream:
      base_url: http://127.0.0.1:4141
      compat: openai
```

## Refresh

Not yet defined. May require GitHub device/browser login and may not be
automatable.

## Runtime / Local Server

Current supported bridge mode is a local OpenAI-compatible HTTP relay. The
relay may be implemented by a VS Code/code-server extension bridge, a Copilot
language-server wrapper, or another approved local process, but Pangaea only
sees the normalized HTTP-compatible endpoint.

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

- Current `/workspace/github-copilot-sidecar` is only an empty skeleton, so the
  Pangaea port supplies the shim/container integration but not a Copilot relay.
- Vendor ToS and account sharing risks require explicit policy.
- Auth automation may not be feasible.

## Tests

- sidecar provider registration
- OpenAI-compatible bridge routing
- capability-only routing
- entitlement unavailable state
- code completion contract fixture
