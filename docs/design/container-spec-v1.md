# Provider Container Spec v1 Draft

이 문서는 Pangaea node-agent가 provider runtime container를 만들고 관리하는
규약 초안이다.

현재 구현 문서가 아니라 설계 합의용 draft다.

## Goals

- provider CLI와 auth state를 host에서 격리한다.
- auth 파일은 mount가 아니라 bootstrap copy를 기본으로 한다.
- provider runtime, shim, auth refresh, usage probe를 container boundary 안에 둔다.
- Docker/Podman 같은 runtime 차이를 node-agent abstraction 뒤로 숨긴다.
- container lifecycle이 routing, auth, usage, update와 충돌하지 않도록 한다.

## Runtime Model

기본 단위는 provider instance다.

```text
node-agent
  └─ provider instance
       ├─ provider runtime container
       │    ├─ provider CLI/local server/API egress
       │    └─ provider shim
       └─ persistent provider volume
```

MVP에서는 provider runtime과 shim을 같은 container에 둘 수 있다.
장기적으로는 별도 sidecar container도 허용한다.

Provider local server는 container 내부 loopback 또는 private network에서만
listen해야 한다. Public network에 직접 노출하면 안 된다.

## Provider Kinds

### `cli-container`

Container 안에 official CLI를 설치하고 실행한다.

예:

- Codex CLI
- Claude CLI
- Gemini CLI

필수 기능:

- auth bootstrap copy
- auth validation
- refresh nudge command
- local server, stdio, pty, or app-server bridge
- CLI version report

### `api-compatible`

Container는 외부 API endpoint를 호출하는 egress shim이다.

예:

- GLM Anthropic-compatible API
- MiniMAX Anthropic-compatible API
- DeepSeek OpenAI-compatible API

필수 기능:

- API secret load/reload
- upstream health check
- model/capability report
- usage/error normalization

### `sidecar-agent`

Container는 IDE/agent sidecar 또는 extension relay를 실행한다.

예:

- Antigravity sidecar
- Cline extension relay
- GitHub Copilot sidecar

필수 기능:

- sidecar health
- workspace/tool/code capability report
- local relay bridge
- strict capability gating

## Container Image Layout

Recommended paths:

```text
/usr/local/bin/pangaea-provider-shim
/usr/local/bin/provider-entrypoint
/etc/pangaea/provider.yaml
/var/lib/pangaea/provider/
/var/lib/pangaea/auth/
/var/lib/pangaea/state/
/run/pangaea/
/tmp
```

Environment variables:

- `PANGAEA_PROVIDER_TYPE`
- `PANGAEA_PROVIDER_INSTANCE_ID`
- `PANGAEA_NODE_ID`
- `PANGAEA_CONTROL_URL`
- `PANGAEA_AUTH_DIR`
- `PANGAEA_STATE_DIR`
- `PANGAEA_RUNTIME_KIND`

Provider-specific examples:

- `CODEX_HOME=/var/lib/pangaea/auth/codex`
- `CLAUDE_CONFIG_DIR=/var/lib/pangaea/auth/claude`
- `HOME=/var/lib/pangaea/home/gemini`

Images SHOULD use pinned provider CLI versions for production rollout.
`latest` may be allowed in development but not as default production policy.

## Provider Spec YAML

Node agent reads provider specs from its config or router-assigned desired state.
One node may define multiple providers for the same service. Each provider has
its own `id`, account hint, auth source path, container state, and routing
identity.

`service` names the underlying service family such as `codex`, `claude`,
`gemini`, `glm`, `minimax`, `deepseek`, `antigravity`, `cline`,
`github-copilot`, or `grok-build`. Public API dialects are expressed by
`shim.protocols` and capabilities, not by `service`.

