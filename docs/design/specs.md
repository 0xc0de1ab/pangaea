# claude-creds-share — 상세 설계 명세

> 본 문서는 20년차 시니어 개발자 2인(Park, Ryu), 시스템 아키텍트 2인(Choi, Lim),
> UX/UI 디자이너 2인(Han, Seo), 테스트 엔지니어 1인(Kim), 보안 전문가 1인(Yoon)이
> 참여한 설계 리뷰의 결과를 취합한 것이다. 각 절 끝의 **패널 의견**은 이견이
> 있었던 지점만 정직하게 남겼다.

---

## 0. TL;DR

- **목적:** 여러 호스트에 흩어진 동일 사용자 계정의 `~/.claude/.credentials.json`을
  중앙 서버를 중재자로 두고 mTLS WebSocket으로 동기화한다. "가장 expiry가 늦게
  남은 토큰"을 정답으로 선정해 그것을 갖지 않은 노드들에 역전파한다.
- **첫 포맷:** `claude-credentials-json-format`. 향후 다른 토큰 파일도 동일 골격으로
  추가될 수 있게 `FormatRegistry`로 확장 가능.
- **신뢰 모델:** 서버가 self-signed CA. CA가 서버 인증서(SAN: IP/도메인)와 클라이언트
  인증서를 발급. 양방향 mTLS. CRL은 1차 범위 제외 — 단명 클라이언트 인증서 + 재발급
  운영으로 대체.
- **동기화 의미론:** "가장 expiry 늦게 남은 단일 진본" 모델(single-truth-by-expiry).
  본 1차 범위는 토큰 **갱신 자체는 하지 않는다.** 갱신은 사용자/Claude CLI가
  수행하고, 본 시스템은 그 결과를 다른 노드로 옮기는 역할만 맡는다.
- **언어/스택:** Go 1.22+, Gin, `gorilla/websocket`, `fsnotify`, `gofrs/flock`,
  `log/slog`, **cobra + viper** (CLI/설정), **`github.com/dh-kam/refutils`의
  `flagsbinder`** (struct ↔ flag/viper 자동 바인딩), `gopkg.in/yaml.v3`.
- **CLI 표면:** `serve`(서버), `connect`(클라이언트), `ca *`(PKI), `inspect`,
  `status`, `version`. `serve`에는 `--also-client <profile,...>` 옵션이 있어
  서버 프로세스 안에서 지정 profile의 클라이언트 에이전트도 별도 goroutine으로
  함께 실행한다.

---

## 1. 범위와 비-범위

### 1.1 범위(In-scope)
- 서버/클라이언트 단일 바이너리(`claude-creds-share` with subcommands)
- `serve`(서버) / `connect`(클라이언트) 커맨드. `serve --also-client <list>`로
  서버 프로세스 안에 클라이언트 에이전트도 함께 실행.
- self-signed CA, 서버/클라이언트 인증서 발급 CLI
- mTLS over TLS 1.3 WebSocket 연결, profile 단위 endpoint
- profile 정의(`profiles.yaml`)와 hot-reload(SIGHUP)
- 파일 watcher(fsnotify, pure Go)
- format 추상화 + `claude-credentials-json-format` 구현
- 서버 중재 동기화: **이미 만들어져 있는** credentials 파일을 수집 → 비교 → 역전파
- 구조화 로깅(slog/JSON), redaction
- 클라이언트 자동 재접속 5초 + 지터

### 1.2 비-범위(Out-of-scope, 명시 제외)
- **토큰 갱신/대행 일체.** 본 도구는 OAuth refresh flow를 절대 수행하지 않는다.
  Claude CLI가 갱신해 디스크에 써 둔 결과 파일을 **있는 그대로 공유**할 뿐이다.
  Claude Code CLI 내부 동작에 대한 조사 결과(`.credentials.json` 스키마,
  `expiresAt` 의미, `/oauth/profile`·`/oauth/usage` 응답 등)는 **그 파일을 어떻게
  인식·해석·우선순위 비교할지를 알기 위한 참고 정보일 뿐**이며, 같은 endpoint를
  본 도구가 호출해 토큰을 새로 받아오는 일은 없다. (live_check은 토큰 유효성
  '판독'에 한정.)
- 다중 계정 격리. 동일 계정의 같은 `.credentials.json`이 노드별로 흩어진 케이스만 다룸.
- CRL/OCSP, HSM 연동, 자동 PKI 로테이션. 향후 항목.
- macOS Keychain 직접 통합. 1차는 파일 기반만.
- 오프라인/P2P 동기화. 서버 중재 전제.

---

## 2. 용어

| 용어 | 정의 |
|---|---|
| **Server** | profiles와 CA를 보유하고 모든 client의 WebSocket을 종단 처리하는 중재자 노드. 자기 자신도 watcher 노드로 등록 가능. |
| **Client(=Node)** | 한 호스트에서 동작하면서 지정된 profile에 연결, 해당 profile이 가리키는 로컬 파일(들)을 watch하고 서버에 보고/수신하는 데몬. |
| **Profile** | 하나의 동기화 그룹. 이름·format·후보 파일 경로·정책을 묶음. |
| **Format** | 파일 내용 해석기. parse / validate / compare 책임. |
| **Snapshot** | 한 노드가 특정 시점에 가진 파일 내용을 format으로 해석한 표준 표현. |
| **Authoritative Snapshot** | 한 profile 내에서 서버가 "지금 정답"으로 선정한 snapshot. |

---

## 3. 시스템 구조

```
                                 ┌─────────────────────────────────────┐
                                 │              Server                 │
                                 │  ┌────────┐   ┌──────────────────┐  │
                                 │  │  CA    │   │ profiles.yaml    │  │
   ┌───────────────┐             │  └────────┘   └──────────────────┘  │
   │  Client A     │             │  ┌──────────────────────────────┐   │
   │ ~/.claude/    │◀── mTLS ───▶│  │   Mediator (per profile)     │   │
   │  .credentials │  WSS        │  │   ┌─────────┐ ┌────────────┐ │   │
   │  watcher+fmt  │             │  │   │ State   │ │  Logger    │ │   │
   └───────────────┘             │  │   └─────────┘ └────────────┘ │   │
                                 │  └──────────────────────────────┘   │
   ┌───────────────┐             │            ▲       ▲                │
   │  Client B     │◀── mTLS ───▶│            │       │                │
   │ ...           │  WSS        │  (server는 자기 자신도 노드로 동작 가능)
   └───────────────┘             └─────────────────────────────────────┘
   ┌───────────────┐
   │  Client C     │◀── mTLS ───▶ (동일)
   └───────────────┘
```

