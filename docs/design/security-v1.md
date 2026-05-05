# Security Design v1 Draft

이 문서는 Pangaea monorepo LLM runtime platform의 보안 설계 초안이다.

현재 구현 문서가 아니라 미래 구조 설계 문서다.

## Security Boundary

보안 경계는 container 그 자체가 아니다. 핵심 경계는 다음 세 가지다.

- router-issued capability
- node/shim/provider instance identity
- audit 가능한 quota ledger

Container isolation은 보조 방어선이다. Control plane 권한과 data plane 권한은
분리해야 한다.

## Identities

모든 구성 요소는 고유 identity를 가진다.

- `router_id`
- `node_id`
- `host_name`
- `shim_id`
- `provider_instance_id`
- `container_id`
- `service`
- `account_id`
- `tenant_id`
- `user_id`
- `api_key_id`

`host_name`은 operator-facing service providing host다. Container 이름으로
대체하면 안 된다.

## Enrollment

Node enrollment:

1. Operator creates short-lived enrollment token.
2. Node agent presents token to router.
3. Node agent generates long-term key pair.
4. Router binds node public key/certificate to `node_id` and `host_name`.
5. Enrollment token is revoked.

Shim enrollment:

1. Node agent creates provider instance manifest.
2. Shim starts with manifest-bound identity.
3. Router verifies shim belongs to enrolled node/provider instance.
4. Arbitrary shim direct registration is rejected.

## Control Channel Authentication

Production default is mTLS.

Allowed alternatives:

- Ed25519 challenge-response
- signed short-lived node/shim token for constrained environments

Dev-only insecure modes must be explicit and visible in logs/UI.

Control messages must include:

- protocol version
- message id
- timestamp
- principal identity
- nonce or replay protection field

Router keeps replay cache for recent nonces/message ids.

## Data Stream Capability Tokens

Data stream authorization is separate from control channel authentication.

Each stream token is scoped to:

- `request_id`
- `stream_id`
- `route_id`
- `tenant_id`
- `user_id`
- `provider_instance_id`
- `model`
- `direction`
- `deadline`
- `nonce`

Rules:

- default TTL: 10-30 seconds
- one token per stream
- no token reuse
- wrong provider/model/tenant is rejected
- data stream cannot be opened by control auth alone

## Auth Bootstrap Copy

Provider auth is copied into container by default.

Rules:

- `auth.host_path` is provider-instance specific.
- default provider CLI paths may be used only when explicit path is omitted.
- explicit paths are required for reliable multi-account hosts.
- copied auth is stored under provider-specific container path.
- file mode should be `0600`.
- owner UID/GID should be provider runtime user.
- host bind mount is disabled by default.

Audit records may include source/destination paths and fingerprints, never file
contents.

## Secret Storage

Router stores:

- API key hashes
- enrollment token hashes
- provider secret references
- node/shim public key or certificate metadata
- quota ledger signing/audit metadata

Node agent stores:

- node private key
- router credential
- local provider auth snapshots if writeback is enabled
- temporary bootstrap files

Shim/container stores:

- provider auth copy
- provider API key if API provider
- short-lived stream tokens

Secrets must not appear in logs, metrics, traces, control messages, or audit
details.

Redaction keys:

- `authorization`
- `cookie`
- `x-api-key`
- `api_key`
- `access_token`
- `refresh_token`
- `id_token`
- `client_secret`
- `set-cookie`

## Hardened Container Profile

Default profile:

- non-root user
- rootless runtime preferred
- `no-new-privileges`
- drop all Linux capabilities
- no Docker socket mount
- read-only root filesystem where possible
- writable paths limited to auth/state/cache/tmp
- seccomp/AppArmor/SELinux profile
- network egress allowlist when practical
- provider local server on loopback/private network
- CPU/memory/pids limits

High-risk providers with workspace, shell, browser, git, or tool access require
explicit elevated capability and UI warning.

## Reverse Data Stream Authorization

Flow:

1. Router authenticates user/API key.
2. Router reserves quota.
3. Router selects provider.
4. Router issues scoped stream token.
5. Router sends `stream.open.request`.
6. Shim validates token and provider state.
7. Data stream opens.
8. Cancel/timeout/close are audited.
9. Quota is committed or released.

Shim must reject:

- expired token
- wrong provider instance
- wrong tenant/user/model
- reused nonce
- stream for drained/quarantined provider
- unsigned or malformed assignment

Forbidden:

- generic arbitrary tunnel
- command execution by raw control message
- direct client access to provider local server
- provider bypassing router for response path

## API Key Security

Raw API keys are never stored.

Recommended model:

- key id and prefix for lookup/display
- Argon2id hash with server-side pepper
- one-time reveal on creation
- expiration support
- rotation/revocation support
- model allowlist
- quota policy binding

## Quota Ledger Security

Quota is an append/audit-friendly ledger.

Lifecycle:

- `reserve`
- `increment`
- `commit`
- `release`
- `adjust`

Ledger keys include:

- tenant/user/API key
- request id
- idempotency key
- model
- provider instance
- provider account
- route id

Security invariants:

- no negative balance
- one request commits once
- retries do not double-charge unless multiple provider executions happened
- provider native usage and router estimated usage remain separate

## Audit Events

Required audit events:

- node enrollment
- shim/provider registration
- provider drain/disable
- auth bootstrap copy
- auth refresh request/result
- auth conflict
- stream open/close/cancel
- route decision
- quota reserve/commit/release
- API key create/revoke/rotate
- admin config publish/rollback
- image update/rollback
- container restart/recreate
- security policy violation

Audit must include actor, target, reason, result, timestamp, `host_name`,
`provider_instance_id`, and request/route ids where applicable.

Prompt and response bodies are not stored by default.

## Supply Chain And Update Policy

Production policy:

- no automatic `latest` updates
- pinned provider image versions
- image digest preferred
- staged rollout: canary, partial, full
- rollback image retained
- update requires drain
- update requires auth snapshot/restore
- post-update readiness and smoke probe
- audit every update

Runtime self-update requires explicit capability:

- `provider.update.runtime`

and admin approval with reason.

## Provider Threat Notes

### Codex CLI

- access token validity and id token expiry must be handled separately.
- refresh oneshot runs only inside container.
- logs and command output require strict redaction.

### Claude CLI

- account/subscription expiry and credential validity are separate.
- workspace access moves provider into agent/workspace risk class.

### Gemini CLI

- Node/npm version is pinned in image.
- explicit auth paths are required for multi-account hosts.
- login-shell fallback commands must be allowlisted.

### API Providers

GLM, MiniMAX, DeepSeek and similar providers:

- secret reload and API key rotation are primary auth operations.
- upstream base URLs should be allowlisted.
- compatible API response drift must be tested.

### Sidecar Providers

Antigravity, Cline, GitHub Copilot:

- workspace and terminal capabilities are high-risk.
- workspace mount is read-only by default.
- write/terminal/tool access requires explicit route policy.

## Production Gate

Production traffic requires:

- node/shim identity
- authenticated control channel
- scoped data stream token
- API key hashing
- quota ledger
- redaction tests
- hardened container profile
- auth bootstrap audit
- update rollback path
- route decision audit
- provider simulator security contract tests
