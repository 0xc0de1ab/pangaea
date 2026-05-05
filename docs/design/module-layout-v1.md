# Module Layout v1 Draft

이 문서는 Pangaea monorepo의 상세 module/package 구조 초안이다.

현재 구현 문서가 아니라 미래 구조 설계 문서다.

## Repository Shape

```text
cmd/
  router/
  node-agent/
  provider-shim/
  providerctl/
  pangaeactl/              # legacy/current CLI compatibility

internal/
  control/
  tunnel/
  router/
  agent/
  runtime/
    docker/
    podman/
  provider/
  compat/
  authsync/
  usage/
  quota/
  store/
  audit/
  observability/
  legacy/

pkg/
  formats/                 # current auth format parsers
  protocol/                # stable external structs if needed

providers/
  codex/
  claude/
  gemini/
  api/
  antigravity/
  cline/
  githubcopilot/
  simulator/

web/
  router-ui/

docs/
  design/
  providers/
```

## Commands

### `router`

Runs central router.

Responsibilities:

- public compatible API
- admin API
- control WebSocket endpoints
- data stream broker
- route policy
- user/API key/quota
- provider registry
- UI static assets

### `node-agent`

Runs on service providing hosts.

Responsibilities:

- connect to router control plane
- manage provider containers
- bootstrap auth
- report node/container resource state
- execute lifecycle commands from router

### `provider-shim`

Runs inside or beside provider container.

Responsibilities:

- connect to router control plane
- register provider capabilities
- bridge provider native runtime
- handle assigned data streams
- report auth/model/usage/health

Provider-specific implementations can be built into one binary and selected by
config, or built as separate images using the same binary.

### `providerctl`

Operator/debug CLI.

Responsibilities:

- validate provider spec
- run route dry-run
- inspect provider registration
- test auth bootstrap
- run local provider simulator
- print protocol fixtures

### `pangaeactl`

Current CLI remains for compatibility during migration.

Long term it can become a compatibility alias or a subcommand set under the new
CLI, but current deploys should not be broken during platform migration.

## Core Packages

### `internal/control`

Control plane protocol.

Contents:

- envelope structs
- message type registry
- version negotiation
- node/shim session manager
- heartbeat and stale eviction
- protocol validation
- JSON schema/protobuf generation hooks

### `internal/tunnel`

Data plane.

Contents:

- reverse stream abstraction
- warm stream pool
- stream lease/capability token verification
- raw HTTP tunneling
- canonical framed request stream
- cancellation and half-close handling
- backpressure tests

### `internal/router`

Public router logic.

Contents:

- OpenAI/Anthropic/Gemini handlers
- route decision engine
- request trace
- provider candidate filtering
- fallback handling
- route dry-run API

### `internal/agent`

Node agent runtime.

Contents:

- desired state reconciliation
- container lifecycle state machine
- auth bootstrap orchestration
- runtime inventory reporting
- provider container update/drain/restart

### `internal/runtime`

Container runtime abstraction.

Initial interface:

```go
type Runtime interface {
    Pull(ctx context.Context, image string) error
    Create(ctx context.Context, spec ContainerSpec) (ContainerID, error)
    Start(ctx context.Context, id ContainerID) error
    Stop(ctx context.Context, id ContainerID, timeout time.Duration) error
    Remove(ctx context.Context, id ContainerID) error
    Exec(ctx context.Context, id ContainerID, cmd ExecSpec) (ExecResult, error)
    CopyTo(ctx context.Context, id ContainerID, src, dst string, mode FileMode) error
    CopyFrom(ctx context.Context, id ContainerID, src, dst string) error
    Stats(ctx context.Context, id ContainerID) (ContainerStats, error)
    Logs(ctx context.Context, id ContainerID, opts LogOptions) (io.ReadCloser, error)
}
```

Docker is first implementation. Podman follows behind the same interface.

### `internal/provider`

Provider model and interfaces.

