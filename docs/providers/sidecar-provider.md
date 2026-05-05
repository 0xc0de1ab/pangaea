# Sidecar Provider

Sidecar provider는 IDE, local agent, code assistant, 또는 vendor sidecar runtime을
provider shim으로 감싸는 provider kind다.

## Purpose

- Chat-only API provider가 아닌 workspace/tool/code capability를 route pool에 포함한다.
- Antigravity, Cline, GitHub Copilot 같은 runtime을 표준 provider contract로 노출한다.
- Agent execution과 normal LLM chat routing을 분리한다.

## Kind

- `kind`: `sidecar-agent`

Examples:

- Antigravity sidecar
- Cline extension relay
- GitHub Copilot sidecar

## Capabilities

Possible:

- `api.openai.chat`
- `agent.tool_use`
- `agent.workspace.read`
- `agent.workspace.write`
- `agent.terminal`
- `agent.mcp`
- `code.completion`
- `stream.sse`
- `stream.bidirectional`

## Auth

Auth depends on vendor sidecar.

Patterns:

- local app/session token
- extension state file
- OAuth file
- IDE account session
- vendor API token

Auth must be reported without leaking raw token.

## Bootstrap

Bootstrap may include:

- installing extension
- injecting relay
- starting code-server/IDE sidecar
- copying vendor state
- validating workspace mount policy

## Refresh

Refresh may be unsupported. If supported, it is sidecar-specific.

Router must treat unsupported refresh as non-refreshable auth.

## Runtime / Local Server

Common runtimes:

- HTTP relay
- WebSocket JSON-RPC
- IDE extension command bridge
- local code completion service

Provider local endpoint is private to shim/container.

## Models

Sidecar providers may expose:

- model names
- capability names
- agent modes
- code completion modes

Router should distinguish chat model aliases from agent capability aliases.

## Usage

Usage may be weak or unavailable.

Fallback:

- router-estimated tokens
- request count
- duration
- provider-specific quotas if readable

## Routing Notes

Sidecar providers require stronger policy.

Route constraints should include:

- user/group permission
- workspace access mode
- tool access mode
- terminal access mode
- audit requirements

## Limitations

- Sidecar protocols are less stable.
- Workspace access is high-risk.
- Vendor ToS/account sharing constraints may apply.

## Tests

- workspace read/write permission
- tool execution permission
- long-running job cancel
- sidecar disconnect
- code completion request
- audit events for tool/workspace actions