서버는 profile별 **Mediator** goroutine을 가진다. Mediator는 (1) 연결된 노드들의
snapshot 목록을 메모리에 들고, (2) `Format.Compare`로 정렬, (3) 1위 snapshot을
가지지 않은 노드들에게 push 메시지를 보낸다. Mediator는 stateless에 가깝게
유지하고, 디스크 영속화는 1차 범위에서 제외(서버 재시작 시 노드들이 재보고).

---

## 4. PKI / mTLS 상세

### 4.1 CA 부트스트랩

`claude-creds-share ca init --out ./pki` 실행 시:
- 4096-bit RSA(또는 P-256 ECDSA, 기본은 ECDSA P-256으로 한다 — 빠르고 충분)
- `CN=claude-creds-share Root CA`, `notAfter=10y`, `BasicConstraints: CA:TRUE, pathlen:0`
- 출력물: `pki/ca.key`(권한 0600), `pki/ca.crt`

### 4.2 서버 인증서

`claude-creds-share ca issue-server --ca ./pki --san IP:10.0.0.5,DNS:hub.local --out ./pki/server`
- `KeyUsage: digitalSignature, keyEncipherment`, `ExtKeyUsage: serverAuth`
- SAN에 사용자가 지정한 IP/DNS 다중 등록
- `notAfter=1y` 기본(설정 가능)

### 4.3 클라이언트 인증서

`claude-creds-share ca issue-client --ca ./pki --cn host-A --out ./pki/host-A`
- `ExtKeyUsage: clientAuth`
- `CN`은 노드 식별자(로깅·ACL의 키)
- 발급물(`host-A.crt`, `host-A.key`)을 OOB(scp/USB/시크릿 매니저)로 해당 호스트에
  전달. CA 공개 인증서(`ca.crt`)도 함께 전달.

### 4.4 TLS 설정

서버:
```go
tls.Config{
    MinVersion:   tls.VersionTLS13,
    Certificates: []tls.Certificate{serverCert},
    ClientCAs:    caPool,
    ClientAuth:   tls.RequireAndVerifyClientCert,
}
```
클라이언트:
```go
tls.Config{
    MinVersion:   tls.VersionTLS13,
    RootCAs:      caPool,
    Certificates: []tls.Certificate{clientCert},
    ServerName:   "<설정값 또는 SAN과 매칭되는 호스트>",
}
```

### 4.5 인가(authz)는 인증과 분리

mTLS는 "이 인증서가 우리 CA가 발급한 것"임을 증명할 뿐 "이 인증서가 어떤 profile에
접근할 수 있는지"는 별개. `profiles.yaml`에 `allowed_clients: [host-A, host-B]`로
CN 기반 ACL을 둔다.

### 패널 의견 — PKI
- **Yoon(보안):** "self-signed CA를 그대로 디스크에 평문으로 두면 그 디스크의 침해
  = 신뢰 루트 침해다. 1차에선 0600 + 운영자 책임 명기로 가되, **로드맵에 OS keyring
  /SOPS/age 봉인을 명시하라.** 또한 CA 키는 서명 외엔 절대 사용·이동되지 않게
  운영문서에 못박을 것."
- **Lim(아키텍트):** "CRL이 없는 self-signed 운영에서 사고 대응의 유일한 수단은
  **CA 회전**이다. CA를 갈아엎으면 모든 client/server가 재발급. 운영 비용 큼. 그래서
  client 인증서 수명을 짧게(예: 90일) + 자동 재발급 절차를 v0.2 로드맵에 두자."
- **Choi(아키텍트):** "client 부트스트랩은 OOB가 정답. enrollment endpoint를 두면
  부트스트랩 토큰의 분실·재사용 문제가 곧바로 들어온다. 처음 버전은 '서버에서 발급
  → 운영자가 scp'로 단순화."

---

## 5. 디렉토리/패키지 레이아웃

```
claude-creds-share/
├── cmd/
│   └── claude-creds-share/      # cobra CLI entry. server/client/ca subcommands.
├── internal/
│   ├── config/                  # profiles.yaml, server config 로드
│   ├── server/
│   │   ├── http.go              # gin router, /ws/:profile, health
│   │   ├── mediator.go          # profile별 중재 로직
│   │   └── hub.go               # 연결 관리
│   ├── client/
│   │   ├── conn.go              # mTLS dial + 재접속 루프
│   │   └── agent.go             # watcher↔transport 결합
│   ├── transport/
│   │   ├── ws.go                # gorilla/websocket 래퍼
│   │   └── proto.go             # 메시지 스키마(JSON)
│   ├── pki/                     # CA/serve/client 인증서 발급 라이브러리
│   ├── watcher/                 # fsnotify 래퍼 + atomic re-read
│   ├── safeio/                  # flock + atomic write
│   └── logging/                 # slog 셋업, redaction helper
├── pkg/
│   └── formats/
│       ├── registry.go          # FormatRegistry
│       ├── format.go            # Format interface, Snapshot interface
│       └── claudecreds/         # claude-credentials-json-format 구현
├── docs/design/specs.md
├── Makefile
└── go.mod
```

**Park(개발자):** "`internal/`로 강제 가두면 외부 임베드가 막혀서 단위 테스트와
별도 도구 작성에서 불편할 수 있다. 하지만 1차에선 외부 SDK 노출 의도가 없으니
의도적 봉쇄로 둔다. `pkg/formats`만 외부 친화적으로 노출."

---

## 6. 설정 파일

### 6.1 서버 설정(`server.yaml`)
```yaml
listen: "0.0.0.0:8443"
pki:
  ca_cert: "./pki/ca.crt"
  server_cert: "./pki/server/server.crt"
  server_key: "./pki/server/server.key"
log:
  level: "info"   # debug|info|warn|error
  format: "json"  # json|text
profiles_file: "./profiles.yaml"
self_node:
  enabled: true            # 서버 자신도 노드로 참여할지
  client_cert: "./pki/server-as-node/host.crt"
  client_key:  "./pki/server-as-node/host.key"
```