```yaml
providers:
  - provider_type: codex-primary
    kind: cli-container
    provider_mode: app-server
    image: pangaea/provider-codex:2026.05.1
    host_name: snowbox
    account_hint: primary@example.test
    service: codex
    auth:
      mode: file
      bootstrap: copy
      host_path: /srv/pangaea/auth/codex/primary/auth.json
      container_path: /var/lib/pangaea/auth/codex/auth.json
      owner_uid: 10001
      owner_gid: 10001
      file_mode: "0600"
      sync:
        container_to_host: true
        host_to_container: reconcile
    refresh:
      threshold: 5m
      command:
        - codex
        - exec
        - --skip-git-repo-check
        - --sandbox
        - read-only
        - --ephemeral
        - --ignore-user-config
        - --color
        - never
        - Reply with OK only.
      cooldown: 2h
      timeout: 90s
    shim:
      entrypoint: [/usr/local/bin/provider-entrypoint]
      command:
        - codex
        - app-server
        - --listen
        - ws://127.0.0.1:8080
      working_dir: /var/lib/pangaea/provider
      listen: 127.0.0.1:8080
      protocols: [openai, anthropic, gemini]
      capabilities:
        - api.openai.chat
        - api.anthropic.messages
        - api.gemini.generateContent
        - usage.read
        - auth.file
        - auth.refresh.oneshot
    resources:
      cpus: 2
      memory: 2GiB
      pids_limit: 512
    upstream:
      base_url: ws://127.0.0.1:8080
      compat: openai

  - provider_type: claude-primary
    kind: cli-container
    provider_mode: cli-adapter
    image: pangaea/provider-claude:2026.05.1
    host_name: snowbox
    account_hint: primary@example.test
    service: claude
    auth:
      mode: file
      bootstrap: copy
      host_path: /srv/pangaea/auth/claude/primary/.credentials.json
      container_path: /var/lib/pangaea/auth/claude/.credentials.json
    shim:
      protocols: [anthropic, openai]
      capabilities:
        - api.anthropic.messages
        - usage.read
        - auth.file
        - auth.refresh.oneshot
    upstream:
      compat: anthropic

  - provider_type: codex-secondary
    kind: cli-container
    provider_mode: http-direct
    image: pangaea/provider-codex:2026.05.1
    host_name: snowbox
    account_hint: secondary@example.test
    service: codex
    auth:
      mode: file
      bootstrap: copy
      host_path: /srv/pangaea/auth/codex/secondary/auth.json
      container_path: /var/lib/pangaea/auth/codex/auth.json
    shim:
      protocols: [openai, anthropic, gemini]
      capabilities:
        - api.openai.chat
        - usage.read
```

`shim.entrypoint` and `shim.command` are passed to the container runtime as the
container entrypoint and command. For same-container MVP images, the entrypoint
SHOULD start the provider local server and the Pangaea provider shim under a
small supervisor, keeping provider HTTP/listen sockets bound to container
loopback or a private network only. The built-in provider images treat
`shim.command` as the provider local server command and keep
`pangaeactl provider-shim run` alive beside it; if either process exits, the
container exits so node-agent can restart or recreate it.

API provider example:

```yaml
providers:
  - provider_type: glm-anthropic
    kind: api-compatible
    image: pangaea/provider-api-compatible:2026.05.1
    service: glm
    upstream:
      base_url: https://api.example.invalid/anthropic
      compat: anthropic
      api_key_mode: bearer
    auth:
      mode: api_key
      bootstrap: copy
      host_path: /srv/pangaea/secrets/glm_api_key
      container_path: /run/pangaea/secrets/glm_api_key
    shim:
      protocols: [anthropic, openai]
      capabilities:
        - api.anthropic.messages
        - api.openai.chat
        - auth.api_key
        - usage.read
```

Sidecar provider example:

