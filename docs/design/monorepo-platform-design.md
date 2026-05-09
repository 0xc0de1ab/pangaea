# Pangaea Monorepo Platform Design Draft

이 문서는 Pangaea를 단일 auth sync 도구에서 monorepo 기반 LLM runtime
platform으로 확장하기 위한 상세 설계 초안이다.

현재 구현 문서가 아니라 미래 구조 설계 문서다.

## Product Direction

Pangaea의 장기 목표는 다음을 하나의 platform으로 통합하는 것이다.

- 격리된 provider runtime container 운영
- provider CLI/API/sidecar 자동 상태 관찰
- provider auth bootstrap, sync, refresh
- OpenAI, Anthropic, Gemini compatible public API 제공
- 자체 사용자, API key, quota, route policy 관리
- provider usage와 router usage를 통합한 dashboard
- 다양한 provider를 capability 기반으로 라우팅

Pangaea는 더 이상 “여러 노드의 auth 파일을 맞추는 도구”만이 아니다. 기존
기능은 `legacy-auth-sync` compatibility layer로 남기고, 새 중심은
containerized provider router다.

## Monorepo Scope

이 monorepo는 다음 기존 프로젝트 기능을 흡수한다.

- `.`
  - mTLS/JWT WebSocket transport
  - auth format parser/validator
  - usage probe
  - reverse connectivity
  - notifier와 운영 event 경험
- `../cli-sidecar`
  - canonical OpenAI/Anthropic/Gemini transform
  - generic/tmux/pty/mitm CLI adapters
  - OpenAI/Gemini-compatible local server 경험
- `../antigravity-cli/*-compat-proxy`
  - Antigravity, Codex, Claude, Gemini bridge 구현 자산
  - provider local server/app-server protocol 조사 내용
- `../antigravity-cli/antigravity-router`
  - router, user, quota, dashboard 초기 자산
- `../cline-sidecar`
  - IDE extension relay
  - workspace executor capability 모델
- `../github-copilot-sidecar`
  - future sidecar provider placeholder

Provider별 proxy 서버 구현을 그대로 합치는 것은 목표가 아니다. Bridge와
transformer 자산은 흡수하되, public API server와 routing은 Pangaea router
공통 계층으로 재작성한다.

## Top-Level Roles

### Router

Router는 central control plane이자 user-facing API server다.

Responsibilities:

- public OpenAI/Anthropic/Gemini compatible API
- router-native admin API
- user, API key, quota, usage ledger
- model alias와 route policy
- provider registry와 route decision
- WebSocket control plane endpoint
- reverse data stream broker
- dashboard UI
- audit/event log

Router는 provider CLI나 Docker runtime을 직접 조작하지 않는다. Runtime 조작은
node-agent 권한이다.

### Node Agent

Node agent는 각 물리/VM host에서 실행된다.

Responsibilities:

- Docker/Podman runtime 관리
- provider container create/start/stop/restart/update
- provider auth bootstrap copy
- host/container auth reconciliation
- container resource metrics
- provider process health observation
- router로 node/container/provider 상태 보고
- router command에 따른 drain, refresh, update 수행

Node agent만 container runtime 권한을 가진다. Provider container 안에 Docker
socket을 넣지 않는다.

### Provider Shim

Provider shim은 provider runtime과 router protocol 사이의 adapter다.

Responsibilities:

- provider capability registration
- provider health/model/usage/auth report
- provider-local refresh nudge
- provider-local bridge 실행
- public request 또는 canonical request를 provider native call로 변환
- stream cancellation과 backpressure 전파
- provider-specific error normalization

Shim은 provider brand-specific code를 담되, router와 통신하는 표준 protocol은
공통이어야 한다.

### Provider Runtime Container

Provider runtime container는 실제 upstream service를 사용할 수 있는 격리 환경이다.

Examples:

- Codex CLI + Codex app-server bridge
- Claude CLI
- Gemini CLI/ACP
- GLM/MiniMAX/DeepSeek API egress shim
- Antigravity sidecar
- Cline code-server extension relay
- GitHub Copilot sidecar

Container 이름은 운영자-facing service identity가 아니다. Dashboard와 usage
view는 `host_name`, `service`, `provider_id`, `account`를 중심으로 보여준다.

## Provider Identity Model

Provider identity는 다음을 분리한다.

- `host_name`: 운영자가 보는 물리/VM host 이름
- `node_id`: router protocol identity
- `container_id`: runtime container identity
- `service`: provider service family, 예: `codex`, `claude`, `gemini`, `glm`
- `provider_id`: 설정상 논리 provider, 예: `codex-primary`
- `provider_instance_id`: 실제 연결된 shim instance
- `account_id`: provider account identity
- `account_display`: email 또는 operator-friendly label

하나의 host는 같은 service의 provider를 여러 개 가질 수 있다.

예:

