# Pangaea Router Dashboard v1 Design

이 문서는 Pangaea Router Web Dashboard의 구현 기준 설계다.

기존 `internal/router/dashboard.go`의 single-file HTML 대시보드는 bootstrap UI로
유지할 수 있지만, v1 Dashboard는 React로 빌드한 정적 자산을 Go 바이너리에 embed해서
배포한다.

## Design Team Consensus

DesignTeam의 공통 결론은 Dashboard를 "관리 페이지"가 아니라 LLM operations console로
다뤄야 한다는 것이다.

- 운영자는 provider, route, auth, quota, usage, request failure를 한 화면 흐름에서
  좁혀 들어갈 수 있어야 한다.
- `host_name`은 모든 provider/usage/container 화면의 1급 컬럼이다. 컨테이너 내부
  hostname이나 container name이 서비스 제공 주체처럼 표시되면 안 된다.
- 하나의 node/host에서 동일 서비스 provider가 여러 개 존재할 수 있으므로
  `service + provider_kind + host_name + account + provider_instance_id`를 함께
  보여준다.
- 안전한 조치 흐름이 중요하다. Drain, refresh, restart, quota update, key mutation은
  preview, confirmation, execute, audit, observe 단계를 가진다.
- 대시보드 보안은 public inference API key와 분리된 privileged admin plane으로
  설계한다.

## Goals

- Router, provider, node, container, auth, quota, usage 상태를 운영자가 빠르게
  이해할 수 있는 고밀도 콘솔 제공
- OpenAI, Anthropic, Gemini 호환 요청이 어떤 provider로 라우팅되는지 진단
- CLI/container provider의 auth refresh, drain, resume, upgrade 상태를 안전하게 관리
- 사용자/API key/tenant quota를 관리하고 요청 trace와 audit으로 원인을 추적
- 외부 CDN 없이 React build artifact와 font asset을 Go 바이너리에 embed

## Non-Goals

- 일반 사용자 self-service portal까지 같은 화면에서 해결하지 않는다.
- route policy authoring 전체를 v1 첫 화면의 중심 기능으로 만들지 않는다. v1은
  read/diagnostic 중심이고, write workflow는 preview 가능한 범위부터 제공한다.
- provider auth 원본 secret을 대시보드에서 노출하지 않는다.

## Frontend Runtime

### Build Shape

```text
web/router-ui/
  package.json
  package-lock.json
  index.html
  src/
    app/
    components/
    features/
    lib/
    styles/
    assets/fonts/
  public/
  dist/

internal/routerui/
  assets.go
  dist/                 # generated copy of web/router-ui/dist
```

`internal/routerui/assets.go`:

```go
package routerui

import "embed"

//go:embed dist/*
var FS embed.FS
```

Go embed는 package directory 밖의 path를 직접 embed할 수 없으므로, frontend build
후 `web/router-ui/dist`를 `internal/routerui/dist`로 복사한다. Router는
`/router/ui`에서 SPA fallback을 제공하고 `/router/ui/assets/*`에서 fingerprinted
asset을 서빙한다.

### Build Commands

```text
make router-ui        # npm ci + npm run build + dist copy
make build            # router-ui 이후 Go build
go generate ./...     # 선택 사항. CI에서는 make target을 명시적으로 사용
```

`npm ci`를 기본으로 사용해서 React dependency를 lockfile에 고정한다. 운영 빌드는
인터넷 없이 재현 가능해야 하므로 CI cache와 package lock을 필수로 둔다.

### Suggested React Stack

- React + TypeScript
- Vite build
- TanStack Query for server state and retry/stale policy
- TanStack Table for dense sortable/filterable tables
- React Router for view routing under `/router/ui`
- Radix UI primitives for dialogs, popovers, tabs, menus, tooltips
- lucide-react for toolbar/action icons

상태 저장은 최소화한다. 서버 상태는 query cache에 두고, UI 상태는 URL query와
component state를 우선한다. Admin token은 `localStorage`에 저장하지 않는다.

## Embedded Fonts

Dashboard는 외부 font CDN을 사용하지 않는다. `woff2` 파일을 직접 다운로드해
저장소에 포함하고 Go embed 대상 asset으로 빌드한다.

### Font Selection

- Primary UI: `Pretendard Variable`
  - 한국어/영문 혼합 운영 콘솔에서 균형이 좋고, 숫자와 짧은 label 가독성이 좋다.
  - variable `woff2` 하나로 400, 500, 600, 700 weight를 사용한다.
- Monospace: `JetBrains Mono`
  - provider id, request id, API key prefix, trace code, JSON preview에 사용한다.
  - 필요한 weight만 포함한다: 400, 600.

