# Implementation Roadmap Draft

이 문서는 Pangaea monorepo LLM runtime platform 전환을 위한 단계별 구현
로드맵이다.

현재 구현 문서가 아니라 미래 작업 계획 초안이다.

## Phase 0: Documentation And Contracts

Deliverables:

- provider protocol v1 draft
- container spec v1 draft
- routing policy v1 draft
- module layout draft
- provider docs under `docs/providers`
- protocol fixtures
- provider simulator design

Exit criteria:

- DesignTeam review comments are reflected.
- Router/node-agent/shim vocabulary is stable.
- Provider kinds and capability names are stable enough for MVP.

## Phase 1: Canonical Compat Core

Deliverables:

- `internal/compat` package
- canonical request/response/event model
- OpenAI chat transform
- Anthropic messages transform
- Gemini generateContent transform
- streaming event transform
- unsupported feature errors

Sources to reuse:

- `/workspace/cli-sidecar/internal/transformer`
- existing `*-compat-proxy/internal/transcoder`

Exit criteria:

- golden tests for OpenAI, Anthropic, Gemini conversions
- tool call and multimodal unsupported cases explicit
- no provider-specific routing logic in compat package

## Phase 2: Control Protocol And Provider Simulator

Deliverables:

- `internal/control`
- provider/node session handshake
- provider registration
- heartbeat and stale eviction
- provider simulator
- contract test suite

Exit criteria:

- simulator can register multiple providers on one host
- simulator can report two accounts for same service
- simulator can emulate auth states, usage, health, stream errors
- contract tests run in CI

## Phase 3: Data Plane MVP

Deliverables:

- `internal/tunnel`
- stream ids and scoped capability tokens
- raw HTTP data stream
- cancellation
- bounded buffers
- initial `ws-mux` or `h2-connect` transport

Exit criteria:

- non-streaming request roundtrip
- SSE streaming roundtrip
- client disconnect cancels provider execution
- backpressure test exists

## Phase 4: Router Core

Deliverables:

- public `/v1/chat/completions`
- public `/v1/models`
- user/API key authentication
- route policy loading
- provider registry
- route dry-run API
- usage ledger skeleton
- audit event skeleton

Exit criteria:

- public OpenAI-compatible request routes to simulator
- route trace explains candidate filtering
- API key stored as hash, not raw
- quota reservation/commit/release works for simulator

## Phase 5: API-Compatible Provider

Deliverables:

- generic `api-compatible` provider shim
- OpenAI-compatible upstream mode
- Anthropic-compatible upstream mode
- Gemini-compatible upstream mode if needed
- GLM/MiniMAX/DeepSeek provider docs

Exit criteria:

- one real or mock Anthropic-compatible upstream works
- upstream errors normalize to router error model
- model and usage metadata are reported
- API key secret reload path exists

## Phase 6: Node Agent Container Runtime

Deliverables:

- `cmd/node-agent`
- Docker runtime implementation
- provider spec reconciliation
- auth bootstrap copy
- container report
- resource stats

Exit criteria:

- node-agent can create/start/stop simulator provider container
- auth source path may be explicit per provider
- one host can run multiple providers for same service
- dashboard/registry sees `host_name`, not container name, as provider host

## Phase 7: Codex CLI Provider MVP

Deliverables:

- Codex provider image
- Codex provider shim
- auth bootstrap from explicit `auth.host_path`
- `CODEX_HOME` isolated path
- Codex oneshot refresh nudge inside container
- Codex model/usage/auth report
- OpenAI chat route via router

Exit criteria:

- two Codex accounts on one host can run as distinct provider instances
- expired id_token with valid access_token stays routable
- refresh window triggers container-local oneshot
- container auth refresh can write back through authsync policy

## Phase 8: Dashboard MVP

Deliverables:

- Overview
- Routes
- Providers
- Nodes/Containers
- Auth
- Users/API Keys
- Usage
- Events/Audit

Exit criteria:

- operator can see provider host, service, account, auth, usage, health
- route dry-run visible
- request trace visible
- dangerous actions require confirmation and audit reason

## Phase 9: Additional Providers

Order:

1. Claude CLI provider
2. Gemini CLI provider
3. Antigravity sidecar provider
4. Cline sidecar provider
5. GitHub Copilot sidecar provider
6. More API providers

Each provider must pass:

- provider protocol contract
- compat golden tests
- auth lifecycle tests if auth-managed
- stream cancellation tests
- usage reporting tests where applicable

## Phase 10: Legacy Cleanup

Deliverables:

- current auth sync commands marked legacy
- old docs linked under legacy/current implementation
- duplicate provider proxy code removed or archived
- migration guide

Exit criteria:

- existing deployments still have a supported path
- new deployments use router/node-agent/provider-shim model
- docs clearly distinguish current/legacy from future platform

## Cross-Cutting Gates

Security:

- per-node and per-shim identity
- stream capability token
- API key hashing
- redaction tests
- audit schema
- hardened container defaults

Performance:

- warm stream pool or explicit fallback design
- stream acquire latency metric
- first token latency metric
- bounded buffers
- pprof/admin diagnostics

Testing:

- provider simulator
- protocol contract tests
- routing/quota concurrency tests
- container lifecycle tests
- nightly real-provider smoke tests

UX:

- route dry-run
- request trace
- provider/host/account usage dashboard
- safe controls for drain/restart/refresh/update
