# Provider Protocol v1 Draft

이 문서는 Pangaea를 containerized LLM runtime router로 확장하기 위한
Router, Node Agent, Provider Shim 사이의 표준 계약 초안이다.

현재 구현 문서가 아니라 설계 합의용 draft다.

## Goals

- provider 구현 방식과 router routing 정책을 분리한다.
- CLI, 외부 API, local sidecar provider를 같은 contract로 다룬다.
- WebSocket은 control plane으로 제한하고, 요청/응답 body는 data plane으로 처리한다.
- provider 추가 시 router core 수정 없이 capability와 shim 구현만 추가한다.
- auth, usage, quota, routing, audit를 provider별 예외가 아닌 공통 모델로 처리한다.

## Roles

### Router

- public OpenAI, Anthropic, Gemini compatible API를 제공한다.
- user, API key, quota, route policy, audit log를 관리한다.
- node, container, provider registry를 유지한다.
- request를 canonical form으로 정규화하고 provider instance를 선택한다.
- selected shim으로 reverse data stream을 연결해 요청을 처리한다.

### Node Agent

- 각 host에서 container lifecycle을 관리한다.
- provider container 생성, auth bootstrap copy, restart, drain, update를 담당한다.
- host/container resource와 runtime 상태를 router에 보고한다.
- Docker/Podman 같은 runtime 권한은 node agent에만 둔다.

### Provider Shim

- provider runtime을 표준 control/data protocol로 router에 노출한다.
- provider local server, CLI stdio/pty, 외부 API endpoint, sidecar relay를 감싼다.
- auth 상태, model, usage, health, capability를 보고한다.
- refresh nudge, local server restart 같은 provider-local action을 수행한다.

### Provider Runtime

- 실제 LLM service runtime이다.
- 예: Codex CLI app-server, Claude CLI, Gemini CLI, Antigravity sidecar,
  Cline relay, GitHub Copilot sidecar, GLM/MiniMAX/DeepSeek API egress shim.

## Provider Kinds

Provider kind는 구현 방식이다. Router는 kind 자체로 routing하지 않고 capability를 본다.

- `cli-container`: container 안에서 CLI/local server를 실행한다.
- `api-compatible`: 외부 OpenAI/Anthropic/Gemini-compatible API를 호출한다.
- `sidecar-agent`: IDE/agent/sidecar runtime을 relay한다.
- `simulator`: contract test와 load test용 fake provider다.

예:

- `claude-cli-provider`: `cli-container`
- `codex-cli-provider`: `cli-container`
- `gemini-cli-provider`: `cli-container`
- `claude-api-provider`: `api-compatible`
- `glm-api-provider`: `api-compatible`
- `minimax-api-provider`: `api-compatible`
- `deepseek-api-provider`: `api-compatible`
- `antigravity-sidecar-provider`: `sidecar-agent`
- `github-copilot-sidecar-provider`: `sidecar-agent`
- `cline-sidecar-provider`: `sidecar-agent`

## Capabilities

Provider는 brand가 아니라 capability set으로 등록된다.

API capabilities:

- `api.openai.chat`
- `api.openai.responses`
- `api.openai.embeddings`
- `api.anthropic.messages`
- `api.gemini.generateContent`
- `api.gemini.streamGenerateContent`

Agent capabilities:

- `agent.tool_use`
- `agent.workspace.read`
- `agent.workspace.write`
- `agent.terminal`
- `agent.mcp`
- `code.completion`

Operational capabilities:

- `usage.read`
- `models.read`
- `auth.file`
- `auth.api_key`
- `auth.refresh.oneshot`
- `auth.refresh.provider_protocol`
- `stream.sse`
- `stream.bidirectional`

Provider-specific capabilities MAY be added with reverse-DNS style names:

- `provider.codex.app_server`
- `provider.gemini.acp`
- `provider.cline.extension_relay`

Router MUST only rely on standardized capabilities for generic routing.

## Identities

All control and data messages carry stable identifiers.

- `tenant_id`: router-side organization or user group.
- `user_id`: router-side user.
- `api_key_id`: router-side key prefix/hash identity, never raw secret.
- `node_id`: host running node-agent.
- `host_name`: human-readable service providing host name. This is the
  operator-facing provider location and MUST NOT be replaced by container name.
