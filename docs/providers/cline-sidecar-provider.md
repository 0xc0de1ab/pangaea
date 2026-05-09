# Cline Sidecar Provider

Cline sidecar provider wraps Cline extension/relay as an agent executor provider.

## Purpose

- Expose workspace agent capabilities through Pangaea.
- Support OpenAI-compatible chat facade only when route policy allows agent behavior.

## Kind

- `kind`: `sidecar-agent`
- `service`: `cline`

## Capabilities

Expected:

- `agent.workspace.read`
- `agent.workspace.write`
- `agent.terminal`
- `agent.mcp`
- `agent.tool_use`
- `api.openai.chat`
- `stream.sse`

## Auth

Cline auth/session depends on extension state and configured upstream model.

Auth reporting must distinguish:

- sidecar reachable
- underlying model auth valid
- workspace permission valid

## Bootstrap

Possible flow:

- start code-server/container
- install or preload Cline extension
- inject relay server
- start gateway/shim
- validate workspace policy

Reference:

- `../cline-sidecar`

## Refresh

Refresh is provider-specific and may be unsupported.

## Runtime / Local Server

Cline relay exposes actions such as:

- read file
- write file
- execute terminal
- list/call MCP tools

Shim must gate each action by capability and route policy.

## Models

Model identity may come from Cline's configured upstream, not Cline itself.

## Usage

Usage may be unavailable. Router estimation and request duration may be primary.

## Routing Notes

Cline is not a normal chat provider. It should be used for agent/workspace
routes with explicit operator/user permission.

## Limitations

- Extension injection is brittle.
- Workspace write/terminal access is high-risk.
- Usage accounting may be weak.

## Tests

- workspace read-only route
- workspace write allowed route
- terminal denied by default
- MCP call permission
- long-running job cancel
- relay disconnect
