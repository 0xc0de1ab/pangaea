# Testing Strategy Draft

이 문서는 Pangaea monorepo LLM runtime platform의 테스트 전략 초안이다.

현재 구현 문서가 아니라 미래 구조 설계 문서다.

## Goals

- Router-Agent-Shim protocol contract를 provider별 구현보다 먼저 고정한다.
- Provider가 늘어도 같은 contract test를 통과하게 한다.
- Auth, usage, quota, routing, reverse stream이 일관되게 동작함을 보장한다.
- CI는 simulator 중심으로 빠르고 결정적으로 유지한다.
- Nightly는 실제 provider와 container runtime smoke를 담당한다.

## Test Layers

Unit:

- canonical transforms
- routing score
- quota ledger
- auth state transitions
- capability matching
- usage normalization

Contract:

- control protocol schema
- reverse data stream lifecycle
- provider registration
- auth/usage/inventory reports
- compatibility response shape

Integration:

- router + simulator shim
- router + node-agent + simulator container
- auth bootstrap/sync/refresh
- routing/quota concurrency
- streaming cancellation/backpressure

Provider compatibility:

- Codex CLI
- Claude CLI
- Gemini CLI
- GLM API
- MiniMAX API
- DeepSeek API
- Antigravity sidecar
- Cline sidecar
- GitHub Copilot sidecar

E2E/Nightly:

- real Docker/Podman
- real CLI/API smoke
- auth refresh smoke
- image upgrade/rollback
- long-running streaming
- memory/load test

## Provider Simulator

`provider-simulator` is required before real provider work.

Modes:

- `api-compatible`
- `cli-local-server`
- `cli-stdio`
- `sidecar-agent`

Failure injection:

- registration rejected
- heartbeat missing
- stale inventory
- model list drift
- auth expired/revoked/conflict
- stream open timeout
- first-token delay
- slow streaming
- partial response disconnect
- malformed JSON/SSE
- missing usage
- invalid usage
- provider local server crash
- container restart during request

Simulator must support multiple providers for the same service on one host.

## Protocol Contract Tests

Control messages:

- `node.hello`
- `provider.register`
- `provider.heartbeat`
- `provider.inventory.report`
- `provider.auth.report`
- `provider.usage.report`
- `auth.refresh.request`
- `auth.refresh.result`
- `provider.drain`
- `stream.open.request`
- `stream.open.ready`
- `stream.cancel`
- `stream.closed`

Required cases:

- version mismatch
- missing required fields
- unknown optional fields
- duplicate message id idempotency
- stale heartbeat eviction
- reconnect and re-register
- duplicate provider instance handling
- one host with multiple same-service providers
- `host_name` preserved as dashboard/usage identity
- container name not exposed as service provider host

## Reverse Data Stream Tests

Required cases:

- non-streaming HTTP roundtrip
- SSE streaming
- Anthropic event stream
- Gemini streaming chunks
- large request body
- large response body
- slow client backpressure
- slow provider backpressure
- client disconnect cancellation
- router timeout
- provider timeout
- half-close
- duplicate stream id rejected
- expired stream token rejected
- wrong provider token rejected
- shim no response after stream open
- network disconnect mid-stream
- orphan provider cleanup

Performance assertions:

- stream acquire p95
- first byte p95
- first token p95
- active stream memory growth
- cancellation propagation latency

## Container Lifecycle Tests

Required:

- image pull/build success and failure
- create/start/stop/restart/remove
- rootless runtime
- non-root user
- read-only rootfs
- seccomp/AppArmor profile
- Docker socket not exposed
- provider local server dead but shim alive
- shim disconnected but container alive
- restart preserves auth
- recreate restores auth
- upgrade drain and rollback
- warm-up probe
- CLI/image/shim version mismatch

Auth bootstrap:

- default auth path copy
- explicit auth path copy
- same host same service multi-account copy
- missing host path
- permission denied
- destination mode `0600`
- wrong UID/GID handling
- no host bind mount after copy
- secret redaction

## Auth Sync And Refresh Tests

States:

- `unknown`
- `healthy`
- `refresh_soon`
- `refreshing`
- `expired`
- `revoked`
- `conflict`
- `unavailable`

Required:

- bootstrap report
- container refresh to server/host propagation
- host change to container reconcile
- server selected source change
- multi-host conflict resolution
- expired id_token with valid access_token
- revoked refresh token
- refresh window oneshot
- refresh success mutation
- refresh failure backoff
- refresh drains routing
- fallback during refresh
- refresh and update mutual exclusion

## Provider Compatibility Tests

Common:

- `/v1/models`
- OpenAI chat
- Anthropic messages
- Gemini generateContent
- streaming true/false
- system prompt
- multi-turn messages
- tool calls/results
- JSON mode unsupported handling
- multimodal unsupported handling
- max tokens
- temperature/top_p
- stop sequences
- provider error mapping
- rate limit mapping
- auth error mapping
- usage normalization
- model alias mapping

Provider-specific:

- API providers: response shape drift, missing usage, 429/5xx, model mismatch
- CLI providers: local server readiness, CLI version change, cancellation,
  stderr redaction, unexpected login prompt
- Sidecar providers: workspace capability, job cancel, tool permission,
  code completion, long-running heartbeat

## Quota And Routing Concurrency Tests

Required:

- API key hash auth
- disabled/expired key reject
- user/model allowlist
- route dry-run
- provider health/auth/quota constraints
- provider concurrency limit
- fallback chain
- no route available
- per-user/key/model quota reserve
- provider native limit
- streaming incremental reservation
- cancel release
- provider error reconcile
- retry idempotency
- 100/1000 concurrent race
- router restart in-flight recovery

Invariants:

- quota balance does not go negative
- one request commits once
- retry does not double-charge within idempotency scope
- route trace links to usage ledger
- provider-reported and router-estimated usage are preserved

## Observability Tests

Every request/event carries:

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
- `stream_id`

Redaction tests:

- access token
- refresh token
- API key
- Authorization header
- cookie
- prompt body default not stored
- provider stderr redaction

## CI And Nightly

CI:

- unit tests
- schema tests
- simulator contract
- router + simulator integration
- quota concurrency
- auth state machine
- canonical transform golden tests
- redaction tests
- lightweight container simulator
- race detector target packages

Nightly:

- Docker/Podman matrix
- Codex/Claude/Gemini container smoke
- GLM/MiniMAX/DeepSeek API smoke
- real streaming smoke
- auth refresh smoke
- image upgrade/rollback
- long-running cancellation
- memory growth/load test
- model list drift detection

Release gate:

- protocol contract tests
- simulator e2e
- quota concurrency
- redaction
- one CLI provider smoke
- one API provider smoke
- migration and rollback tests