### 6.2 `profiles.yaml`
```yaml
profiles:
  - name: "claude-prod"
    format: "claude-credentials-json-format"
    paths:
      - "~/.claude/.credentials.json"
      - "~/.config/claude/.credentials.json"
    allowed_clients: ["host-a", "host-b", "server-self"]
    validate:
      strategy: "expires_at_max"  # format이 정의하는 비교 전략 키
      live_check: true            # /api/oauth/profile 호출 여부
      live_check_timeout: "5s"
    propagate:
      mode: "to_stale_only"       # to_stale_only | to_all
      cooldown: "2s"              # 동일 진본을 반복 push 하지 않게
```

`paths`는 후보 위치들이며 **그 중 존재하는 것 모두**가 watch 대상. 한 호스트에서
한 파일이 두 경로에 동시에 존재할 수도 있음(심볼릭 링크/`CLAUDE_CONFIG_DIR` 분리).
이 경우 노드는 각 파일을 독립 snapshot으로 보고하고, 서버는 노드 단위가 아닌
**파일(=경로) 단위**로 비교한다.

### 6.3 클라이언트 설정(`client.yaml`)
```yaml
server: "wss://hub.local:8443"
profile: "claude-prod"
node_id: "host-a"            # 인증서 CN과 일치해야 함
pki:
  ca_cert: "./pki/ca.crt"
  client_cert: "./pki/host-a/host-a.crt"
  client_key:  "./pki/host-a/host-a.key"
reconnect:
  initial_delay: "5s"
  jitter: "1s"
  max_delay: "60s"
log: { level: "info", format: "json" }
```

### 패널 의견 — 설정
- **Han(UX):** "tilde 확장(`~/.claude/...`)을 라이브러리 차원에서 정확히 처리해야
  한다. `os.UserHomeDir()` 기반으로 해석하고, 환경변수 `CLAUDE_CONFIG_DIR`도
  존중하라(없을 때만 `~/.claude` 사용)."
- **Seo(UX):** "profiles.yaml 변경 시 reload는 SIGHUP으로. 잘못된 yaml은 기존
  구성을 유지하고 stderr로 친절한 에러를 출력. 부분 적용은 하지 마라."

---

## 7. WebSocket 프로토콜

### 7.1 엔드포인트
- `GET wss://<host>:<port>/ws/profile/{profile}` — Upgrade 요구.
- 서버는 (a) 클라이언트 인증서 검증, (b) profile 존재 확인, (c) 인증서 CN이
  `allowed_clients`에 있는지 확인. 실패 시 1008/1011 close.

### 7.2 메시지 포맷
JSON. 디버깅/스키마 진화의 단순함을 우선. 모든 메시지는 envelope:
```json
{"type": "<kind>", "v": 1, "id": "<uuid>", "ts": "<RFC3339Nano>", "payload": {...}}
```

| `type` | 방향 | 의미 |
|---|---|---|
| `hello` | C→S | 노드 자기소개. node_id, agent_version, OS, capabilities. |
| `welcome` | S→C | 서버가 받은 hello 확인. 현재 알려진 진본 메타(있다면) 동봉. |
| `snapshot.report` | C→S | 노드가 본 파일 1건의 snapshot 보고. 아래 7.3 참조. |
| `snapshot.absent` | C→S | 후보 경로에 파일이 하나도 없다는 보고. |
| `truth.push` | S→C | 서버가 정한 진본을 노드에 전달. payload에 raw bytes(base64) + 메타. |
| `truth.ack` | C→S | 노드가 적용 완료/실패를 보고. |
| `error` | both | 프로토콜 오류 통지. graceful close 전 송신. |
| `ping/pong` | both | gorilla/websocket 기본 keepalive. 30초 간격. |

### 7.3 `snapshot.report` payload
```json
{
  "profile": "claude-prod",
  "path": "/home/alice/.claude/.credentials.json",
  "format": "claude-credentials-json-format",
  "fingerprint": "sha256:...",     // raw bytes 해시
  "summary": {                     // format이 만든 redact된 요약
    "identity": "user-id-or-hash",
    "expires_at": "2026-04-26T03:14:06Z",
    "scopes": ["user:profile", "..."],
    "subscription": "max"
  },
  "live_check": {
    "performed": true,
    "result": "ok",                 // ok | expired | revoked | unreachable
    "checked_at": "2026-04-25T12:00:01Z"
  },
  "raw_size": 612
}
```
**raw bytes 자체는 보내지 않는다.** 서버가 진본으로 채택한 노드에만 별도 요청해서
`truth.push`로 다른 노드에 분배한다(아래 9절). 이렇게 분리하면 (a) 평소 트래픽이
가볍고 (b) 노드가 진본이 아니면 토큰이 네트워크에 노출되지 않는다.

### 7.4 `truth.push` payload
```json
{
  "profile": "claude-prod",
  "format": "claude-credentials-json-format",
  "fingerprint": "sha256:...",
  "raw_b64": "eyJjbGF1ZGVB...",   // 원본 바이트 base64
  "target_paths": ["~/.claude/.credentials.json"],
  "issued_at": "2026-04-25T12:00:02Z",
  "summary": {...}                  // 7.3과 동일 redact 요약
}
```

### 7.5 흐름
```
hello → welcome → snapshot.report (각 후보 파일 1건씩, 변경마다)
                       ↓ (서버 중재)
                 ← truth.push  (필요 시)
                 → truth.ack
```

### 패널 의견 — 프로토콜
- **Park(개발자):** "JSON으로 시작하지만 `v: 1` 필드를 두어 향후 protobuf 전환을
  열어두자. fingerprint는 raw bytes 그대로(JSON canonical화 X) sha256."
- **Ryu(개발자):** "`snapshot.report`에서 raw bytes를 안 보내는 두-단계 방식이
  중요하다. 모든 노드가 매번 토큰을 흘리면 mTLS가 뚫리는 순간 피해 면적이
  최대화된다. **'진본만 raw 송신'** 원칙을 명문화."
- **Yoon(보안):** "Ryu 의견 +1. 그리고 `truth.push`의 `raw_b64`는 슬쩍 로그에
  찍히기 쉽다. 절대 logger에 payload 통째 찍지 마라. 본 명세에 'raw_b64는
  redaction 대상'으로 명기."

---

## 8. Format 추상화

