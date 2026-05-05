# CLI Container Provider

CLI container provider는 official CLI를 container 안에 설치하고, provider shim이
그 CLI 또는 local server를 감싸 router protocol로 노출하는 provider kind다.

## Purpose

- Host의 provider CLI/auth state를 격리한다.
- 동일 host에서 같은 service의 여러 account를 독립 provider로 실행한다.
- Auth refresh는 container 안에서 official CLI를 통해 유도한다.
- Router는 CLI별 차이를 capability contract로만 본다.

## Kind

- `kind`: `cli-container`

Examples:

- Codex CLI
- Claude CLI
- Gemini CLI

## Capabilities

Common:

- `auth.file`
- `auth.refresh.oneshot`
- `usage.read`
- `models.read`
- `stream.sse`

Depending on CLI:

- `api.openai.chat`
- `api.anthropic.messages`
- `api.gemini.generateContent`
- `agent.tool_use`
- `agent.workspace.read`
- `agent.workspace.write`

## Auth

Auth is file-based.

Provider config must support explicit `auth.host_path` so one host can run
multiple accounts for the same service.

Example:

```yaml
providers:
  - id: codex-samtest
    service: codex
    auth:
      bootstrap: copy
      host_path: /srv/pangaea/auth/codex/samtest/auth.json
      container_path: /var/lib/pangaea/auth/codex/auth.json
```

## Bootstrap

Default bootstrap is copy:

1. Node agent reads `auth.host_path`.
2. Node agent verifies minimum format.
3. Node agent copies auth to container.
4. Shim validates auth inside container.
5. Shim reports provider/account/auth state.

Bind mount is not default.

## Refresh

Refresh runs inside container.

Flow:

1. Mark provider instance `refreshing` or `draining`.
2. Run provider-specific oneshot command.
3. Watch auth file fingerprint.
4. Validate new auth.
5. Report result.
6. Resume routing if ready.

## Runtime / Local Server

Possible bridge modes:

- provider app-server/local server
- stdio command
- pty/tmux adapter
- MITM hook for provider-internal HTTP observation

Local server must be private to container/shim network.

## Models

Model discovery may come from:

- provider local server
- provider usage endpoint
- static provider spec
- router policy aliases

Shim reports native model ids and aliases.

## Usage

Usage may come from:

- provider native usage endpoint
- local server account/rate limit method
- current Pangaea usage probes
- router-side estimation when native usage missing

Usage reports include `host_name`, `provider_id`, `account_id`, and `service`.

## Routing Notes

CLI providers are heavier than API providers. Routing should consider:

- auth freshness
- provider native quota
- queue depth
- warm stream availability
- first-token latency
- container memory pressure

## Limitations

- CLI output and local server protocols may change.
- Some CLIs require interactive login or shell profile behavior.
- CLI providers may be high-risk if workspace/tool access is enabled.

## Tests

- explicit auth path bootstrap
- same host same service multi-account
- refresh window oneshot
- local server crash detection
- CLI process cancellation
- stderr/token redaction
- usage report normalization