Core interface:

```go
type Provider interface {
    Identity() ProviderIdentity
    Capabilities(context.Context) ProviderCapabilities
    Health(context.Context) Health
    Models(context.Context) ([]Model, error)
    Usage(context.Context) (UsageReport, error)
    Invoke(context.Context, CanonicalRequest) (CanonicalResponse, error)
    Stream(context.Context, CanonicalRequest) (<-chan CanonicalEvent, error)
}
```

Optional interfaces:

- `AuthManagedProvider`
- `ContainerManagedProvider`
- `HTTPUpstreamProvider`
- `LocalServerProvider`
- `AgentExecutorProvider`
- `CodeCompletionProvider`

### `internal/compat`

Canonical model and public API transforms.

Sources:

- `cli-sidecar/internal/transformer`
- existing `*-compat-proxy/internal/transcoder`

Responsibilities:

- OpenAI -> canonical
- Anthropic -> canonical
- Gemini -> canonical
- canonical -> OpenAI
- canonical -> Anthropic
- canonical -> Gemini
- unsupported feature errors
- streaming event transforms

### `internal/authsync`

Auth file and secret state machine.

Responsibilities:

- provider-specific auth parse/validate
- host/container/router truth comparison
- bootstrap copy
- container-to-host writeback
- conflict detection
- refresh result processing

It should reuse `pkg/formats/*`.

### `internal/usage`

Provider usage collection and normalization.

Responsibilities:

- provider-native usage probes
- router-side estimated usage
- usage window normalization
- dashboard aggregates

### `internal/quota`

Router-side quota ledger.

Responsibilities:

- reserve
- commit
- release
- reconcile
- idempotency keys
- per-user/key/model/provider quota dimensions

### `internal/store`

Persistence abstraction.

MVP can use SQLite. Schema should not block later PostgreSQL.

Stores:

- users
- API keys
- route policies
- provider registry snapshots
- usage ledger
- audit events
- node/container inventory

### `internal/audit`

Structured audit events.

No prompt/response body by default.

### `internal/observability`

Metrics, tracing, pprof/admin diagnostics, log redaction.

### `internal/legacy`

Compatibility layer for current auth sync behavior.

Current `connect`, `serve`, `reverse-client`, `reverse-connect`, `status`,
notifiers, and auth mediator can remain here during migration.

## Provider Packages

### `providers/simulator`

Required first provider for tests.

Capabilities:

- fake model list
- fake usage
- configurable latency/error
- streaming and non-streaming
- auth state transitions
- backpressure behavior

### `providers/api`

Generic API-compatible provider.

Supports:

- OpenAI-compatible upstream
- Anthropic-compatible upstream
- Gemini-compatible upstream
- GLM/MiniMAX/DeepSeek specializations

### `providers/codex`

Codex CLI/container provider.

Reuses:

- `pkg/formats/codexauth`
- `codex-compat-proxy` bridge/transcoder knowledge

### `providers/claude`

Claude CLI/container provider.

Reuses:

- `pkg/formats/claudecreds`
- `claude-compat-proxy` bridge knowledge

### `providers/gemini`

Gemini CLI/container provider.

Reuses:

- `pkg/formats/geminioauth`
- `cli-sidecar` Gemini hooks
- `gemini-compat-proxy` bridge knowledge

### `providers/antigravity`

Antigravity sidecar provider.

### `providers/cline`

Cline sidecar/workspace agent provider.

### `providers/githubcopilot`

Future GitHub Copilot sidecar provider.

## Documentation Cleanup

Existing `docs/claude.md`, `docs/codex.md`, `docs/gemini.md` are current auth
format notes. They should remain until migration, but indexes must clearly mark
them as legacy/current auth-sync docs.

New provider operation details live under `docs/providers/`.

After provider docs are stable, old provider-format notes can be linked from
provider docs or moved under `docs/legacy/` in a separate cleanup commit.