```text
host_name=snowbox
  provider_id=codex-primary  service=codex  account=primary@example.test
  provider_id=codex-secondary service=codex  account=secondary@example.test
  provider_id=gemini-primary service=gemini account=primary@example.test
```

Auth bootstrap source path는 provider별로 지정 가능해야 한다. 기본 경로만
전제하면 동일 host의 다중 계정을 지원할 수 없다.

## Capability-First Provider Model

Router는 provider name이나 service brand가 아니라 capability로 후보를 고른다.

Examples:

- `api.openai.chat`
- `api.anthropic.messages`
- `api.gemini.generateContent`
- `usage.read`
- `auth.file`
- `auth.refresh.oneshot`
- `agent.workspace.read`
- `agent.workspace.write`
- `agent.terminal`
- `code.completion`

Provider kind는 구현 방식이다.

- `cli-container`
- `api-compatible`
- `sidecar-agent`
- `simulator`

Router는 kind를 알고 있을 수 있지만, generic routing은 capability와 health,
quota, policy에 따라 결정한다.

## Control And Data Plane

Control plane:

- long-lived WebSocket
- node/shim identity
- registration, heartbeat, inventory, auth, usage, command
- prompt/response/auth token 원문 금지

Data plane:

- public request와 provider response byte stream
- reverse stream or HTTP/2 CONNECT
- warm stream pool 권장
- scoped capability token required
- cancellation and half-close required
- bounded buffers only

WebSocket control frame에 요청 body를 싣는 것은 테스트/MVP fallback으로만
허용한다.

## Compatibility Layer

Public API dialect는 Router가 받는다.

- OpenAI `/v1/chat/completions`
- OpenAI `/v1/responses`
- Anthropic `/v1/messages`
- Gemini `generateContent`

Router/shared compat layer는 public request를 canonical request로 변환한다.
Provider shim은 canonical request를 provider native protocol로 변환한다.

Short-term escape hatch:

- provider shim이 raw HTTP passthrough를 처리할 수 있다.
- 단, 이 경우에도 capability, usage, error, audit metadata는 표준화해야 한다.

## State And Readiness

Provider instance is routable only when:

- node agent connected
- container running
- shim connected
- auth healthy or provider does not require file auth
- provider local server/upstream healthy
- models discovered
- data stream capacity available
- route policy permits it

Container running alone is not readiness.

## Auth Model

Auth state holders:

- host auth source
- container auth copy
- provider runtime memory/session
- router selected auth source

Bootstrap is copy by default:

- `auth.host_path` -> `auth.container_path`
- host path may be default or explicit
- explicit path is recommended for multi-account hosts
- bind mount is disabled by default

Refresh happens inside container:

- provider shim/node-agent marks instance refreshing/draining
- executes official provider refresh path or oneshot nudge
- watches container auth mutation
- validates auth
- reports result
- router decides whether to write back to host/server state

## Usage Model

Usage reporting is multi-dimensional.

Required dimensions:

- tenant/user/API key
- route/model alias
- provider/service/account
- host_name/node_id/container_id
- provider native window
- router-side quota period

Provider native usage and router usage ledger are separate. Router may estimate
usage when provider does not report tokens, but provider-reported usage must be
stored when available.

## UI Model

Router dashboard is an LLM operations console.

Primary sections:

- Overview
- Routes
- Providers
- Nodes
- Containers
- Auth
- Users
- API Keys
- Quotas
- Usage
- Events/Audit

The UI must answer:

- 어떤 요청이 어느 provider/host/account로 갔는가?
- 왜 특정 후보가 제외되었는가?
- 어떤 auth가 곧 만료되는가?
- 어떤 provider quota가 압박받는가?
- 어떤 host/container가 요청을 받을 수 없는가?
- 수동 refresh/restart/drain을 실행하면 어떤 영향이 있는가?

## Migration From Current Pangaea

Migration path:

1. Keep current auth sync as legacy mode.
2. Add future design docs and protocol contract tests.
3. Introduce provider simulator.
4. Extract transport/control primitives from current Pangaea.
5. Add router registry for providers without routing traffic.
6. Add API-compatible provider MVP.
7. Add Codex cli-container provider MVP.
8. Add reverse data stream routing.
9. Add quota/user/API key and dashboard.
10. Deprecate host-level CLI refresh in favor of container-local refresh.

## Design Gates

No production routing until:

- provider protocol v1 contract tests exist
- provider simulator exists
- per-shim identity exists
- scoped data stream token exists
- API key hashing exists
- quota ledger exists
- auth redaction tests exist
- hardened container profile exists
- audit schema exists

## Related Documents

- [Provider Protocol v1](./provider-protocol-v1.md)
- [Provider Container Spec v1](./container-spec-v1.md)
- [Routing Policy v1](./routing-policy-v1.md)
- [Module Layout v1](./module-layout-v1.md)
- [Implementation Roadmap](./implementation-roadmap.md)
- [Provider Docs](../providers/README.md)
