# Monorepo Implementation v1 Draft

이 문서는 Pangaea monorepo 전환의 구현 상세 초안이다.

현재 구현 문서가 아니라 미래 구조 설계 문서다.

## Direction

Pangaea becomes:

```text
router + node-agent + provider-shim + compat core + legacy auth sync
```

Existing provider-specific proxy servers are not merged as-is. Bridge,
transcoder, and fixture knowledge are extracted into shared packages and
provider implementations.

## Commands

Preferred future CLI:

```text
pangaea router serve --config router.yaml
pangaea node-agent run --config node-agent.yaml
pangaea provider-shim run --config /etc/pangaea/provider.yaml
pangaea providerctl inspect|models|usage|refresh|drain|exec
pangaea legacy serve|connect|status
```

Compatibility:

- `pangaeactl` remains supported during migration.
- Current commands can live under `legacy`.

## Config Schemas

Router:

```yaml
listen:
  public_api: "0.0.0.0:8080"
  control: "0.0.0.0:8443"
  admin: "127.0.0.1:9090"
store:
  driver: sqlite
  dsn: /var/lib/pangaea/router.db
identity:
  auth_mode: mtls
routing_policy_file: /etc/pangaea/routing-policy.yaml
quota_policy_file: /etc/pangaea/quota-policy.yaml
data_plane:
  mode: reverse_stream_pool
  idle_streams_per_provider: 2
  stream_token_ttl: 30s
```

Node agent:

```yaml
node_id: snowbox
host_name: snowbox
runtime:
  kind: docker
  rootless: true
providers:
  - id: codex-primary
    kind: app-server
    service: codex
    account_hint: primary@example.test
    image: pangaea/provider-codex:2026.05.1
    auth:
      bootstrap: copy
      host_path: /srv/pangaea/auth/codex/primary/auth.json
      container_path: /var/lib/pangaea/auth/codex/auth.json
    refresh:
      threshold: 5m
      command: ["codex", "exec", "--ephemeral", "Reply with OK only."]
```

Identity key is not `service`. It is:

```text
provider_id + provider_instance_id + account_id
```

## Provider Interfaces

```go
type ProviderEndpoint interface {
    Identity() ProviderIdentity
    Capabilities(context.Context) (Capabilities, error)
    Health(context.Context) (Health, error)
    Models(context.Context) ([]Model, error)
    Usage(context.Context) (UsageReport, error)
    Invoke(context.Context, *compat.Request) (*compat.Response, error)
    Stream(context.Context, *compat.Request) (<-chan compat.Event, error)
}
```

Optional:

```go
type AuthManagedProvider interface {
    AuthSnapshot(context.Context) (AuthSnapshot, error)
    RefreshNudge(context.Context, RefreshRequest) (RefreshResult, error)
}

type ContainerManagedProvider interface {
    Bootstrap(context.Context) error
    Upgrade(context.Context, ImageRef) error
    Drain(context.Context, DrainOptions) error
}

type HTTPUpstreamProvider interface { Upstream() UpstreamSpec }
type LocalServerProvider interface { LocalEndpoint() LocalEndpointSpec }
type AgentExecutorProvider interface { WorkspaceCapabilities() WorkspacePolicy }
```

## Runtime Interface

```go
type Runtime interface {
    Info(context.Context) (RuntimeInfo, error)
    Pull(context.Context, ImageRef) error
    Create(context.Context, ContainerSpec) (ContainerID, error)
    Start(context.Context, ContainerID) error
    Stop(context.Context, ContainerID, time.Duration) error
    Exec(context.Context, ContainerID, ExecSpec) (ExecResult, error)
    CopyTo(context.Context, ContainerID, CopySpec) error
    CopyFrom(context.Context, ContainerID, CopySpec) error
    Stats(context.Context, ContainerID) (ContainerStats, error)
    Logs(context.Context, ContainerID, LogSpec) (<-chan LogEvent, error)
    Remove(context.Context, ContainerID, RemoveOptions) error
}
```

Docker first, Podman second.

## Compat Core

`internal/compat` is based on `cli-sidecar` canonical transformer.

Required extensions:

- request id and tenant/user metadata
- API dialect
- multimodal metadata
- tool call delta
- usage source
- canonical stream events
- unsupported feature policy

Package split:

```text
internal/compat/
  canonical.go
  openai/
  anthropic/
  gemini/
  stream/
```

Provider-native conversion stays in provider package.

## Code Reuse Plan

Reuse directly or with adaptation:

- `pkg/formats/*`
- `internal/safeio`
- `internal/pki`
- `internal/jwtauth`
- `internal/logging`
- current WebSocket codec concepts
- current reverse connectivity concepts
- `cli-sidecar` canonical transformers and CLI adapters
- `antigravity-cli/*-compat-proxy` bridge knowledge
- `cline-sidecar` relay capability model

Do not directly carry forward:

- provider-specific public API servers as separate router surfaces
- direct host CLI refresh as primary path
- container names as operator provider identity

## Implementation Sequence

1. Finish docs and provider catalog.
2. Move canonical model into `internal/compat`.
3. Add `internal/provider` contract.
4. Implement provider simulator.
5. Implement `internal/control`.
6. Implement `internal/tunnel`.
7. Implement router MVP.
8. Implement node-agent read-only inventory.
9. Add Docker lifecycle and auth bootstrap.
10. Add API-compatible provider.
11. Add Codex CLI provider.
12. Add authsync v2.
13. Add dashboard MVP.
14. Add Claude/Gemini/Antigravity/Cline/Copilot providers.

## Documentation Rule

Provider operation detail goes in `docs/providers/[provider].md`.

Each provider document uses the same sections:

- purpose
- kind
- capabilities
- auth
- bootstrap
- refresh
- runtime/local server
- models
- usage
- routing notes
- limitations
- tests