```yaml
providers:
  - provider_type: copilot-default
    kind: sidecar-agent
    image: pangaea/provider-github-copilot-sidecar:2026.05.1
    host_name: snowbox
    account_hint: operator@example.test
    service: github-copilot
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

## Auth Bootstrap

Default auth bootstrap is copy, not bind mount.
The source path is provider-specific. It may be the provider CLI's default host
path, but it does not have to be. Operators can keep multiple auth files for
the same service on one host and bind each configured provider to a different
`auth.host_path`.

Flow:

1. Node agent reads the configured `auth.host_path`.
2. Node agent validates minimum format and account hint if possible.
3. Node agent creates or starts provider container.
4. Node agent copies auth file into container temp path.
5. Node agent moves temp file to final path with `0600`.
6. Provider shim validates auth inside container.
7. Shim reports `auth.status`.
8. Router only routes after provider is `ready`.

Host auth files MUST NOT be bind-mounted by default.

When `auth.host_path` is omitted, node-agent may use provider defaults such as
`~/.codex/auth.json`, `~/.claude/.credentials.json`, or
`~/.gemini/oauth_creds.json`. Production configs should prefer explicit paths
when more than one account for the same service exists on a host.

Allowed exception:

- short bootstrap-only read-only mount
- disabled after initial copy
- explicit config flag
- audit event required

## Auth Sync

There are four state holders:

- host auth file
- container auth file
- router/server selected source
- provider runtime memory/session

Container auth may become newer than host auth after refresh. Therefore sync
must be bidirectional but state-machine driven.

Recommended rules:

- Container refresh result becomes candidate source.
- Router compares account, expiry, refreshability, fingerprint, provider-specific validity.
- Host write-back only happens after router accepts container source.
- Conflict creates `auth.status=conflict`; do not overwrite automatically.
- API key providers do not use file truth; they use secret version truth.

Auth report fields:

```json
{
  "mode": "file",
  "host_name": "snowbox",
  "provider_type": "codex-primary",
  "service": "codex",
  "account_id": "user-H8Pbt...",
  "display": "primary@example.test",
  "status": "healthy",
  "expires_at": "2026-05-06T10:19:45Z",
  "refreshable": true,
  "fingerprint": "sha256:...",
  "source": "container",
  "last_refresh_at": "2026-05-05T09:00:00Z",
  "last_refresh_result": "success"
}
```

## Refresh Nudge

Refresh is provider-local and happens inside the container.

Trigger sources:

- shim detects auth is expired or within refresh window
- router sends `auth.refresh.request`
- operator clicks force refresh

Rules:

- mark provider instance `draining` or `refreshing` before refresh
- do not route new traffic to instance while refresh is in progress unless policy allows it
- run refresh command inside container
- wait for auth fingerprint mutation
- validate new auth
- report `auth.refresh.result`
- resume routing only after readiness probe passes

Refresh failures require backoff and audit.

## Container Lifecycle

States:

- `declared`
- `image_missing`
- `creating`
- `bootstrapping_auth`
- `starting`
- `warming`
- `ready`
- `draining`
- `refreshing`
- `updating`
- `stopped`
- `failed`
- `quarantined`

Readiness requires:

- container running
- shim connected
- auth valid or provider does not require auth file
- provider local server/API upstream healthy
- models discovered
- data stream pool ready or fallback data stream available

`container running` alone is not ready.

## Update Lifecycle

Provider CLI/image update sequence:

1. mark instance `draining`
2. wait for active streams or timeout
3. snapshot current container auth/state
4. pull/build new image
5. create replacement container
6. restore auth/state
7. run readiness and warm-up probe
8. shift routing to replacement
9. stop old container
10. keep rollback reference until retention window expires

Automatic `npm install -g ...@latest` inside long-lived production containers is
not preferred. Prefer pinned image versions plus staged rollout.

## Security Defaults

Containers SHOULD run with:

- rootless runtime when available
- non-root user
- `no-new-privileges`
- dropped Linux capabilities
- seccomp/AppArmor/SELinux profile
- read-only root filesystem where possible
- writable paths limited to `/var/lib/pangaea`, `/run/pangaea`, `/tmp`
- no Docker socket mount
- no host auth bind mount after bootstrap
- private network namespace or restricted egress where possible

Secrets:

- auth files `0600`
- owned by provider runtime UID/GID
- excluded from backups
- redacted from logs
- never included in control messages

## Resource Reporting

Node agent reports:

- CPU current/limit
- memory current/limit/peak
- OOM count
- disk usage for provider volume
- container restart count
- process count
- active streams
- queue depth
- provider first-token latency when available

Resource reports should be delta-based with periodic full snapshot.

## Provider Container Contract Tests

Required tests:

- bootstrap copy creates file inside container without host mount
- missing host auth fails safely
- bad file permission is repaired or rejected
- container restart preserves auth
- container recreate restores auth
- refresh command mutates auth and reports result
- refresh failure backs off
- update preserves auth and supports rollback
- readiness blocks routing until shim/provider/auth are ready
- container stats are reported
- provider local server death is detected even when shim stays alive

## Open Questions

- Should provider runtime and shim be same container for v1, or should v1 require sidecar separation?
- Which container runtime abstraction is required first: Docker only, or Docker plus Podman?
- Should auth write-back to host be enabled by default or operator-controlled per provider?
- What is the production policy for provider CLI versions: pinned only, pinned with canary, or optional latest?
