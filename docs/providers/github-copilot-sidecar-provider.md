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

Potential:

- `code.completion`
- `api.openai.chat`
- `agent.workspace.read`
- `stream.sse`

## Auth

Auth likely comes from GitHub/Copilot account state.

Must report:

- account id/display
- auth state
- subscription/entitlement state if available

## Bootstrap

Not yet defined.

Potential bootstrap:

- copy approved Copilot state
- start VS Code/code-server extension relay
- validate Copilot entitlement

## Refresh

Not yet defined. May require GitHub device/browser login and may not be
automatable.

## Runtime / Local Server

Likely sidecar/extension relay, not simple HTTP API.

## Models

May expose chat models, completion models, or opaque Copilot capabilities.

## Usage

Provider usage may be unavailable. Router should estimate by request/response
size and request count.

## Routing Notes

Copilot provider should initially be opt-in and limited to code completion or
operator-approved users.

## Limitations

- Current `/workspace/github-copilot-sidecar` has little implementation.
- Vendor ToS and account sharing risks require explicit policy.
- Auth automation may not be feasible.

## Tests

- placeholder provider registration
- capability-only routing
- entitlement unavailable state
- code completion contract fixture