- `container_id`: runtime container identity.
- `provider_type`: logical configured provider.
- `provider_instance_id`: concrete connected shim instance.
- `account_id`: provider account identity if known.
- `model_id`: provider native model id.
- `route_id`: selected route policy version.
- `request_id`: public request trace id.
- `stream_id`: data stream id.

## Control Plane

Control plane uses long-lived WebSocket sessions.

- Router exposes `/control/v1/node` for node agents.
- Router exposes `/control/v1/provider` for provider shims.
- Sessions authenticate with mTLS or an equivalent challenge-response identity.
- Enrollment tokens are one-time bootstrap credentials only.
- Control frames MUST NOT carry prompt body, response body, provider auth tokens, or API keys.

### Envelope

All control messages use a versioned envelope.

```json
{
  "version": "provider-protocol/v1",
  "type": "provider.register",
  "id": "msg_01J...",
  "sent_at": "2026-05-05T00:00:00Z",
  "trace": {
    "request_id": "",
    "route_id": "",
    "node_id": "a1",
    "provider_instance_id": "codex-primary/a1/01"
  },
  "payload": {}
}
```

Required envelope fields:

- `version`
- `type`
- `id`
- `sent_at`
- `payload`

Unknown message types MUST be ignored unless the message is marked required by
the negotiated protocol version.

## Handshake

### Node Agent

Node agent connects first and reports host/runtime inventory.

```text
node.hello
node.hello.ack
node.inventory.report
container.report
```

`node.hello` payload:

```json
{
  "node_id": "a1",
  "agent_version": "v1.0.0",
  "os": "linux",
  "arch": "arm64",
  "runtime": {
    "kind": "docker",
    "version": "26.1.0",
    "rootless": true
  },
  "capabilities": [
    "container.create",
    "container.exec",
    "container.cp",
    "container.stats"
  ]
}
```

### Provider Shim

Provider shim connects from inside or near a provider container.

```text
provider.hello
provider.hello.ack
provider.register
provider.inventory.report
provider.auth.report
provider.usage.report
provider.heartbeat
```

`provider.register` payload:

```json
{
  "provider_type": "codex-primary",
  "provider_instance_id": "codex-primary/a1/01",
  "kind": "cli-container",
  "node_id": "a1",
  "host_name": "snowbox",
  "container_id": "docker://...",
  "service": "codex",
  "account": {
    "id": "user-H8Pbt...",
    "email": "primary@example.test",
    "display": "primary@example.test"
  },
  "capabilities": [
    "api.openai.chat",
    "api.anthropic.messages",
    "api.gemini.generateContent",
    "usage.read",
    "auth.file",
    "auth.refresh.oneshot",
    "stream.sse"
  ],
  "models": [
    {
      "id": "gpt-5.3-codex-spark",
      "aliases": ["gpt-5.3", "codex-spark"],
      "capabilities": ["api.openai.chat", "stream.sse"],
      "context_window": 200000,
      "quality_tier": "coding"
    }
  ],
  "auth": {
    "status": "ok",
    "expires_at": "2026-05-06T10:19:45Z",
    "refreshable": true
  },
  "limits": {
    "max_concurrency": 2,
    "queue_depth": 0,
    "remaining_fraction": 0.68,
    "reset_at": "2026-05-05T12:50:21Z"
  },
  "health": {
    "state": "ready",
    "reason": "",
    "ready_since": "2026-05-05T00:00:00Z"
  }
}
```

`host_name` is the service providing host shown in dashboards and usage views.
It must identify the physical or VM node, not the provider container. A single
`node_id`/`host_name` pair may report multiple providers for the same service,
for example two Codex accounts on one host:

```json
[
  {
    "provider_type": "codex-primary",
    "service": "codex",
    "node_id": "snowbox",
    "host_name": "snowbox",
    "account": {"email": "primary@example.test"}
  },
  {
    "provider_type": "codex-secondary",
    "service": "codex",
    "node_id": "snowbox",
    "host_name": "snowbox",
    "account": {"email": "secondary@example.test"}
  }
]
```