### 8.1 인터페이스
```go
package formats

type Format interface {
    Name() string
    Parse(raw []byte) (Snapshot, error)
    Validate(ctx context.Context, snap Snapshot, opts ValidateOpts) (ValidationResult, error)
    Compare(a, b Snapshot) int    // -1: a older, 0: equal, +1: a newer
    Redact(snap Snapshot) Summary // 로그/네트워크 안전 요약
}

type Snapshot interface {
    Identity() string             // 동일 주체 판별 키 (예: user id, token hash)
    ExpiresAt() time.Time
    Raw() []byte                  // 원본 바이트(필요 시에만 노출)
    Fingerprint() string          // sha256 hex
}

type ValidationResult struct {
    Status   string             // ok | expired | revoked | unreachable | parse_error
    Detail   string
    CheckedAt time.Time
}

type ValidateOpts struct {
    LiveCheck bool
    Timeout   time.Duration
    HTTPClient *http.Client       // 테스트 주입용
    Clock     func() time.Time    // 테스트 주입용
}
```

### 8.2 FormatRegistry
```go
package formats

var registry = map[string]Format{}
func Register(f Format)                    { registry[f.Name()] = f }
func Get(name string) (Format, bool)       { f, ok := registry[name]; return f, ok }
func List() []string                       { /* sorted */ }
```
각 format 패키지는 `init()`에서 `formats.Register(&impl{})` 호출. 서버·클라이언트
양쪽이 동일한 registry 빌드(태그)로 컴파일되어야 함.

### 패널 의견 — Format
- **Choi(아키텍트):** "Compare를 'a vs b'로만 두지 말고, **선택 전략을 enum으로**
  설정에서 받아라. 가령 `expires_at_max`, `last_modified_max`, `custom_keyword` 등.
  format이 여러 전략을 등록할 수 있게 하면 다른 토큰 파일 추가가 쉽다."
  → 합의안: `Format`은 `Strategies() []string`을 노출. 설정의
  `validate.strategy`가 그 중 하나여야 함. `Compare`는 strategy 컨텍스트 내에서
  동작.
- **Kim(QA):** "Snapshot은 raw bytes + parsed view를 동시에 갖는데, 테스트가
  `Raw()`를 우회해 parsed만으로 단정하기 쉽다. Property test로 'Parse(Raw()) ==
  원본 Snapshot'을 강제하라."

---

## 9. `claude-credentials-json-format` 구현 명세

> **목적 한정:** 본 format은 디스크에 이미 존재하는 `.credentials.json`을 **읽고
> 비교하기 위한 해석기**일 뿐이다. 토큰을 새로 발급/갱신하는 일은 절대 하지
> 않는다. Claude Code CLI 동작에 대한 조사 결과는 (a) 어떤 키가 어떤 의미인지,
> (b) 어느 후보가 "더 새것"인지 판정하는 근거, (c) 토큰이 살아있는지 확인하는
> 가장 가벼운 방법을 결정하기 위한 자료로만 쓰인다.

### 9.1 입력 스키마(현 Claude Code v2.1.x 기준)
```json
{
  "claudeAiOauth": {
    "accessToken": "sk-ant-oat01-...",
    "refreshToken": "sk-ant-ort01-...",
    "expiresAt": 1777107446691,        // unix ms
    "scopes": ["user:profile", "user:inference", ...],
    "subscriptionType": "max",
    "rateLimitTier": "default_claude_max_20x"
  }
}
```
파일은 단일 OAuth 엔트리만 포함. 다중 계정은 본 1차 범위에서 명시적으로 비-지원
(설정 검증 시 `paths`가 동일 사용자 동일 계정의 동일 토큰을 가리킨다는 가정을
운영자가 책임진다고 README에 명기).

### 9.2 Parse
- JSON unmarshal → 구조체 매핑.
- 필수 필드 부재 시 `parse_error`.
- `expiresAt`이 과거여도 parse는 성공(상태는 Validate에서 결정).
- `Identity()`는 `sha256(accessToken)[0:16]`을 hex로(개인정보 노출 최소화).
  (계정 식별자가 토큰 내부에 명시되지 않는다는 한계 — 로그용도일 뿐 권한 판정에
  쓰지 않음.)
- `Fingerprint()`는 `sha256(raw_bytes)` 전체.

### 9.3 Validate
- `expires_at_max` 전략에서는 `expires_at > now`이면 후보로 인정.
- `live_check=true`면 `GET https://api.anthropic.com/api/oauth/profile`을
  `Authorization: Bearer <accessToken>`로 호출.
  - 200 → `ok`
  - 401 → `expired`(또는 revoked, 메시지로 구분 시도하되 동일 처리)
  - 403 → `ok` 단 `scope_warn` (validate 결과는 ok로 두고 로그에 경고)
  - 그 외/timeout → `unreachable`
- 위 엔드포인트는 **공식 문서가 없는 reverse-engineered API**이며, 변경 가능성
  존재. 따라서 Validate 호출 자체가 실패해도 "토큰이 죽었다"라고 단정하지 않고
  `unreachable`로 분류, **mediator는 unreachable인 후보를 후순위로**(완전 배제 X)
  취급.

### 9.4 Compare(`expires_at_max`)
1. `live_check.result == ok`인 후보들끼리 `expires_at` 큰 쪽이 우선.
2. 모두 `unreachable`이면 그래도 `expires_at` 큰 쪽 우선.
3. `expired`/`parse_error`는 후보 풀에서 제외.

### 9.5 Redact
로그/메시지에 노출 가능한 필드만:
- accessToken/refreshToken: 절대 미노출. 대신 `tail4=...XXXX` 4글자만.
- `expires_at`(ISO8601), `scopes`, `subscription`, `fingerprint(short)`,
  `identity(short)`.

### 패널 의견 — claudecreds
- **Yoon(보안):** "공식 문서 없는 `/oauth/profile`을 검증에 넣었다. 운영 중 401이
  의도치 않게 자주 나면 Anthropic 서버 측 변동 가능성을 의심해야 한다. 따라서
  **'401이 N분 내 K회 이상이면 live_check를 임시로 끄고 운영자 알림'**의
  서킷브레이커를 v0.2에 두자."
- **Park(개발자):** "single-use refreshToken 때문에 같은 파일을 두 노드에 동시에
  배포한 뒤 둘 다 Claude CLI로 갱신하면 한 쪽만 성공한다. 이는 **본 시스템 외부
  문제**지만, README와 CLI `--help`에 경고를 넣어 운영 사고를 줄이자."
- **Lim(아키텍트):** "Identity()를 token hash로 두면 갱신 후 identity가 바뀐다.
  이는 의도된 결과(=새로운 자격으로 간주). 단, 로그에서 같은 사용자의 진본
  교체 흐름을 보고 싶은 운영자에게는 식별이 어려우므로 **profile + path** 쌍을
  이력 추적의 키로 추가 사용한다."

---

