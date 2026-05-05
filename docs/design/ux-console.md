# Router Dashboard UX/UI Draft

이 문서는 Pangaea Router Dashboard의 UX/UI 설계 초안이다.

현재 구현 문서가 아니라 미래 구조 설계 문서다.

## Goal

Router Dashboard는 LLM operations console이다.

It must answer:

- 지금 요청을 받을 수 있는 provider capacity가 있는가?
- model alias가 어디로 라우팅되는가?
- auth, quota, node, container, provider 중 어디가 병목인가?
- 특정 요청 실패가 어느 단계에서 발생했는가?
- 수동 조치가 어떤 사용자와 트래픽에 영향을 주는가?

## Information Architecture

Primary navigation:

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
- Requests
- Events/Audit

Operator UI and user self-service UI are separate.

## Overview

Top summary:

- Healthy capacity
- Active requests
- Auth risk
- Quota pressure
- Route failures
- Provider errors
- Disconnected nodes

Provider pool table:

- Service
- Provider kind
- Host name
- Accounts
- Containers
- Models
- Route weight
- Usage
- Last error

Use red only for active traffic failure, revoked auth, or no route available.
Use amber for expiring auth, quota pressure, and degraded provider states.

## Routes

Route unit:

```text
model alias -> candidates -> constraints -> weights -> fallback
```

Route row:

- model alias
- public protocols
- candidate providers
- constraints
- fallback chain
- current health
- last decision
- draft/published status

Editing should be form-first. YAML is advanced mode with diff preview.

## Route Dry Run

Inputs:

- user or group
- API key prefix
- requested protocol
- model alias
- payload class
- estimated tokens
- optional region/latency/cost constraints

Outputs:

- selected provider
- fallback candidates
- rejection reasons
- quota reservation estimate
- provider native limit impact
- auth freshness
- queue depth
- expected latency class
- traffic-impact summary

## Providers

Provider row:

- Provider ID
- Service
- Kind
- Host name
- Node ID
- Account email or secret ref
- Capabilities
- Models
- Health
- Auth state
- Usage state
- Current concurrency
- p95 first token latency
- Recent error rate
- Shim/CLI/image version

Provider actions:

- Drain
- Disable routing
- Force refresh
- Restart runtime
- Upgrade image

Actions must be separated visually and semantically.

## Nodes And Containers

Node row:

- Host name
- Node ID
- Agent version
- WebSocket state
- Last heartbeat
- CPU/memory/disk
- Docker/Podman state
- Running provider count
- Active requests
- Last node error

Container row:

- Provider ID
- Service
- Account
- Host name
- Image version
- Shim version
- CLI version
- State
- Auth file state
- Local server health
- Current sessions
- Memory current/peak
- OOM count
- Last refresh result
- Last upgrade result

`Drain` is the default safe operation. Restart and disable routing are separate.

## Auth

Auth is displayed by provider account.

Avoid exposing internal `truth` terminology. Use:

- selected source
- replicas
- conflicts

Auth row:

- Service
- Account email/id
- Host name
- Provider instances
- Selected source
- Replica count
- Status
- Expires at
- Refresh capability
- Last successful probe
- Last refresh attempt
- Last sync
- Nodes containing copy

Manual refresh preview:

- target provider/account/container
- current auth status
- refresh command
- recent failures
- routing impact
- fallback availability

## Users And API Keys

User row:

- user
- group
- status
- allowed models
- active API keys
- daily/monthly usage
- quota pressure
- last request

API key row:

- key prefix
- owner
- created at
- expires at
- last used
- allowed models
- rate limit
- quota
- status
- rotation state

Raw key is shown only once at creation.

## Quotas

Quota views separate internal quota and provider native quota.

Dimensions:

- user
- API key
- group
- model alias
- provider service
- provider account
- provider pool

Values:

- internal remaining
- provider native remaining
- rate limit
- daily/monthly limit
- reserved usage
- committed usage
- released usage
- reset time

Over-limit actions:

- reject
- fallback
- downgrade
- queue
- require admin approval

## Usage

Dimensions:

- time range
- user
- API key
- model alias
- service
- provider account
- host name
- node
- container
- route id

Metrics:

- requests
- input tokens
- output tokens
- total tokens
- provider reported usage
- router estimated usage
- drift
- cost estimate
- error count
- cancellation count
- average latency
- p95 first-token latency
- tokens/sec

## Request Trace

Trace stages:

- incoming request
- API key authentication
- user/quota lookup
- model alias resolution
- route policy evaluation
- provider scoring
- quota reservation
- stream token issuance
- reverse data stream acquire
- provider invocation
- streaming response
- usage reconciliation
- quota commit/release
- final response

Prompt body is not stored by default. Show payload hash and size.

## Events And Audit

Event types:

- request accepted/rejected
- API key created/disabled/rotated
- quota reserved/committed/released
- route policy drafted/published/rolled back
- provider registered/drained/disabled
- stream opened/closed/cancelled
- auth copied/refreshed/synced/conflicted
- container started/restarted/upgraded
- admin action approved/rejected

Common fields:

- actor
- target
- reason
- before/after diff
- request id
- route id
- host name
- provider id
- timestamp

## Safe Controls

Actions requiring confirmation and reason:

- Disable routing
- Restart container
- Force auth refresh
- Upgrade image
- Drain provider
- Publish route policy
- Rollback route policy
- Rotate API key
- Revoke user access

Bulk actions require dry-run first with affected users, routes, providers,
fallback availability, and estimated downtime.
