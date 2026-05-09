# Routing Policy v1 Draft

이 문서는 Router가 public API 요청을 provider instance로 배정하는 정책 규약
초안이다.

현재 구현 문서가 아니라 설계 합의용 draft다.

## Goals

- OpenAI, Anthropic, Gemini compatible 요청을 공통 routing model로 처리한다.
- 자체 사용자/API key/quota와 provider native quota를 분리한다.
- model alias, provider candidates, constraints, weights, fallback을 명시한다.
- 왜 특정 요청이 실패했거나 특정 provider로 갔는지 설명 가능해야 한다.
- policy dry-run과 staged publish/rollback을 지원한다.

## Concepts

- `tenant`: organization or project boundary.
- `user`: router user.
- `api_key`: user credential; raw secret is never stored.
- `model_alias`: public model name exposed by router.
- `route`: alias to candidate providers and constraints.
- `provider`: logical provider config.
- `provider_instance`: concrete connected shim/container.
- `host_name`: physical or VM node that provides the service. Dashboards and
  usage views show this instead of container name.
- `capability`: provider-supported operation.
- `quota`: router-side allowance or provider-native allowance.
- `ledger`: reservation/commit/release accounting record.

## Request Lifecycle

```text
authenticate API key
authorize user/model/capability
normalize public request to canonical request
reserve router quota
resolve model alias
find candidate provider instances
filter by constraints
score and order candidates
acquire data stream
forward request
stream response
commit or release quota
record audit/trace
```

If request fails before provider execution starts, quota reservation is released.
If provider execution starts but final usage is unknown, router records estimated
usage and marks the ledger entry reconciliable.

## Public API Dialects

Router may expose:

- OpenAI-compatible `/v1/chat/completions`
- OpenAI-compatible `/v1/responses`
- Anthropic-compatible `/v1/messages`
- Gemini-compatible `generateContent`
- Router-native admin and usage APIs

Public request is converted to canonical request before routing unless policy
explicitly selects raw HTTP passthrough.

## Capability Matching

Each route requires one or more capabilities.

Examples:

- OpenAI chat completion requires `api.openai.chat`
- Anthropic messages requires `api.anthropic.messages`
- Gemini generateContent requires `api.gemini.generateContent`
- Tool-use request may require `agent.tool_use`
- Workspace agent request may require `agent.workspace.read` or `agent.workspace.write`

Provider candidates missing required capabilities are filtered out.

## Policy YAML

Example:

```yaml
version: routing-policy/v1

model_aliases:
  gpt-5-codex:
    canonical_model: gpt-5.3-codex-spark
    required_capabilities:
      - api.openai.chat
      - stream.sse
  gemini-auto:
    canonical_models:
      - auto-gemini-3
      - auto-gemini-2.5
      - gemini-2.5-flash
    required_capabilities:
      - api.gemini.generateContent

routes:
  - id: codex-primary
    match:
      models: [gpt-5-codex]
      tenants: [team-a, team-b]
      api_dialects: [openai, anthropic]
    candidates:
      - provider: codex-cli
        account: primary@example.test
        host_name: snowbox
        weight: 100
      - provider: codex-cli
        account: secondary@example.test
        host_name: snowbox
        weight: 50
      - provider: openai-api
        weight: 10
    constraints:
      auth_status: [healthy, refresh_soon]
      health_state: [ready]
      min_provider_remaining_fraction: 0.05
      max_queue_depth: 4
      require_warm_stream: true
    fallback:
      on_provider_busy: next
      on_auth_expired: next
      on_quota_exhausted: next
      on_capability_unsupported: reject
      on_policy_denied: reject
```

API provider example:

```yaml
routes:
  - id: anthropic-compatible-cheap
    match:
      models: [claude-compatible-lowcost]
    candidates:
      - provider: glm-api
        weight: 70
      - provider: minimax-api
        weight: 30
      - provider: deepseek-api
        weight: 20
    constraints:
      required_capabilities:
        - api.anthropic.messages
      max_error_rate_5m: 0.05
      min_provider_remaining_fraction: 0.10
```

Sidecar provider example:

```yaml
routes:
  - id: cline-agent-workspace
    match:
      models: [cline-agent]
      api_dialects: [openai]
    candidates:
      - provider: cline-sidecar
        weight: 100
    constraints:
      required_capabilities:
        - agent.workspace.read
        - agent.workspace.write
        - agent.terminal
      user_groups: [operators]
```

## Constraints

Common constraints:

- `required_capabilities`
- `tenant`
- `user_group`
- `model`
- `api_dialect`
- `auth_status`
- `health_state`
- `provider_kind`
- `node_id`
- `host_name`
- `region`
- `account`
- `min_provider_remaining_fraction`
- `max_queue_depth`
- `max_error_rate_5m`
- `max_p95_latency_ms`
- `require_warm_stream`
- `allow_degraded`
- `cost_class`

Constraints are evaluated before scoring.

## Scoring

After filtering, candidates are scored.

Suggested score inputs:

- configured weight
- provider native quota remaining
- router queue depth
- warm stream availability
- recent p95 latency
- recent error rate
- auth freshness
- container memory pressure
- account affinity if configured

Router SHOULD store score explanation for request trace.

Example explanation:

```json
{
  "selected": "codex-cli/primary/a1",
  "score": 0.82,
  "reasons": [
    "matched model alias gpt-5-codex",
    "required capabilities satisfied",
    "auth healthy",
      "provider remaining 68%",
      "host snowbox has warm provider instance",
      "warm stream available",
      "lower p95 latency than fallback"
  ],
  "rejected_candidates": [
    {
      "provider": "codex-cli/secondary/a3",
      "reason": "queue_depth 8 > max_queue_depth 4"
    }
  ]
}
```

## Quota Model

Router-side quota and provider-native quota are separate.

Router quota dimensions:

- tenant
- user
- API key
- model alias
- route
- day/month/custom reset period
- token budget
- request budget
- optional spend budget

Provider-native quota dimensions:

- host/service/provider/account
- provider account
- provider model/window
- provider reset time
- concurrency
- rate limit

Quota lifecycle:

```text
reserve estimated budget
start provider request
receive streaming or final usage
commit actual usage
release unused reservation
reconcile provider-reported usage later if needed
```

Long streaming requests SHOULD use incremental reservation or periodic
reconciliation instead of a single final commit.

Retry policy MUST be idempotent. A retried request must not double-charge unless
multiple provider executions actually consumed provider quota.

## Fallback Policy

Fallback decisions must be explicit.

Common actions:

- `next`: try next candidate.
- `reject`: return error.
- `queue`: wait for candidate capacity.
- `downgrade`: route to cheaper/weaker model alias.
- `degraded`: use provider in degraded state.

Recommended defaults:

- auth expired: `next`
- auth revoked: `next`
- no provider with capability: `reject`
- provider busy: `next`
- router quota exhausted: `reject`
- provider quota exhausted: `next`
- stream open timeout: `next`
- provider started then failed: route-specific; avoid unsafe retry for non-idempotent agent tasks

## Route Dry Run

Router MUST support dry-run route evaluation.

Input:

```json
{
  "tenant_id": "team-a",
  "user_id": "usr_123",
  "api_key_id": "key_abc",
  "model": "gpt-5-codex",
  "api_dialect": "openai",
  "stream": true,
  "features": ["tool_use"]
}
```

Output:

```json
{
  "allowed": true,
  "selected": "codex-cli/primary/a1",
  "route_id": "codex-primary",
  "required_capabilities": ["api.openai.chat", "stream.sse"],
  "quota": {
    "reservation": 4000,
    "remaining_after_reservation": 96000
  },
  "fallback_chain": [
    "codex-cli/primary/a1",
    "codex-cli/secondary/a3",
    "openai-api/default"
  ],
  "rejections": []
}
```

## Audit Events

Router records audit events for:

- API key authentication success/failure
- policy decision
- quota reservation
- provider selection
- stream open/close
- provider error
- quota commit/release
- route policy publish/rollback
- admin drain/restart/refresh/update action

Audit logs MUST NOT store raw provider tokens, raw API keys, or prompt/response
bodies by default.

## UI Requirements

Routing UI should expose:

- model alias to provider fallback chain
- current candidate health
- auth and quota state
- dry-run route test
- draft/publish policy workflow
- diff and rollback
- affected users/models before publish

Request trace should show:

```text
user -> api key -> model alias -> route policy -> selected provider
-> selected host/node/container -> stream -> provider response -> usage ledger
```

## Policy Contract Tests

Required tests:

- model alias resolution
- capability filtering
- auth/health/quota constraints
- weighted selection
- fallback chain behavior
- user model allow/deny
- quota reserve/commit/release
- retry idempotency
- dry-run explanation
- route policy version rollback
- concurrent requests do not overrun quota

## Open Questions

- Should policy be stored as YAML in git, DB records edited by UI, or both with staged publish?
- Should provider score be deterministic by default, or should weighted random be allowed?
- How should model downgrade be represented to clients?
- Which quota unit is primary for non-token providers: tokens, requests, cost units, or provider native windows?