## 10. 동기화 알고리즘 (서버 중재)

서버는 profile 단위로 메모리에 다음 자료구조를 둔다:

```go
type pathKey struct{ NodeID, Path string }
type ProfileState struct {
    Snapshots  map[pathKey]Report   // 모든 (node, path) 조합의 마지막 보고
    Truth      *Truth               // 현재 진본(없을 수 있음)
    LastPushed map[string]string    // nodeID -> fingerprint
}
type Truth struct {
    Fingerprint string
    Raw         []byte               // 진본 노드에서 가져온 원본
    SourceNode  string
    SourcePath  string
    SelectedAt  time.Time
    Summary     formats.Summary
}
```

### 10.1 진본 선정
이벤트(보고 도착, 노드 disconnect, profile reload)마다:
1. `live_check`가 안된 보고에 대해 서버측에서 한 번 더 verify(서버에 직접 인증
   토큰을 들고 있지 않으므로, format이 verify 책임을 담당. live_check.result는
   노드가 실행한 결과를 그대로 신뢰).
2. `Format.Compare`로 정렬, 1위 후보 하나를 선택.
3. 선택된 후보가 기존 `Truth.Fingerprint`와 다르면 진본 갱신:
   - 해당 노드에 `truth.fetch` 요청(payload에 raw bytes 첨부 응답) — 또는 보고
     단계에서 이미 raw를 가져오지 않으므로 별도 fetch round trip이 필요하다.
   - **간소화 결정:** 1차 구현은 `snapshot.report` 단계에서 raw bytes를 같이
     보내되, **TLS 위에서 + 메모리 보관 시 즉시 zero out**, 로깅 redaction 강제.
     2-단계 fetch는 v0.2 항목.
4. 새 Truth 확정 후, fingerprint가 다른 모든 노드에 `truth.push`.
5. `LastPushed[nodeID] = fingerprint`로 중복 push 차단(cooldown 안에서).

> **결정:** Yoon이 7.5에서 두-단계 분리를 제안했지만, Park/Choi는 1차 단순성을
> 우선해 단일 단계(snapshot.report에 raw 포함, 단 redaction·메모리 zero-out 강제)
> 로 가자고 했다. **합의안:** 1차는 단일 단계, v0.2에 두-단계 분리. 본 명세
> 7.3의 "raw bytes 자체는 보내지 않는다"를 다음과 같이 정정한다:
> *"1차에서는 raw bytes를 포함해 보고하되, 진본이 아닌 노드의 raw는 서버 메모리
> 에서 즉시 폐기(zero-out)한다. v0.2에서 두 단계 분리로 진화한다."*

### 10.2 동률(tie) 처리
- `expires_at`이 동일하면 `fingerprint` 사전순으로 결정(결정성 보장).

### 10.3 unreachable만 있을 때
- 모두 unreachable이고 `expired`도 아닌 케이스 → 최선의 후보를 진본으로 선정하되
  mediator는 `degraded` 상태 로그를 남긴다. propagate는 수행.

### 10.4 시계 동기화 가정
- 비교는 모두 서버 측 `now()` 기준. 노드가 보낸 `expires_at`는 절대 unix 시각이라
  서버 시계만 정확하면 충분. 서버는 NTP 동기화 운영 책임을 README에 명시.

### 패널 의견 — 동기화
- **Ryu(개발자):** "동률 처리는 의외로 자주 일어난다(같은 토큰이 두 노드에
  복제된 상태). fingerprint 우선이면 결정성은 OK. 단, 동률이 자주 발생하면
  '이미 동기화 완료'로 간주해 push를 생략하는 게 자원 절약."
- **Lim(아키텍트):** "v0.2 두-단계 분리로 갈 때, raw fetch를 진본 노드가 거부하면?
  → fallback으로 다음 후보로 강등. 구현은 단순한 priority queue."

---

## 11. 파일 안전성

### 11.1 쓰기
- 항상 **temp file + fsync + rename** 패턴.
- 같은 디렉토리에 `.credentials.json.tmp.<rand>`, `chmod 0600`, write, fsync,
  `os.Rename`.
- 디렉토리 fsync까지 수행해 rename 영속화 보장.

### 11.2 읽기/쓰기 동시성 — Claude CLI와의 경합
- Claude CLI는 file lock을 쓰지 않는다(조사 결과). 본 시스템이라도 lock을 쓰자:
  쓰기 직전 `gofrs/flock`로 `.credentials.json.lock` 별도 파일에 LOCK_EX, 0.5s
  내 획득 실패 시 backoff·재시도, 5회 실패 시 이번 cycle 포기 + 경고 로그.
- 별도 lock 파일을 쓰는 이유: `.credentials.json` 자체에 lock을 걸면 Claude CLI가
  parse 중 우리가 truncate하는 케이스가 발생할 수 있음. 별도 lock 파일은
  '본 시스템 끼리만'의 합의지만, 파일 자체 쓰기는 항상 atomic rename이라 Claude
  CLI 입장에선 partial read가 발생하지 않는다.

### 11.3 적용 후 검증
- `truth.push` 적용 직후 다시 한 번 Parse + Validate(live_check=false)로 무결성
  확인. 실패 시 즉시 `truth.ack {ok=false, reason=...}`. 진본 적용에 실패한 노드는
  롤백 — 적용 전 백업 파일 `.credentials.json.bak.<ts>`을 만들고, ack=false면
  rename으로 복원.

### 패널 의견 — 파일 안전성
- **Yoon(보안):** "백업 파일 `.bak.*`도 평문 토큰. 권한 0600은 기본, 그리고 적용
  성공 후 즉시 삭제(unlink). 디스크에 쓸데없이 토큰을 더 두지 마라."
- **Kim(QA):** "atomic rename·flock 코드는 race condition test가 어렵다. fault
  injection — `safeio` 레이어에 hook을 두어 `fsync`/`rename` 직전 panic을 주입,
  복구 동작을 검증."

---

## 12. Watcher

- `fsnotify` 사용. 단, **삭제 후 재생성**(temp+rename) 패턴을 정확히 잡으려면
  '디렉토리'를 watch한 뒤 파일 이벤트를 필터링한다(파일 자체를 watch하면 inode
  교체로 watch가 끊어진다).
- 첫 실행 시: `paths` 후보 각각에 대해 (a) 존재하면 즉시 1회 read+report,
  (b) 부재면 `snapshot.absent` 보고. 그 후 디렉토리 watch 시작.