## State Reports

### Health States

- `starting`: process/container exists but is not ready.
- `ready`: can accept routed traffic.
- `busy`: alive but cannot accept more traffic now.
- `draining`: existing requests may finish; new requests must not be routed.
- `degraded`: usable only if policy permits fallback-to-degraded.
- `unavailable`: not routable.
- `quarantined`: protocol/security/compat failure; admin action required.

### Auth States

- `healthy`: valid and routable.
- `refresh_soon`: valid but inside provider refresh window.
- `refreshing`: refresh nudge or provider refresh is in progress.
- `expired`: not routable until refreshed.
- `revoked`: refresh is not expected to work.
- `unknown`: cannot validate.
- `conflict`: multiple incompatible auth states exist for same account.

### Auth Snapshot / Push

`auth.snapshot` is a redacted provider-to-router auth state report. It carries
status, account identity, expiry, refreshability, selected source, and optional
fingerprint metadata. It MUST NOT carry raw OAuth tokens, API keys, refresh
tokens, credential file bytes, or provider cookies.

```json
{
  "provider_instance_id": "codex-primary/a1/01",
  "account_id": "user-H8Pbt",
  "auth": {
    "status": "refresh_soon",
    "account": {"id": "user-H8Pbt", "display": "primary@example.test"},
    "expires_at": "2026-05-06T10:19:45Z",
    "refreshable": true,
    "selected_source": "container"
  },
  "fingerprint": "sha256:...",
  "source": "container",
  "observed_at": "2026-05-05T00:00:00Z",
  "reported_at": "2026-05-05T00:00:00Z"
}
```

`auth.push` is a router-to-shim control command that updates the shim's redacted
auth mirror and asks it to publish a fresh `auth.snapshot`. It is metadata-only;
credential file synchronization remains a node-agent/container runtime action
such as explicit bootstrap copy or configured host/container auth sync.

### Inventory Reports

Inventory reports may be full or delta.

```json
{
  "mode": "delta",
  "node_id": "snowbox",
  "host_name": "snowbox",
  "providers": [],
  "containers": [],
  "resources": {
    "cpu_percent": 17.5,
    "memory_bytes": 762314752,
    "memory_peak_bytes": 1023410176,
    "oom_count": 0
  }
}
```

Agents and shims SHOULD send jittered heartbeat deltas and periodic full snapshots.

## Data Plane

The data plane carries request and response bytes.

Control WebSocket only triggers or assigns streams. Data streams are separate
bidirectional byte streams with backpressure and cancellation.

Allowed transports:

- `reverse-tcp`: shim opens a TCP connection back to router.
- `h2-connect`: HTTP/2 CONNECT-style reverse stream.
- `ws-mux`: multiplexed WebSocket data stream for MVP/testing only.
- `grpc-stream`: future option.

The protocol contract is transport independent:

- ordered bytes in both directions
- half-close support
- cancellation support
- bounded buffering
- stream-level deadline
- stream-level capability token

## Stream Lifecycle

Preferred flow uses warm reverse stream pools.

```text
provider.stream_pool.request
provider.stream_pool.ready
router receives public request
router selects provider instance
router assigns idle stream
stream.open.request
stream.open.ready
data stream carries HTTP request/response or canonical framed request
stream.closed
quota commit/release
audit event
```

Per-request stream creation is allowed only as fallback.

### Stream Open Request

```json
{
  "stream_id": "str_01J...",
  "request_id": "req_01J...",
  "route_id": "route_codex_primary_v3",
  "provider_instance_id": "codex-primary/a1/01",
  "tenant_id": "team-a",
  "user_id": "usr_123",
  "model": "gpt-5-codex",
  "deadline_at": "2026-05-05T00:01:30Z",
  "protocol": "http.raw",
  "capability_token": "..."
}
```

`capability_token` MUST be scoped to:

- `stream_id`
- `request_id`
- `provider_instance_id`
- `tenant_id`
- `user_id`
- `model`
- `direction`
- `deadline_at`
- `nonce`

Tokens SHOULD expire within 10-30 seconds unless they are bound to a warm stream
pool lease.

## Request Payload Forms

Two payload forms are allowed.

