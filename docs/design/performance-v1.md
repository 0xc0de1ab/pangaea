# Performance Design v1 Draft

이 문서는 Pangaea monorepo LLM runtime platform의 성능/메모리 설계 초안이다.

현재 구현 문서가 아니라 미래 구조 설계 문서다.

## Goals

- WebSocket control plane과 request data plane을 분리한다.
- Streaming 요청은 full-buffering 없이 처리한다.
- End-to-end backpressure를 보장한다.
- Provider/container/node 단위 latency와 resource를 관찰 가능하게 한다.
- 많은 provider instance가 있어도 route decision을 빠르게 유지한다.

## Warm Stream Pools

Provider instance는 router로 idle data stream을 미리 열어둔다.

Config knobs:

- `min_idle_streams`
- `max_idle_streams`
- `max_active_streams`
- `stream_idle_ttl`
- `stream_acquire_timeout`

Default route path:

1. Router selects provider.
2. Router acquires warm stream.
3. Router assigns stream with scoped token.
4. Request/response flows over data stream.

Per-request stream creation is fallback only.

Metrics:

- stream acquire latency
- warm stream hit ratio
- cold stream fallback count
- stream open failures
- stream lifetime
- active stream count

## Backpressure

Unbounded queues are forbidden.

Requirements:

- bounded buffers between router, shim, provider bridge
- client disconnect cancels provider execution
- provider timeout and router timeout are separate
- half-close supported
- idle timeout supported
- request/response body is not fully buffered

Streaming chunks flush at event/token boundary where possible.

## Streaming Transforms

Public API streams and provider native streams are transformed through canonical
events.

Canonical stream events:

- message start
- text delta
- tool call delta
- tool result
- thinking/reasoning metadata when allowed
- usage delta
- error
- done

The transform layer must not buffer the entire response before producing output.

## Registry Scaling

Router registry indexes provider instances by:

- capability
- model alias
- service
- account
- host_name/node
- health
- auth state
- quota state

Heartbeat strategy:

- frequent delta heartbeat
- periodic full inventory snapshot
- jitter per node
- stale session eviction
- provider generation numbers

Routing score inputs:

- route weight
- auth freshness
- queue depth
- warm stream availability
- provider concurrency
- native quota remaining
- user quota remaining
- recent error rate
- p95 first-token latency
- resource pressure
- drain state

## Container Resource Metrics

Node agent reports per-container metrics:

- CPU usage
- memory current
- memory peak
- memory limit
- OOM count
- restart count
- filesystem usage
- network rx/tx
- process count
- open FD count when available
- local server health
- shim version
- CLI version
- image digest

Readiness requires:

- auth valid
- provider local server ready
- shim connected
- models discovered
- warm stream pool initialized
- recent health probe success

## Cold Start Avoidance

Provider containers are warm runtime units. They are not created on request path.

Cold start allowed only for:

- bootstrap
- upgrade
- crash recovery
- explicit scale-out
- quarantine recovery

Update sequence:

1. drain provider
2. wait for active streams
3. snapshot auth/state
4. update image/container
5. restore auth
6. warm up local server
7. discover models
8. synthetic probe
9. re-enable routing

## Observability

Every request carries:

- `request_id`
- `tenant_id`
- `user_id`
- `api_key_id`
- `route_id`
- `provider_type`
- `provider_instance_id`
- `node_id`
- `host_name`
- `container_id`
- `model`
- `capability`

Latency metrics:

- route decision latency
- quota reservation latency
- stream acquire latency
- provider request start latency
- first byte latency
- first token latency
- total request latency
- provider completion latency

Streaming metrics:

- tokens/sec
- bytes/sec
- chunks/sec
- abort reason
- client disconnect count
- provider cancel success/failure
- backpressure wait time
- buffer high-water mark

Reliability metrics:

- route miss count
- provider error rate
- shim disconnect count
- stale heartbeat count
- retry count
- fallback count
- quota rollback count
- orphan provider process count

## Profiling

Router, node-agent, and provider-shim expose profiling endpoints only on admin
listener or authenticated admin route.

Profiles:

- CPU
- heap
- goroutine
- mutex
- block
- allocs

Rules:

- profile endpoint requires admin auth
- profile capture is audited
- prompt/token/auth secret must not be used as profile labels
- high-load 30s CPU profile should be safe to collect

## Load Testing

Synthetic load tests start in MVP.

Scenarios:

- 1 concurrent streaming request
- 10 concurrent
- 100 concurrent
- 1000 target
- slow client
- slow first token
- mid-stream provider error
- client disconnect
- router restart
- shim reconnect
- warm stream exhaustion
- quota race

Success criteria:

- memory growth is bounded and roughly linear
- no goroutine/stream/container handle leaks after completion
- p95/p99 first-token latency measurable
- no duplicate quota charge
- cancel leaves no provider orphan work

## Memory Safety

Budgets:

- max request body bytes
- max response transform buffer
- per-stream memory budget
- per-user active streams
- per-provider active streams
- bounded retry queue
- bounded event queue

Router stores metadata by default, not response bodies.

Provider memory baseline should feed scheduler and route scoring. OOM moves a
provider instance to degraded or quarantined state.