Inter는 영문 UI만 고려하면 좋은 선택이지만, Pangaea 운영자는 한국어 label과 영문
identifier를 같이 보게 된다. v1 기본은 Pretendard 하나로 UI tone을 맞추고,
monospace만 JetBrains Mono로 분리한다.

### Asset Layout

```text
web/router-ui/src/assets/fonts/
  PretendardVariable.woff2
  JetBrainsMono-Regular.woff2
  JetBrainsMono-SemiBold.woff2
  NOTICE.md
  checksums.txt
```

`scripts/download-router-ui-fonts.sh`가 다음 작업을 수행한다.

- pinned source URL에서 `woff2`만 다운로드
- `sha256sum`으로 `checksums.txt` 생성
- `NOTICE.md`에 source URL, license, 다운로드 날짜, checksum 기록
- 기존 font와 checksum diff를 출력해서 review 가능하게 함

Primary source:

- Pretendard: [`orioncactus/pretendard`](https://github.com/orioncactus/pretendard) release의
  `dist/web/variable/woff2/PretendardVariable.woff2`
- JetBrains Mono: [`JetBrains/JetBrainsMono`](https://github.com/JetBrains/JetBrainsMono)의
  `fonts/webfonts/*.woff2`

Pretendard와 JetBrains Mono는 OFL-1.1 계열 license를 사용한다. font 파일은 직접
commit해서 router binary 안에 embed한다.

### CSS Contract

```css
@font-face {
  font-family: "Pretendard";
  src: url("../assets/fonts/PretendardVariable.woff2") format("woff2");
  font-weight: 400 700;
  font-style: normal;
  font-display: swap;
}

:root {
  font-family: "Pretendard", -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif;
  font-size: 14px;
  font-variant-numeric: tabular-nums;
}

code,
.mono {
  font-family: "JetBrains Mono", "SFMono-Regular", Consolas, monospace;
}
```

운영 콘솔은 숫자 비교가 많으므로 `tabular-nums`를 기본으로 사용한다. 글꼴 크기는
viewport width로 scaling하지 않는다.

## Visual System

Tone은 조용하고 정밀한 운영 콘솔이다.

- Layout: left rail navigation + sticky top command bar + full-width work area
- Density: 기본 table row 36-40px, compact mode 32px
- Radius: card/dialog/table frame 6-8px
- Color: neutral base, blue는 interactive, green/amber/red는 health와 incident에만 사용
- No hero, no marketing panels, no decorative gradients
- Status는 색만 쓰지 않고 icon + text + timestamp를 함께 제공
- 숫자, quota, latency, usage는 오른쪽 정렬과 fixed-width column을 사용
- 긴 ID는 middle ellipsis와 copy action을 제공

### Screen Chrome

```text
+--------------------------------------------------------------------------------+
| env / router / role / search / command palette / data age / refresh             |
+--------------+-----------------------------------------------------------------+
| Overview     | Health strip                                                    |
| Routes       | Incident queue                    Capacity matrix               |
| Providers    | Auth risk / quota pressure       Live requests                  |
| Requests     | Recent route failures                                          |
| Incidents    |                                                                 |
| Usage        | Main table or detail workspace                                  |
| Quotas       |                                                                 |
| ...          |                                                                 |
+--------------+-----------------------------------------------------------------+
```

- Left rail width: 216px desktop, icon-only 56px compact, bottom sheet navigation on mobile.
- Top bar height: 48px, sticky, never overlaps table headers.
- Work area max width is not capped; tables use available horizontal space.
- Detail appears as right drawer, 480-720px wide depending on viewport.
- Mutating action confirmation uses modal dialog, not drawer, to avoid accidental execution.

### Design Tokens

```css
:root {
  --font-ui: "Pretendard", -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif;
  --font-mono: "JetBrains Mono", "SFMono-Regular", Consolas, monospace;

  --bg: #f7f8fa;
  --surface: #ffffff;
  --surface-subtle: #f1f3f5;
  --border: #d8dde3;
  --text: #17202a;
  --muted: #667085;
  --interactive: #2563eb;
  --ok: #15803d;
  --warn: #b45309;
  --danger: #b42318;
  --unknown: #6b7280;

  --row-default: 38px;
  --row-compact: 32px;
  --radius: 6px;
}
```

Palette는 neutral 기반이다. provider 종류를 구분하기 위해 색을 남발하지 않고,
provider icon/label/service column으로 구분한다. 색은 주로 상태와 위험도를 표현한다.

### Operator Convenience

- Global search는 provider id, host, account, model, API key prefix, request id를 찾는다.
- Command palette는 `drain provider`, `dry run route`, `find request`, `open auth risk`
  같은 작업 중심 command를 제공한다.
- Saved views는 table column, filters, sort, density를 저장한다.
- Bulk selection은 provider drain, quota template apply, key disable처럼 scope가 명확한
  작업에만 제공한다.
- Every table has column visibility, sticky identity columns, CSV export for current filtered view.
- Copy actions copy IDs only. Secret values are never copied from normal table cells.
- Empty states show the next operator action, not explanatory marketing copy.

## Information Architecture

Primary navigation:

- Overview
- Routes
- Providers
- Requests
- Incidents
- Usage
- Quotas
- Users
- API Keys
- Nodes
- Containers
- Auth
- Audit
- Settings

v1의 실제 첫 구현은 다음 5개 view를 우선한다.

1. Overview
2. Routes + Dry Run
3. Providers
4. Requests + Trace Detail
5. Controls/Audit

Nodes, Containers, Auth, Users, API Keys, Quotas, Usage는 기존 API를 활용해 secondary
view로 붙이되, UX 완성도는 provider operations 흐름을 우선한다.

## Core Read Models

UI는 내부 Go struct를 그대로 소비하지 않는다. Dashboard용 read model을 둔다.

### DashboardSummary

- router health
- active requests
- active streams
- provider ready/degraded/down count
- disconnected control/data sessions
- stale nodes/containers
- auth expiring/expired count
- quota pressure count
- recent route failures
- fallback rate
- top incidents
- last updated at

Endpoint:

```text
GET /router/v1/dashboard/summary
```

### RouteView

- route id
- public model aliases
- accepted dialects
- stream support
- candidate providers
- constraints
- weight/fallback order
- current candidate health
- last decision
- last rejection reasons

Endpoint:

```text
GET /router/v1/dashboard/routes
```

### ProviderView

- provider_instance_id
- service
- provider_kind
- host_name
- node_id
- account display
- models/capabilities
- health/auth/usage state
- control/data session state
- container state
- concurrency and active streams
- p95 latency
- recent 429/5xx
- CLI/shim/image versions
- last refresh/upgrade result

Endpoint:

```text
GET /router/v1/dashboard/providers
GET /router/v1/dashboard/providers/{provider_instance_id}
```

### RequestTraceView

- request id
- tenant/user/API key prefix
- protocol and model
- selected provider
- rejected candidates
- quota reservation/commit/release
- route decision
- status/error normalization
- first byte and total duration
- token estimate and actual usage

Endpoint:

```text
GET /router/v1/dashboard/traces?limit=100&cursor=...
GET /router/v1/dashboard/traces/{request_id}
```

### IncidentView

Incidents are derived from provider, node, quota, auth, session, and trace signals.

- severity
- service/model/provider/account/host scope
- first seen / last seen
- affected routes/models/users/API keys
- recommendation
- linked runbook
- related traces and audit events

## View Design

### Overview

Top command bar:

- environment badge
- router version
- current admin role
- global search
- refresh state and data age
- command palette

Main panels:

- Fleet health strip
- Capacity by service/model
- Incident queue
- Auth and quota risk
- Live request pressure
- Recent route failures

Overview must degrade gracefully. If one endpoint fails, the page shows stale cached sections with
section-level error banners instead of failing the whole dashboard.

### Routes

Primary table:

- model alias
- protocols
- stream support
- candidate count
- ready/degraded/down candidates
- fallback chain
- quota pressure
- last decision
- last failure

Route detail drawer:

- candidate matrix grouped by `service -> host_name -> account`
- selected/rejected reasons from recent traces
- quota and concurrency constraints
- policy preview

Dry Run panel:

- tenant/user/API key prefix
- protocol
- model
- stream flag
- token estimate
- optional provider/service/host/account constraint

Output:

- selected provider
- fallback candidates
- rejected candidates with reasons
- quota reservation estimate
- current session/auth state
- routing impact

### Providers

Table is grouped by service and host. `host_name`, account, provider id, and health columns are
pinned.

Row actions:

- Drain
- Resume routing
- Refresh auth
- Probe
- Restart runtime
- Upgrade image

Drain is the safest default action and appears first. Restart/upgrade/auth writeback are separated
as elevated actions.

Provider detail drawer tabs:

- Summary
- Models
- Auth
- Usage
- Sessions
- Container
- Traces
- Audit
- Actions

### Requests

Request list:

- time
- request id
- user/API key
- protocol
- model
- provider
- host/account
- status
- route duration
- provider duration
- token usage
- error code

Trace detail drawer:

- route decision timeline
- quota reservation and commit/release
- provider data websocket session
- normalized upstream error
- audit correlation

Prompt and response bodies are not stored or displayed by default.

### Incidents

Incident grouping:

- no ready provider for route/model
- auth expired/revoked/unrefreshable
- stale node or container
- disconnected control/data session for routable provider
- high 429/5xx rate
- quota pressure
- failed refresh/drain/upgrade

Each incident provides operator next actions and links to affected routes, providers, traces, and
audit events.

### Admin Management

Users/API Keys/Quotas are designed for administrative convenience:

- searchable tables with saved filters
- bulk selection for quota changes and disable/drain operations
- one-time raw API key display only
- masked key prefixes
- quota templates by role/group/model
- reason field and idempotency key for mutating actions
- audit preview before execution

## Control Workflow

Every dangerous action follows the same contract.

```text
POST /router/v1/dashboard/actions/{action}/preview
POST /router/v1/dashboard/actions/{action}/execute
```

Preview returns:

- target scope
- current state
- affected routes/models/users/API keys
- active streams that block or delay action
- fallback availability
- required role
- required confirmation text
- audit metadata

Execute requires:

- preview id
- confirmation
- reason
- idempotency key
- recent admin auth

The UI always shows the resulting audit event and updated provider/route state.

## Security Requirements

- Dashboard/admin auth is separate from public inference API keys.
- Admin roles: `viewer`, `operator`, `quota_admin`, `key_admin`, `auth_admin`,
  `break_glass`.
- Non-dev router startup must fail without explicit admin auth, peer auth, stream-token key, and
  public API auth configuration.
- No admin token persistence in `localStorage`.
- Prefer secure HttpOnly same-site admin session cookies with CSRF tokens. If bearer mode is kept
  for early development, keep the token only in memory.
- Raw API keys are displayed once and never stored in UI state after dismissal.
- `/router/ui` responses include CSP, `X-Content-Type-Options`, `Referrer-Policy`, and no framing.
- Peer websocket auth moves away from shared query token toward per-peer identity with expiry,
  audience, node id, provider id, and capability claims.
- Audit is durable append-only storage, not only in-memory ring buffer.
- Audit and trace payloads are redacted and length-capped before storage and response.

## Performance And Memory

- Use `/router/v1/dashboard/summary` for initial paint. Do not fetch 10 independent heavy endpoints
  every 10 seconds as the primary model.
- Tables are paginated server-side with cursor or time-window filters.
- Traces and audit events have bounded retention plus persistent storage.
- Incremental updates use SSE or websocket after summary/read models are stable.
- Polling has jitter, backs off on repeated failures, and pauses when the tab is hidden.
- Large tables cap DOM rows. Use server paging first; add row virtualization only where data volume
  requires it.
- Router aggregation should use copy-on-read snapshots and avoid holding hot registry locks while
  JSON encoding.
- Registry indexes should support service, model, host, account, health, auth state, and provider id
  lookup.
- Performance tests track payload size, JSON encode time, route evaluation allocations, trace/audit
  retention, and dashboard render time.

## Test Strategy

Backend:

- API contract/golden tests for Dashboard read models
- auth/RBAC tests for every admin route
- quota reserve/commit/release tests
- provider control preview/execute tests
- trace and audit redaction tests

Frontend:

- component tests for tables, drawers, status badges, action confirmations
- API mock tests for partial failure, stale data, unauthorized, empty states
- Playwright e2e for `/router/ui` desktop and mobile viewports
- screenshot checks for dense table layout, font loading, drawer overflow, and non-overlapping text
- network test ensuring no external font/CDN request is made

CI:

- `npm ci`
- `npm run lint`
- `npm run typecheck`
- `npm run test`
- `npm run build`
- `go test ./...`
- `git diff --check`

## Delivery Plan

### Phase 1: Embedded React Shell

- Add `web/router-ui` React/Vite/TypeScript app.
- Add embedded fonts and font NOTICE/checksum files.
- Add `internal/routerui` embed package.
- Replace `/router/ui` serving with embedded SPA fallback.
- Keep current admin API surface.

### Phase 2: Dashboard Read Models

- Add `/router/v1/dashboard/summary`.
- Add provider/route/trace read model endpoints.
- Add section-level partial failure handling in UI.
- Keep old endpoints for compatibility while migrating UI.

### Phase 3: Operator Workflows

- Add preview/execute action API.
- Implement provider drain/resume/probe/refresh workflows.
- Add audit correlation and result observation in UI.

### Phase 4: Admin Management

- Split admin/public key stores or scopes.
- Add RBAC enforcement.
- Add user/API key/quota management views.
- Add durable audit store.

### Phase 5: Live Operations

- Add dashboard SSE or websocket event stream.
- Add incident derivation and runbook links.
- Add rollout/upgrade progress visualization.

## Open Decisions

- Admin session mode: HttpOnly cookie session vs memory-only bearer token for first React cut.
- Whether route policy editing is v1 or v2.
- Durable audit storage backend.
- SSE vs websocket for dashboard live updates.