### Raw HTTP

Router sends the already accepted public HTTP request to shim as raw HTTP bytes.
Shim handles provider-local compat conversion.

Use when:

- provider shim owns compat conversion
- sidecar already exposes OpenAI/Anthropic/Gemini-compatible HTTP
- router is acting as streaming proxy

### Canonical Request

Router converts public request to a canonical request and sends framed canonical
events over the data stream.

Use when:

- multiple public API dialects map to the same provider capability
- route policy needs canonical feature inspection
- contract tests target provider behavior independent of public API dialect

Long term preference is canonical request at provider boundary and public API
compat in shared router/compat packages.

## Canonical Model

Canonical request/response/event types should be based on `cli-sidecar`'s
OpenAI/Anthropic/Gemini transformer model, extended for:

- tool calls and tool results
- multimodal content blocks
- reasoning/thinking blocks without exposing hidden chain-of-thought by default
- streaming usage events
- provider-specific metadata under `extensions`
- unsupported capability reporting

Provider shims MUST return explicit unsupported-feature errors instead of
silently dropping unsupported content.

## Control Messages

Required v1 messages:

- `node.hello`
- `node.hello.ack`
- `node.inventory.report`
- `container.report`
- `provider.hello`
- `provider.hello.ack`
- `provider.register`
- `provider.heartbeat`
- `provider.inventory.report`
- `provider.auth.report`
- `provider.usage.report`
- `provider.drain`
- `auth.snapshot`
- `auth.push`
- `auth.refresh.request`
- `auth.refresh.result`
- `stream.open.request`
- `stream.open.ready`
- `stream.cancel`
- `stream.closed`
- `error`

## Error Model

Control-plane errors:

```json
{
  "code": "provider_unavailable",
  "message": "provider local server is not ready",
  "retryable": true,
  "details": {
    "provider_instance_id": "codex-primary/a1/01"
  }
}
```

Standard error codes:

- `version_unsupported`
- `capability_unsupported`
- `provider_unavailable`
- `provider_busy`
- `auth_expired`
- `auth_revoked`
- `quota_exhausted`
- `stream_token_invalid`
- `stream_open_timeout`
- `stream_cancelled`
- `deadline_exceeded`
- `upstream_error`
- `protocol_violation`

## Security Requirements

- Control sessions MUST authenticate node/shim identity.
- Data streams MUST use scoped capability tokens.
- Raw provider auth tokens MUST NOT appear in control messages, logs, metrics, or audit.
- Shim MUST reject unsigned or expired stream assignments.
- Refresh/update/restart/drain actions REQUIRE explicit capability and audit events.
- User API keys MUST be stored as prefix plus strong hash, never raw.
- Router MUST record quota reservation, commit, release, and selected provider.
- Prompt and response bodies are not stored by default.

## Observability

All request paths MUST carry:

- `request_id`
- `route_id`
- `tenant_id`
- `user_id`
- `api_key_id`
- `node_id`
- `host_name`
- `container_id`
- `provider_type`
- `provider_instance_id`
- `account_id`
- `model`

Minimum metrics:

- route decision latency
- stream acquire latency
- provider first byte/token latency
- provider total latency
- active streams
- queue depth
- tokens/sec when available
- retry count
- cancel/abort reason
- provider error rate
- container memory current/peak/OOM count

## Contract Tests

Provider protocol v1 is not complete until these tests exist:

- provider simulator handshake
- version negotiation and unknown message handling
- provider register/heartbeat/inventory/auth/usage reports
- stream open/ready/cancel/closed
- raw HTTP non-streaming
- SSE streaming with backpressure
- client disconnect cancellation
- provider timeout and retryable errors
- quota reservation and idempotent commit
- auth refresh request/result state transitions
- malformed provider reports rejected without router crash

## Open Questions

- Data plane v1 should start with `ws-mux` for implementation speed or
  `h2-connect` for closer production behavior?
- Should compat conversion live only in router/shared package, or may shim keep
  provider-specific public API endpoints for local debugging?
- How much provider-native metadata should be surfaced to users vs operator-only UI?
- What is the first API provider MVP target: GLM, MiniMAX, DeepSeek, or a local simulator?