- 변경 디바운스: 50ms 코어 + 200ms 파일 안정화(`stat.Size` 동일 2회 확인) 후
  실제 read.
- watcher → agent 채널 → transport. 백프레셔: 채널이 가득 차면 가장 오래된
  이벤트 1건만 코얼레스(파일별로 최신만 유효).

### 패널 의견 — Watcher
- **Park(개발자):** "디렉토리 watch가 정답. WSL2/맥에서 inode 교체 케이스가 가장
  악명 높다."

---

## 13. 클라이언트 재접속

- 초기 5초 + 지터 ±1초.
- 연속 실패 시 expo backoff: 5s → 10s → 20s → 40s → 60s 캡.
- TLS handshake 실패는 즉시 fail이지만 재접속은 동일 정책.
- 메시지 버퍼: 끊긴 동안 watcher가 만든 보고 중 **각 path의 최신 1건만 유지**.
  재연결 직후 hello → snapshot.report 를 일괄 송신.
- 기동 시 첫 연결 실패도 동일 backoff. CI/dev에서 답답하니 `--fail-fast`
  플래그로 즉시 종료 옵션 제공.

### 패널 의견 — 재접속
- **Han(UX):** "다시 시도 카운트와 다음 시도 시각을 stderr에 명확히. 'Connection
  closed (eof). Reconnecting in 8.2s (attempt #4)' 정도. 색상 환경 감지(`isatty`)."

---

## 14. 로깅

- `log/slog` JSON. 모든 라인에 `profile`, `node_id`, `event`, `outcome` 필수
  필드. token/raw payload는 절대 로깅 금지(필드 redactor를 통과시켜 자동
  마스킹).

### 14.1 사용자가 명시 요구한 로깅 항목 — 검증 매트릭스

| 사용자 요구 | 로그 위치 / 이벤트 | 비고 |
|---|---|---|
| 각 노드가 가진 파일이 적절한 format에 따라 파싱된 결과 | `event=snapshot.parsed` (노드측) | summary(redacted), fingerprint, expires_at |
| usage(=live_check)로 검증된 내용들 | `event=snapshot.validated` (노드측, 서버측 둘 다 가능) | result, latency, scope_warn |
| 후보들의 리스트 | `event=mediator.candidates` (서버측) | per-profile, ordered list with rank |
| 최종 선정된 유효 인증 정보 | `event=mediator.truth_selected` (서버측) | source_node, fingerprint, expires_at |
| 역으로 전파된 내용 | `event=truth.pushed` (서버측), `event=truth.applied` (노드측) | target_node_ids, fingerprint, ack 결과 |

### 14.2 redactor
`logging` 패키지에 `slog.Handler`를 감싸는 wrapper. 키 `raw`, `raw_b64`,
`accessToken`, `refreshToken`, `Authorization` 등을 자동으로 `<redacted>`로 치환.

### 패널 의견 — 로깅
- **Yoon(보안):** "redactor는 키 이름 매칭이 한계. 값에 `sk-ant-oat01-`, `Bearer `
  prefix가 보이면 마스킹하는 **값 기반 패턴 매칭**도 추가해라."
- **Seo(UX):** "운영자 입장에서 JSON만 있으면 사람이 못 읽는다. `--log-format=text`
  도 1급 지원. 또는 `claude-creds-share log tail`로 사람-친화 포맷 변환 명령."

---

## 15. CLI

**cobra + viper + `github.com/dh-kam/refutils/flagsbinder`** 조합. 각 커맨드는
입력 파라미터를 struct로 정의하고 flagsbinder로 (a) cobra flag 등록, (b) viper
key 바인딩, (c) 환경변수 매핑을 한 번에 처리한다.

```
claude-creds-share
├── ca
│   ├── init                        # CA 생성
│   ├── issue-server                # 서버 인증서 발급 (SAN: IP/DNS multi)
│   └── issue-client                # 클라이언트 인증서 발급
├── serve   [--config server.yaml] [--also-client p1,p2,...]
├── connect [--config client.yaml]  # = 클라이언트
├── status                          # 로컬 데몬 상태
├── inspect <path>                  # 단일 .credentials.json을 format으로 검사
└── version
```

### 15.1 `serve` 커맨드 옵션 struct (예시 — flagsbinder 적용)
```go
type ServeFlags struct {
    Config      string   `flag:"config,c"      env:"CCS_CONFIG"      default:"server.yaml" usage:"server.yaml 경로"`
    AlsoClient  []string `flag:"also-client"   env:"CCS_ALSO_CLIENT" usage:"이 서버 프로세스에서 함께 실행할 client profile 이름들 (쉼표로 여러 개)"`
    LogLevel    string   `flag:"log-level"     env:"CCS_LOG_LEVEL"   default:"info"`
    LogFormat   string   `flag:"log-format"    env:"CCS_LOG_FORMAT"  default:"json"`
}
// flagsbinder.Bind(cmd, viper.GetViper(), &flags) 한 줄로 등록.
```
- `--also-client claude-prod,claude-dev`처럼 명시한 profile들에 대해 `serve`는
  서버 라우트를 띄움과 **동시에** 별도 goroutine 그룹(아래 15.3)을 만들어 자기
  서버에 mTLS WSS로 dial-in한다.

### 15.2 `connect` 커맨드 옵션 struct
```go
type ConnectFlags struct {
    Config   string `flag:"config,c" env:"CCS_CONFIG" default:"client.yaml"`
    Server   string `flag:"server"   env:"CCS_SERVER"  usage:"wss://host:port"`
    Profile  string `flag:"profile"  env:"CCS_PROFILE" usage:"접속할 profile 이름"`
    NodeID   string `flag:"node-id"  env:"CCS_NODE_ID" usage:"client 인증서 CN과 일치"`
    LogLevel string `flag:"log-level" env:"CCS_LOG_LEVEL" default:"info"`
}
```
플래그/환경변수/yaml 모두 동일 struct에 채워지며, 우선순위는
**flag > env > config(yaml) > default** (viper 기본 동작).

### 15.3 `serve --also-client` 동작 모델
- 서버 부팅 후, `--also-client`로 지정된 profile마다 `agent.Run(ctx, profileX)`
  goroutine을 띄운다. 이 에이전트는 일반 `connect`가 하는 일과 동일하지만,
  **dial 대상이 자기 자신**(`https://127.0.0.1:<listen_port>` 또는 설정된 SAN
  중 loopback에 매칭되는 것).
- 인증서: `server.yaml`의 `self_node` 섹션에 별도 client 인증서 경로를 둔다(4.5
  ACL과 일관). 자기-루프 접속도 mTLS handshake를 정상적으로 통과해야 한다 —
  loopback이라서 봐주는 우회로는 두지 않는다.
- 라이프사이클: `errgroup`으로 묶어서 (server, also-client #1, also-client #2 …)
  중 하나가 종료되면 전체 graceful shutdown. 신호 처리는 server 메인이 단독.
- 로깅: 메인 logger에 `component=server` / `component=self-client profile=...`
  필드를 부여해 분간 가능하게.

`status`는 로컬 unix socket(`$XDG_RUNTIME_DIR/claude-creds-share.sock`)에 질의해
현재 연결 상태/마지막 진본/last error를 보여준다.

### 패널 의견 — CLI
- **Han(UX):** "`inspect` 좋다. 운영자가 '내 파일이 왜 진본으로 안 뽑히지?'를 가장
  자주 묻는다. parse 결과 + redacted summary + live_check 결과를 한 화면에."
- **Choi(아키텍트):** "ca 서브커맨드는 별도 바이너리로 분리도 고려. 1차는 단일
  바이너리로 합쳐서 배포 단순화."
- **Park(개발자):** "`serve --also-client`는 편의 기능이지만 디버깅이 어려워질
  수 있다. 로그 필드(`component`)와 별도 metric 라벨을 처음부터 분리해
  두자."
- **Lim(아키텍트):** "self-loop을 mTLS로 그대로 통과시키는 결정에 동의. 우회로
  를 만들면 ACL/감사 일관성이 깨진다. 약간의 핸드셰이크 비용은 아끼지 마라."

---

## 16. 보안 위협 모델

### 16.1 자산
- (A1) Anthropic OAuth accessToken / refreshToken
- (A2) self-signed CA private key
- (A3) 서버/클라이언트 인증서 private key

### 16.2 행위자 / 공격
| ID | 위협 | 완화 |
|---|---|---|
| T1 | 네트워크 도청 | TLS 1.3 + mTLS, 요약 외 raw 전달은 진본 노드만 |
| T2 | MITM | mTLS 서버 인증서 SAN 검증, CA 핀닝 |
| T3 | 비인가 클라이언트 접속 | mTLS + CN 기반 ACL(`allowed_clients`) |
| T4 | 서버 디스크 침해(CA 키 유출) | 0600 + 운영문서 명기. v0.2 OS keyring 봉인 |
| T5 | 노드 디스크 침해(token 유출) | 본 시스템 외 문제. 단, `.bak` 파일 즉시 unlink |
| T6 | 로그 유출 통한 token 노출 | redactor + 값 패턴 매칭 |
| T7 | 무한 재접속 DoS | server측 rate limit per CN, exponential backoff |
| T8 | refreshToken race | 1차에선 갱신 안 함. v0.2에 '서버 단일 갱신자' 패턴 |
| T9 | 시계 왜곡 공격 | 서버 단일 시각 기준 비교. 노드 시각 비신뢰 |
| T10 | 공식 미문서화 API 변경 | live_check 서킷브레이커, profile별 비활성 가능 |

### 패널 의견 — 보안
- **Yoon(보안):** "T7의 '서버측 rate limit'은 CN 기준이지만 attacker가 mTLS를
  못 뚫었을 때 IP 기반 fail2ban 같은 OS 레벨 보호도 운영문서에. 그리고 '한 CN
  당 동시 1세션'이 본 시스템에서 더 안전하다 — 같은 CN 다중 접속 차단."
- **Lim(아키텍트):** "T8 '갱신 안 함'을 1차로 둔 이유를 README 최상단에 적자.
  '본 도구는 갱신을 하지 않는다. 한 노드(주로 사용자가 자주 쓰는 노드)에서
  Claude CLI가 갱신하면, 그 결과를 다른 노드로 전파하는 데만 책임이 있다.' "

---

## 17. 테스트 전략

### 17.1 단위
- `formats/claudecreds`: 골든 픽스처(JSON 샘플) + table-driven Parse/Compare,
  Property test로 `Parse(Raw())==원본`.
- `safeio`: fault injection으로 fsync/rename 실패 복구 검증.
- `pki`: 발급된 인증서 chain verify, SAN 매칭, NotBefore/NotAfter 경계.
- `transport/proto`: envelope round-trip JSON.

### 17.2 통합
- 임시 디렉토리에 CA + server cert + 2 client cert 발급.
- in-process 서버 + 2 client agent. fsnotify는 실제 파일 사용.
- 시나리오: (a) 첫 기동시 한 노드만 파일 보유 → 다른 노드에 전파 확인. (b)
  파일 변경 → 새 진본 선정 → 전파. (c) 한 노드 disconnect → 잔여 노드만으로
  진본 갱신. (d) 모두 expired → propagate 안 함, 경고 로그.

### 17.3 E2E (도커)
- `docker compose up`으로 server + 2 client 컨테이너. profile 한 개.
- 셸 스크립트로 한 컨테이너에 새 `.credentials.json` 주입 → 다른 컨테이너에서
  파일이 동일해지는지 hash 비교.

### 17.4 시간/네트워크 의존성
- `clock` interface 주입(`func() time.Time`).
- live_check용 HTTP는 `httptest.Server`로 가짜 401/200/timeout 시나리오 생성.

### 패널 의견 — 테스트
- **Kim(QA):** "fsnotify 통합 테스트는 OS별 동작차가 크다. CI에서 Linux + macOS
  매트릭스. WSL2에서 `inotify`는 일부 이벤트 손실 케이스 보고가 있어 운영
  매뉴얼에 함께 적자."
- **Park(개발자):** "race detector(`go test -race`)는 PR 게이트로 강제."

---

## 18. UX/UI 종합

CLI-only 시스템이지만 사용자가 '실제로 동작하는지' 빠르게 확인하는 길을
디자이너 패널이 강조했다.

- **Han(UX):** 첫 5분 경험(first-run UX)
  1. `claude-creds-share ca init` → 친절한 출력에 다음 단계 가이드.
  2. `claude-creds-share ca issue-server --san IP:...,DNS:...` → 결과 파일 위치
     출력 + "이 파일들을 서버에 두고 server.yaml 작성"이라는 안내.
  3. `claude-creds-share ca issue-client --cn host-a` → "이 두 파일과 ca.crt를
     호스트 host-a로 안전하게 옮기세요" 가이드.
  4. `claude-creds-share server run` 첫 실행 시 `profiles.yaml`이 없으면 친절한
     샘플과 함께 종료(빈 런이 아니라 actionable error).

- **Seo(UX):** 에러 메시지 톤
  - "연결 끊김 → 5.4초 후 재시도 (시도 #3)" 처럼 **다음 무엇이 일어날지**를 항상
    함께. 로그와 사용자-stderr 출력은 분리(JSON은 파일/syslog로, 사람용 출력은
    stderr).
  - validation 결과는 "이 토큰은 2시간 24분 후 만료됩니다 — 정상" 같은 자연어
    요약을 status에서.

- **공통:** systemd unit 파일 샘플(`Install/`)과 launchctl plist 샘플 동봉.

---

## 19. Makefile (요약 — 별도 setup 스킬로 정합성 맞출 것)

```
make build         # 단일 바이너리
make test          # unit
make integration   # tag로 분리한 통합
make race          # go test -race
make lint          # golangci-lint
make fmt           # gofmt + goimports
make ca-dev        # 개발용 CA 셋 자동 생성(./pki-dev)
make demo          # docker compose up
```

> **메모:** CLI 셋업은 **cobra + viper + `github.com/dh-kam/refutils`의
> `flagsbinder`** 조합으로 직접 구성한다(가용 스킬 목록에 `setup-golang-cli`가
> 없어 보일러플레이트 자동 생성은 이용하지 않음). `flagsbinder`는 struct 태그로
> flag 선언/viper 키/환경변수 매핑을 한 번에 처리하므로 커맨드별 입력 정의가
> 한 곳에 모인다. `go.mod`에 `github.com/dh-kam/refutils` 의존성을 추가하고,
> 사설 git 접근이 필요하면 `GOPRIVATE=github.com/dh-kam/*` 안내를 README에.

---

## 20. 결정 보류 / 향후 로드맵

| ID | 항목 | 결정 |
|---|---|---|
| O1 | 서버를 단일 갱신자로 두는 능동 OAuth refresh | v0.2 |
| O2 | snapshot.report에서 raw 분리(두 단계) | v0.2 |
| O3 | CA 키 OS keyring 봉인 | v0.2 |
| O4 | 클라이언트 인증서 자동 재발급(enrollment) | v0.3 |
| O5 | macOS Keychain credentials 통합 | v0.3 |
| O6 | 다중 계정/profile별 라우팅 | v0.3 |
| O7 | profile별 메트릭(prometheus) | v0.2 |

---

## 21. 패널 토론 — 가감없는 의견 모음

> 본 절은 합의로 정리되지 않은, 또는 합의했지만 기록 가치가 있는 발언들을
> 그대로 둔다. 이견을 의도적으로 보존했다.

**Park(개발자, Go 런타임):** "초기 버전에 욕심내지 마라. v1은 '갱신은 안 한다'를
명확히 못 박아 동작 보증 면적을 줄여야 한다. UI가 깔끔하지 않더라도 정확성이
먼저다."

**Ryu(개발자, 분산):** "single-truth-by-expiry 모델은 단순해서 좋다. 단 추후
'expiry 같은데 scope 다른' 케이스가 보고된다면 단순 비교로는 못 푼다. 그럴 땐
사용자에게 결정권을 넘기는 화면이 필요. 1차에선 logging으로만 noted."

**Choi(아키텍트, 경계):** "`pkg/formats`가 외부에 노출되더라도 `internal/...`은
숨겨라. 장기적으로 다른 프로젝트가 format만 가져다 쓸 수 있게."

**Lim(아키텍트, 실패):** "운영 사고의 80%는 첫 부트스트랩에서 난다. CA 발급/배포
가이드를 README에 가장 앞쪽에 두고, 그림 한 장으로 시각화해라."

**Han(UX):** "이 도구의 사용자는 곧 서버를 띄울 줄 아는 개발자/운영자다. 기술
난이도를 낮추기보다 **상태 노출**과 **다음 단계 가이드**를 풍부히. 친절한 사람
대신 친절한 stdout."

**Seo(UX):** "운영 도구라도 첫 인상은 중요하다. `--help`만 봐도 무엇을 하는지
8문장 안에 이해되어야 한다. 8문장 넘기면 기획부터 다시."

**Kim(QA):** "race detector + fsnotify는 CI에서 깨지기 쉽다. integration은 별도
태그(`-tags=integration`)로 분리해 PR 게이트에서 빼고, nightly로 돌려라.
unit/race만 PR 필수."

**Yoon(보안):** "이 시스템의 가장 큰 위험은 **편의성 압력**이다. '잠깐만 CA 키를
공용 git에 올리자'든가 '로그에 토큰 한 번만 찍어 디버그하자' 같은 운영 압력을
**도구 차원에서 막아라.** 도구가 토큰값을 자기 로그에 흘리지 못하게 하는
redactor가 그 첫 번째 방어선이다. 둘째는 README에 'CA 키 취급 규칙'을 가장
크게."

**(공통 합의):** v1의 성공 기준은 '두 노드에서 같은 토큰을 보고, 한쪽이 갱신되면
나머지에 자동 전파된다 — 그뿐'. 그 외 기능은 v0.2 이후.

---

## 22. v1 체크리스트

- [ ] 패키지 레이아웃 잡기 (5절)
- [ ] CLI 골격(cobra + viper + refutils/flagsbinder) + `serve` / `connect` /
      `ca *` / `status` / `inspect`
- [ ] `serve --also-client <list>` self-loop 에이전트 errgroup 구동
- [ ] PKI 발급 라이브러리 + SAN 멀티엔트리
- [ ] gin + gorilla/websocket + mTLS 셋업, `/ws/profile/:name`
- [ ] config 로더(server.yaml, profiles.yaml, client.yaml) + tilde/CLAUDE_CONFIG_DIR
- [ ] `pkg/formats` 인터페이스 + Registry
- [ ] `formats/claudecreds` Parse/Validate(live_check)/Compare/Redact
- [ ] watcher(디렉토리 watch + 디바운스)
- [ ] safeio(flock + atomic rename + bak unlink)
- [ ] mediator(보고 수집 → 비교 → push) + redaction 로깅
- [ ] 클라이언트 재접속(5s + jitter + cap)
- [ ] slog redactor(키 + 값 패턴)
- [ ] Makefile + golangci-lint + race CI
- [ ] integration test 시나리오(a)–(d)
- [ ] systemd / launchd 샘플 + README 부트스트랩 그림
