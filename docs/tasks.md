# pangaeactl — 구현 작업 분해(tasks.md)

본 문서는 `docs/design/specs.md`를 구현으로 떨어뜨리기 위한 **작업 지시서**이다.
각 작업은 (1) 목적, (2) 산출물(파일·함수), (3) 정상 케이스, (4) 비정상 케이스,
(5) 코너 케이스, (6) 완료 기준(DoD)을 포함한다. 6인 개발 서브에이전트에게
패키지 단위로 분배된다. 패키지 경계와 **인터페이스는 본 문서에서 동결(freeze)**
되며, 변경이 필요하면 중재자(오케스트레이터)에게 반드시 보고해야 한다.

---

## §A. 최종 패키지 레이아웃 (동결)

```
pangaeactl/
├── cmd/
│   └── pangaeactl/
│       ├── main.go
│       ├── root.go
│       ├── serve.go
│       ├── connect.go
│       ├── ca.go
│       ├── inspect.go
│       ├── status.go
│       └── version.go
├── internal/
│   ├── common/
│   │   ├── consts.go          # 상수 (버전, 디폴트 포트, 채널 크기 등)
│   │   ├── strings.go         # 로그 이벤트명/에러 메시지 등 문자열 리터럴
│   │   ├── keys.go            # viper/flag 키 이름 상수
│   │   └── errors.go          # sentinel error + 에러 wrapping helper
│   ├── logging/
│   │   ├── logger.go          # slog 셋업
│   │   ├── redact.go          # key/value 패턴 redactor
│   │   └── fields.go          # 공용 필드 키(component, profile, node_id, event, outcome)
│   ├── safeio/
│   │   ├── atomic.go          # temp+fsync+rename
│   │   ├── flock.go           # gofrs/flock 래퍼
│   │   └── zeroize.go         # 메모리 zero-out 유틸
│   ├── pki/
│   │   ├── ca.go              # CA 생성/로드
│   │   ├── issue.go           # server/client 인증서 발급
│   │   ├── tlsconf.go         # TLS config 빌더(서버/클라이언트)
│   │   └── pool.go            # CA pool 로드
│   ├── config/
│   │   ├── server.go          # server.yaml 스키마 + 로드
│   │   ├── client.go          # client.yaml 스키마 + 로드
│   │   ├── profiles.go        # profiles.yaml 스키마 + 로드 + reload
│   │   └── expand.go          # ~ / $CLAUDE_CONFIG_DIR 경로 확장
│   ├── transport/
│   │   ├── proto.go           # Envelope + 각 message payload 구조체
│   │   ├── codec.go           # JSON marshal/unmarshal + v 필드 검증
│   │   ├── wsconn.go          # gorilla/websocket 래퍼(read/write pump, pingpong)
│   │   └── errors.go          # transport 전용 에러
│   ├── watcher/
│   │   ├── watcher.go         # fsnotify 디렉토리 watch + 디바운스
│   │   ├── debounce.go        # 50ms 코어 + 200ms stable
│   │   └── events.go          # 이벤트 타입
│   ├── server/
│   │   ├── app.go             # errgroup 조립 + shutdown
│   │   ├── router.go          # gin 라우터 + /ws/profile/:name
│   │   ├── hub.go             # 연결 관리(per-profile)
│   │   ├── mediator.go        # 진본 선정 + 전파
│   │   ├── session.go         # 1개 WS 세션 상태
│   │   ├── selfclient.go      # --also-client 루프 에이전트
│   │   └── status.go          # unix socket status endpoint
│   └── client/
│       ├── agent.go           # watcher+transport+validator 조립
│       ├── conn.go            # mTLS dial + 재접속 루프
│       └── apply.go           # truth.push 수신 → safeio write → ack
└── pkg/
    └── formats/
        ├── format.go          # Format, Snapshot 인터페이스
        ├── registry.go        # Register/Get/List
        ├── result.go          # ValidationResult, Summary
        └── claudecreds/
            ├── format.go      # claude-credentials-json-format
            ├── parse.go
            ├── validate.go
            ├── compare.go
            ├── redact.go
            └── livecheck.go   # /api/oauth/profile 호출(읽기 전용 검증)
```

외부 의존:
- `github.com/spf13/cobra`, `github.com/spf13/viper`
- `github.com/dh-kam/refutils` (flagsbinder)
- `github.com/gin-gonic/gin`
- `github.com/gorilla/websocket`
- `github.com/fsnotify/fsnotify`
- `github.com/gofrs/flock`
- `gopkg.in/yaml.v3`
- `github.com/stretchr/testify`

---

## §B. 패키지 간 인터페이스 (동결 계약)

인터페이스는 **소비자측 패키지에 정의하지 않고**, 각 도메인 제공자 패키지가
노출한다. 의존 방향: `cmd → server|client → transport,watcher,safeio,pki,config,formats → common,logging`.

### B.1 `pkg/formats`
```go
package formats

type Format interface {
    Name() string
    Strategies() []string          // 지원 비교 전략 (예: "expires_at_max")
    Parse(raw []byte) (Snapshot, error)
    Validate(ctx context.Context, snap Snapshot, opts ValidateOpts) (ValidationResult, error)
    // Compare: strategy 컨텍스트 내에서 a vs b. Compare 직전에 strategy가
    // Strategies()에 있는지 호출자가 보장한다.
    Compare(strategy string, a, b Snapshot) int
    Redact(snap Snapshot) Summary
}

type Snapshot interface {
    Identity() string
    ExpiresAt() time.Time
    Raw() []byte                   // caller MUST NOT log. safeio.Zeroize 허용.
    Fingerprint() string           // sha256 hex of Raw()
}

type ValidationResult struct {
    Status    ValidationStatus     // "ok" | "expired" | "revoked" | "unreachable" | "parse_error" | "scope_warn"
    Detail    string
    CheckedAt time.Time
}

type ValidationStatus string

const (
    StatusOK          ValidationStatus = "ok"
    StatusExpired     ValidationStatus = "expired"
    StatusRevoked     ValidationStatus = "revoked"
    StatusUnreachable ValidationStatus = "unreachable"
    StatusParseError  ValidationStatus = "parse_error"
    StatusScopeWarn   ValidationStatus = "scope_warn"
)

type ValidateOpts struct {
    LiveCheck  bool
    Timeout    time.Duration
    HTTPClient *http.Client         // nil이면 default
    Clock      func() time.Time     // nil이면 time.Now
}

type Summary struct {
    Identity       string           // short, redacted id
    ExpiresAt      time.Time
    Scopes         []string
    Subscription   string
    FingerprintShort string
    TokenTail4     string           // "XXXX"
    Extra          map[string]string
}

// Registry
func Register(f Format)
func Get(name string) (Format, bool)
func List() []string                // 정렬된 복사본
```

### B.2 `internal/transport`
```go
package transport

type Kind string
const (
    KindHello         Kind = "hello"
    KindWelcome       Kind = "welcome"
    KindSnapshotReport Kind = "snapshot.report"
    KindSnapshotAbsent Kind = "snapshot.absent"
    KindTruthPush     Kind = "truth.push"
    KindTruthAck      Kind = "truth.ack"
    KindError         Kind = "error"
)

type Envelope struct {
    Type    Kind            `json:"type"`
    V       int             `json:"v"`
    ID      string          `json:"id"`
    TS      time.Time       `json:"ts"`
    Payload json.RawMessage `json:"payload"`
}

// Typed payloads — 필드는 설계 §7.3/7.4와 일치
type Hello          struct { NodeID, AgentVersion, OS string; Capabilities []string }
type Welcome        struct { ServerVersion string; KnownTruth *TruthMeta }
type SnapshotReport struct { Profile, Path, Format string; Fingerprint string; Summary formats.Summary; LiveCheck LiveCheckMeta; RawSize int; RawB64 string /* 1차: 동봉 */ }
type SnapshotAbsent struct { Profile, Path string }
type TruthPush      struct { Profile, Format, Fingerprint, RawB64, TargetPath, IssuedAt string; Summary formats.Summary }
type TruthAck       struct { Profile, Fingerprint string; OK bool; Reason string }
type ErrorPayload   struct { Code, Message string }
type LiveCheckMeta  struct { Performed bool; Result formats.ValidationStatus; CheckedAt time.Time }
type TruthMeta      struct { Fingerprint string; IssuedAt time.Time; Summary formats.Summary }

// Conn wraps one gorilla/websocket.Conn with separate read/write goroutines.
type Conn interface {
    Send(ctx context.Context, env Envelope) error   // thread-safe; internally serialized via writer goroutine
    Recv() <-chan Envelope                           // closed on disconnect
    Close(code int, reason string) error
    Err() error                                      // last error (nil if clean close)
    RemoteCN() string                                // only meaningful for server-side (from peer cert)
}

// Server-side upgrader + dialer helpers:
func Upgrade(w http.ResponseWriter, r *http.Request) (Conn, error)  // assumes mTLS already verified upstream
func Dial(ctx context.Context, url string, tlsCfg *tls.Config, headers http.Header) (Conn, error)
```

### B.3 `internal/watcher`
```go
package watcher

type Event struct {
    Path       string
    Exists     bool       // 파일 삭제면 false
    ModifiedAt time.Time
    Size       int64
}

type Watcher interface {
    Start(ctx context.Context) error
    Events() <-chan Event
    Close() error
}

// paths는 후보 경로. 존재하지 않는 경로는 absent 이벤트 1회 방출 후 디렉토리
// watch로 등록(뒤에 생성될 수 있으므로). 디렉토리 watch + 파일 필터.
func New(paths []string, opts Options) (Watcher, error)

type Options struct {
    DebounceCore   time.Duration // default 50ms
    StableWindow   time.Duration // default 200ms
    MaxQueue       int           // default 64
    Clock          func() time.Time
}
```

### B.4 `internal/pki`
```go
package pki

type CA struct { Cert *x509.Certificate; Key any /* ECDSA private */ }
func LoadCA(certPath, keyPath string) (*CA, error)
func NewCA(outDir, commonName string, notAfter time.Time) (*CA, error)

type SAN struct { IPs []net.IP; DNS []string }

func IssueServer(ca *CA, outDir, commonName string, san SAN, notAfter time.Time) error
func IssueClient(ca *CA, outDir, commonName string, notAfter time.Time) error

// TLS config builders
func ServerTLSConfig(caCertPath, serverCertPath, serverKeyPath string) (*tls.Config, error)
func ClientTLSConfig(caCertPath, clientCertPath, clientKeyPath, serverName string) (*tls.Config, error)

// Peer CN 추출 (session 수립 후 request TLS 상태에서)
func PeerCN(state *tls.ConnectionState) (string, error)
```

### B.5 `internal/config`
```go
package config

type ServerConfig struct { /* §6.1 그대로 */ }
type ClientConfig struct { /* §6.3 그대로 */ }
type ProfilesFile struct { Profiles []Profile }
type Profile struct {
    Name           string
    Format         string
    Dir            string
    WatchFiles     []string
    AllowedClients []string
    Validate       ValidateSpec
    Propagate      PropagateSpec
}
type ValidateSpec   struct { Strategy string; LiveCheck bool; LiveCheckTimeout time.Duration }
type PropagateSpec  struct { Mode string; Cooldown time.Duration } // mode: "to_stale_only" | "to_all"

func LoadServer(path string) (*ServerConfig, error)
func LoadClient(path string) (*ClientConfig, error)
func LoadProfiles(path string) (*ProfilesFile, error)

// ProfileStore: 서버 런타임이 사용. SIGHUP 시 Reload 호출.
type ProfileStore interface {
    Get(name string) (Profile, bool)
    List() []Profile
    Reload(path string) error
    Subscribe() <-chan []Profile   // 변경 시 최신 목록 방출
}
func NewProfileStore(initial *ProfilesFile) ProfileStore

// 경로 확장 util (tilde, $CLAUDE_CONFIG_DIR)
func ExpandPath(p string) (string, error)
```

### B.6 `internal/safeio`
```go
package safeio

// AtomicWrite: temp file in same dir + chmod + fsync + rename + dir fsync.
func AtomicWrite(dst string, data []byte, perm os.FileMode) error

// LockFile: flock on <dst>.lock with timeout. returns unlock func.
func LockFile(dst string, timeout time.Duration) (unlock func() error, err error)

// Zeroize: best-effort; wipes []byte in-place. caller responsible for not keeping copies.
func Zeroize(b []byte)

// ReadWithBackup: read dst into memory, and simultaneously write a .bak.<ts> copy
// under same dir with 0600. Returns backup path for later unlink/rollback.
func ReadWithBackup(dst string) (data []byte, backupPath string, err error)
```

### B.7 `internal/logging`
```go
package logging

type Options struct {
    Level   string   // debug|info|warn|error
    Format  string   // json|text
    Output  io.Writer // nil => os.Stderr
}
func New(o Options) *slog.Logger            // redactor 장착된 logger 반환

// Fields (string const) in fields.go — §14 요구 일치
const (
    FieldComponent = "component"
    FieldProfile   = "profile"
    FieldNodeID    = "node_id"
    FieldEvent     = "event"
    FieldOutcome   = "outcome"
    // ...
)

// Event names (consts)
const (
    EvtSnapshotParsed    = "snapshot.parsed"
    EvtSnapshotValidated = "snapshot.validated"
    EvtMediatorCandidates= "mediator.candidates"
    EvtTruthSelected     = "mediator.truth_selected"
    EvtTruthPushed       = "truth.pushed"
    EvtTruthApplied      = "truth.applied"
    // ...
)
```

### B.8 `internal/server`, `internal/client`
상세는 §E 각 섹션 참조. 외부로 드러나는 API는
- `server.Run(ctx, cfg *config.ServerConfig, ps config.ProfileStore, fmts formats.Registry?, log *slog.Logger) error`
- `client.Run(ctx, cfg *config.ClientConfig, log *slog.Logger) error`

### B.9 `internal/common`
- `consts.go`: `DefaultPort=8443`, `DefaultWSPath="/ws/profile/"`, `EnvelopeV=1`,
  `ChannelBuf=64`, `ReconnectInitial=5*time.Second`, `ReconnectMax=60*time.Second`,
  `ReconnectJitter=time.Second`, `PingInterval=30*time.Second`, `WriteTimeout=10*time.Second`.
- `strings.go`: 사람-가시 에러 메시지, CLI 설명 문구.
- `keys.go`: viper key들 — `"server.listen"`, `"log.level"`, … (중복 방지).
- `errors.go`: sentinel errors + `Wrapf(err, code, "...", args...)`.

---

## §C. 공용 유틸 / 상수 파일 정책

- **리터럴 금지 원칙:** 두 번 이상 쓰이는 문자열은 `internal/common/strings.go`
  또는 각 패키지의 `consts.go`에 이름을 붙여 상수화.
- **에러 센티넬:** `internal/common/errors.go`에 `ErrProfileNotFound`,
  `ErrFormatNotRegistered`, `ErrInvalidMessage`, `ErrTLSHandshake`,
  `ErrParseFailed`, `ErrExpired`, `ErrLiveCheckUnreachable`, `ErrApplyFailed`,
  `ErrLockTimeout`, `ErrConfigInvalid`, `ErrCNMismatch` 등을 export.
- **로거 필드 키:** `internal/logging/fields.go`에서만 선언하고 전 패키지가 import.
- **이벤트 이름:** `logging.EvtXxx`로 중앙화 (§14 매트릭스 전부 커버).
- **환경변수/viper 키:** `internal/common/keys.go`에서 선언(예: `KeyServerListen = "server.listen"`).
- **디폴트 값:** `internal/common/consts.go`에서만.

---

## §D. 구현 착수 전 "기반 작업"(Phase 0)

Phase 0가 완료되지 않으면 패키지별 병렬 작업이 충돌한다. 반드시 선행.

1. **Repo bootstrap**
   - `git init`, `go mod init github.com/0xc0de1ab/pangaea`
   - `.gitignore`, `.golangci.yml`, `Makefile` 스텁
   - `go.mod` 의존 추가(위 목록)
2. **`internal/common`**: 상수·문자열·에러 센티넬 전부 선언 (빈 값이라도 이름 확정)
3. **`internal/logging`**: Options + New + 필드/이벤트 상수 + redactor
4. **`internal/safeio`**: AtomicWrite/LockFile/Zeroize/ReadWithBackup
5. **`pkg/formats`**: 인터페이스·타입·registry (구현체는 Phase 1)
6. **`internal/transport/proto.go` + `codec.go`**: Envelope/Kind/Payload 타입
7. **`internal/config`**: 스키마 + 로더(profile reload 포함) + 경로 확장
8. **`internal/pki`**: CA/issue/tlsconf/pool
9. **경계 테스트**: 각 인터페이스 mock 기반 유닛 테스트 최소 1개

Phase 0 완료 판정 기준: `go build ./...`, `go vet ./...`, `go test ./internal/common ./internal/logging ./internal/safeio ./pkg/formats ./internal/transport ./internal/config ./internal/pki ./internal/watcher` 통과.

---

## §E. 패키지별 상세 작업 (Phase 1)

각 작업의 **정상/비정상/코너** 케이스를 모두 구현·테스트해야 한다.

### E.1 `internal/common` — 상수·에러 센티넬
- 정상: 위 §B.9, §C 항목 전부 상수화.
- 비정상: N/A (상수 전용)
- 코너: 두 상수가 같은 값을 갖는 중복 방지 — `go test`에서 reflect로 중복 검사
  유닛테스트 1개.

### E.2 `internal/logging`
- 정상:
  - `New(Options{Level:"info", Format:"json"})` → slog.Logger with JSON handler,
    redactor 장착
  - 필드 키 상수 사용 시 바른 JSON 키로 직렬화
- 비정상:
  - 잘못된 level 문자열 → fallback "info" + stderr 경고 1회
  - nil Output → os.Stderr
- 코너:
  - **값 기반 패턴 매칭**: `sk-ant-oat01-...`, `sk-ant-ort01-...`, `Bearer ...`,
    `eyJ...`(JWT-like)를 값에서 찾으면 `<redacted>`로 치환.
  - 중첩 map/struct 내부도 재귀 redact.
  - 매우 긴 payload(>64KB)는 `<redacted:oversize>`.
- 테스트:
  - 로그 라인 스냅샷 + JSON 파싱 후 키 존재성 검증
  - `sk-ant-oat01-ABCDEFG`를 포함한 임의 구조 → 결과에 해당 문자열이 없음을 단언
- DoD: `go test ./internal/logging -run . -v`.

### E.3 `internal/safeio`
- 정상:
  - `AtomicWrite("/tmp/x/f", data, 0600)` → 같은 디렉토리에 `.tmp.<rand>` 생성
    후 rename, 디렉토리 fsync
  - `LockFile(dst, 500ms)` → `.lock` 획득 성공 시 unlock 함수 반환
- 비정상:
  - 대상 디렉토리 없음 → `ErrConfigInvalid` 계열 wrap 에러 (디렉토리 자동 생성
    X — 명시적으로 호출자가 책임)
  - `LockFile` 타임아웃 → `ErrLockTimeout`
  - rename 실패(readonly FS 등) → temp 파일 삭제 후 에러 wrap
- 코너:
  - 같은 이름 `.tmp.*`가 남아있는 경우 무시하고 새로 생성
  - `AtomicWrite` 도중 프로세스 crash → 재시작 시 `.tmp.*` 잔존 가능. 별도 GC
    루틴은 두지 않지만, 3일 이상 된 `.tmp.*`는 스타트업 시 unlink(safeio 레벨
    아닌 상위 호출자가 선택). 1차에선 미구현, DoD에서 제외하고 README note.
  - `Zeroize(nil)` → no-op
  - `Zeroize` 후 caller가 여전히 sliceheader를 유지한 케이스는 문서로 경고만.
- 테스트: 파일 시스템 fault injection 추상화는 두지 않고, 기본 OS FS에서 통합
  테스트. race `-race` 통과.

### E.4 `internal/pki`
- 정상:
  - `NewCA` → ECDSA P-256, notAfter=+10y, BasicConstraints CA=true pathlen=0
  - `IssueServer(ca, outDir, "server-1", SAN{IPs, DNS}, +1y)` → `server.crt`,
    `server.key` 두 파일. SAN에 IP/DNS 다중 포함 확인
  - `IssueClient` → clientAuth ExtKeyUsage
  - `ServerTLSConfig` → `ClientAuth=RequireAndVerifyClientCert`, TLS1.3 min
  - `ClientTLSConfig` → `ServerName` 세팅
- 비정상:
  - CA 파일 손상 → `ErrConfigInvalid`
  - `IssueServer` SAN 비어있음 → 거절(`ErrConfigInvalid`)
  - notAfter가 과거 → 거절
- 코너:
  - SAN에 IPv6 포함 → `net.ParseIP` 기반 정확 구분
  - DNS에 와일드카드 `*.local` 지원 (x509가 허용하는 수준에서)
  - 같은 outDir 재발급 시 기존 파일 overwrite 하지 않고 `.bak.<ts>` 이동
- 테스트: 발급한 서버/클라이언트 쌍으로 loopback TLS handshake 성공까지 e2e.

### E.5 `internal/config`
- 정상:
  - `LoadServer("server.yaml")` → `ServerConfig` 구조체
  - `LoadProfiles("profiles.yaml")` → `*ProfilesFile`
  - `ProfileStore.Reload(path)` → 새 파일 반영, Subscribe 채널에 최신 목록 방출
  - `ExpandPath("~/.claude/.credentials.json")` → `/home/<user>/.claude/.credentials.json`
  - `ExpandPath`는 `$CLAUDE_CONFIG_DIR`가 설정되었고 경로가 `~/.claude`로
    시작하면 `$CLAUDE_CONFIG_DIR` 기준으로 치환
- 비정상:
  - 파일 없음/파싱 실패 → `ErrConfigInvalid` wrap
  - profile 이름 중복 → 거절
  - `format`이 registry에 없음 → 거절 (단, Phase 0에서는 registry가 비어있을
    수 있으므로 LoadProfiles는 format 존재 검증을 하지 않고, 서버가 start-up에
    별도로 검증하는 것으로 역할 분리)
  - `allowed_clients` 중 빈 문자열 → 거절
  - `dir` 비어있음 → 거절
- 코너:
  - tilde 미해석 남아있는 경로 문자열이 `profiles.yaml`에 그대로 → 사용 시점
    (watcher 시작 직전)에 lazy expand
  - `validate.strategy` 문자열이 빈값 → format 기본값 (`format.Strategies()[0]`)
- 테스트: golden yaml 다수 + invalid 케이스 다수.

### E.6 `internal/transport`
- 정상:
  - Envelope 직렬화/역직렬화 round-trip
  - `Dial` → 서버 `Upgrade` → hello/welcome round-trip
  - 30초 ping/pong, writer goroutine + writeTimeout 10초
- 비정상:
  - 알 수 없는 `Kind` → `ErrInvalidMessage` (close code 1002)
  - `v` 미스매치 → `ErrInvalidMessage`
  - payload JSON 파싱 실패 → `ErrInvalidMessage`
  - 상대가 ping pong 응답 X → read timeout, Close 1001
  - 쓰기 중 에러 → writer goroutine 종료, `Conn.Err()`에 보관, Recv 채널 close
- 코너:
  - Recv 채널을 소비자가 안 빼가면 backpressure → N(=ChannelBuf) 초과 시 가장
    오래된 이벤트 drop + warn 로그 ("transport.recv.overflow")
  - 동시 Send(thread-safe 보장) — 내부 writer 1개 goroutine
  - gorilla/websocket close frame 코드 매핑 테이블
- 테스트: httptest + TLS + 두 Conn loopback.

### E.7 `internal/watcher`
- 정상:
  - 존재하는 파일 1개: 최초 시점 1회 Event{Exists=true} 후 fsnotify 이벤트로 변경 감지
  - 부재 파일 1개: 최초 Event{Exists=false} 후 디렉토리 watch, 나중 생성 시 Event{Exists=true}
  - temp+rename 패턴 변경 감지(대표: Claude CLI가 이렇게 씀) — 디렉토리 watch로 잡힘
- 비정상:
  - fsnotify 초기화 실패 → 에러 반환
  - 디렉토리 자체가 없음 → 상위 디렉토리로 올라가면서 존재하는 가장 가까운 조상 watch,
    중간 디렉토리 생성 이벤트 수신 시 아래로 내려가며 재등록 (1차: 단순화 — 상위 없으면 에러)
- 코너:
  - 심볼릭 링크: 링크 자체 경로를 watch하되 evaluate한 real path도 watch. 둘 중 하나라도
    이벤트면 re-read
  - 빠른 연속 변경 → 디바운스 50ms 창 내 병합, stable window 200ms 동안 size 동일해야 방출
  - 파일이 삭제→재생성(rename) → Exists=false then Exists=true 각각 방출 (상위에서
    판단)
  - WSL2에서 일부 이벤트 손실 — stable window로 방어. stable 내에 stat 실패 → 재시도
- 테스트: tmpdir + 수동 write/rename으로 시나리오 구성.

### E.8 `pkg/formats` + `pkg/formats/claudecreds`
- 정상:
  - `Parse` → Snapshot: accessToken/refreshToken 포함 구조 성공
  - `Validate(live=false)` → expiresAt과 clock 비교해 `expired|ok`
  - `Validate(live=true)` → `/api/oauth/profile`: 200→ok, 401→expired, 403→scope_warn, timeout→unreachable
  - `Compare("expires_at_max", a, b)` → expiresAt 큰 쪽 +1
  - `Redact(snap)` → tail4, fingerprint_short, scopes 등 노출 정상
- 비정상:
  - JSON 구조 비규격/필수 키 없음 → `Parse` → `ErrParseFailed`
  - accessToken 빈 문자열 → `ErrParseFailed`
  - `Compare` strategy가 Strategies()에 없음 → panic (호출자 책임, 상수 사용 강제)
  - live_check HTTP 5xx → `unreachable` (401과 구분)
- 코너:
  - `expiresAt`가 0 → expired 취급
  - 403 응답은 "토큰은 유효하나 scope 부족" — 본 시스템 관점에서 `scope_warn`로
    ok 동치로 취급하되 로그 warn 남김
  - 네트워크 OFF(리졸브 실패) → `unreachable`
  - JSON에 `accessToken` 대신 오래된 키 `access_token` 오는 경우 → 호환 허용
  - raw 바이트에 BOM 포함 → 허용 (unmarshal 가능)
  - refreshToken만 있고 accessToken 없음 → `ErrParseFailed`
- 테스트: 골든 픽스처(정상/만료/malformed/oldkey/BOM/누락), httptest로 live_check 케이스 전부.

### E.9 `internal/server`
하위 구성:
- **router.go**: gin + `/ws/profile/:name` Handler
- **hub.go**: per-profile 연결 세트 관리, register/unregister
- **mediator.go**: 보고 수집 → Format.Compare → push 결정
- **session.go**: 1 WS session. read loop에서 envelope 디코드 → 핸들러 분기
- **selfclient.go**: `--also-client` 지정 profile에 대해 localhost dial-in
- **status.go**: unix socket status endpoint (read-only)
- 정상:
  - hello 수신 → CN ACL 검사 → welcome 송신 (현재 truth 메타 포함)
  - snapshot.report 수신 → mediator state 갱신 → 필요 시 truth.push
  - truth.ack 수신 → LastPushed 기록
  - SIGHUP → profiles reload → hub에 변경 반영(변경된 profile 연결 재평가)
  - `--also-client p1,p2` → p1/p2용 client agent 2개 goroutine 구동(errgroup)
- 비정상:
  - profile 이름이 없음 → WS upgrade 전 404
  - CN이 ACL에 없음 → 1008(Policy Violation) close
  - 동일 CN 중복 접속 → 기존 세션을 정상 close 후 새 세션 수락 (Yoon 요구)
  - 알 수 없는 envelope → session close with 1002
- 코너:
  - truth 확정 직전 진본 source 노드 disconnect → 다음 후보로 재선정
  - 동일 fingerprint 반복 보고 → push 억제 (LastPushed + cooldown)
  - unreachable만 있을 때 → 여전히 최선 후보 선정, degraded 로그
  - mediator 내부 상태 map 동시성 — RWMutex 또는 single goroutine actor 패턴 중 후자 채택
  - selfclient가 서버보다 먼저 시작해 dial 실패 → listener 준비 완료 시그널 대기(errgroup에 "listener ready" chan 1)
- 테스트: httptest TLS server + 2 가짜 client conn + scenario (a)(b)(c)(d)(§17.2).

### E.10 `internal/client`
- 정상:
  - `connect` 시작 → mTLS dial → hello → 워처 이벤트마다 snapshot.report
  - 최초 스캔 1회 송신 후 이벤트 기반 전송
  - truth.push 수신 → safeio.LockFile → AtomicWrite → 재read 검증 → truth.ack(ok=true)
- 비정상:
  - dial 실패 → 5s + jitter backoff, max 60s
  - peer cert mTLS 실패 → 고정 backoff 60s (복구 어려운 구성오류이므로 폭주 방지)
  - apply 시 디스크 full → backup 복원 → truth.ack(ok=false, reason)
- 코너:
  - 연결 끊긴 동안 워처 이벤트 누적 → path별 최신 1건만 유지
  - 연결 복귀 시 누적 보고를 hello 이후 순차 송신
  - truth.push의 `target_path`가 내 로컬 credentials 경로와 달라도 로컬 경로에
    적용하여 수렴 유지
  - 적용 후 fingerprint 재계산 불일치 → ok=false + backup 복원
  - 동일 fingerprint 반복 push 수신 → 이미 일치면 no-op, ok=true 반환
  - Claude CLI가 동시에 파일을 덮어써 flock 실패 → 3회 재시도 후 ok=false
- 테스트: in-process server + client, scenario (a)(b)(c).

### E.11 `cmd/pangaeactl`
- cobra root + viper 초기화(config 경로/env prefix `CCS_`)
- flagsbinder로 각 subcmd 옵션 struct 바인딩 (§15.1, §15.2 예시)
- 서브커맨드:
  - `serve`
  - `connect`
  - `ca init|issue-server|issue-client`
  - `inspect <path>`
  - `status`
  - `version`
- 정상:
  - `serve -c server.yaml --also-client p1,p2`
  - `connect -c client.yaml`
  - `ca init --out ./pki`
  - `ca issue-server --ca ./pki --san IP:1.2.3.4,DNS:hub.local --out ./pki/server`
  - `inspect ~/.claude/.credentials.json --format claude-credentials-json-format`
- 비정상:
  - 필수 flag 누락 → usage 출력 + exit 2
  - 설정 파일 파싱 실패 → "친절한 에러 + 다음 단계" stderr 출력 + exit 1
  - profiles.yaml 부재 시 `serve` → 빈 샘플 출력 후 exit 1
- 코너:
  - `--also-client` 값에 unknown profile → 기동 거절
  - SIGINT/SIGTERM → errgroup cancel → graceful shutdown(30s)
  - SIGHUP → reload (serve만)
- 테스트: cobra 커맨드 단위 테스트 + integration(`serve`+`connect` goroutine 쌍).

---

## §F. 교차 관심사 테스트

- `docker-compose.yml` (1 server + 2 clients + shared ca volume) — E2E
- GitHub Actions 매트릭스: linux-amd64, linux-arm64, darwin-arm64
- `go test -race` PR 게이트; integration은 `-tags=integration` 별도 job.

---

## §G. 6인 개발자 서브에이전트 작업 분배

| Dev | 담당 패키지 | Phase 0 책임 | Phase 1 책임 |
|---|---|---|---|
| D1 | `internal/common`, `internal/logging`, `internal/safeio` | §D.2–4 | E.1–3 |
| D2 | `internal/pki`, `internal/config` | §D.7–8 | E.4–5 |
| D3 | `internal/transport`, `internal/watcher` | §D.6 | E.6–7 |
| D4 | `pkg/formats`, `pkg/formats/claudecreds` | §D.5 | E.8 |
| D5 | `internal/server` | — | E.9 |
| D6 | `internal/client`, `cmd/pangaeactl` | — (Phase 0 bootstrap repo §D.1 포함) | E.10–11 |

### G.1 규칙
1. 모든 Dev는 **§B의 인터페이스를 동결 계약**으로 삼는다. 변경 필요 시 즉시
   오케스트레이터에게 "인터페이스 조정 요청"으로 보고.
2. Phase 0이 끝나기 전에는 타 패키지에 의존하는 함수 본체 구현 금지(시그니처·
   타입 선언까지만 허용). Phase 0이 끝나면 일괄 `go build ./...` 초록빛.
3. 모든 Dev는 `go test -race ./<본인패키지>` 초록 후 제출.
4. 코드는 `gofmt + goimports`, `golangci-lint run` 통과.
5. 로깅은 `logging.FieldXxx` / `logging.EvtXxx` 상수만 사용. 리터럴 금지.
6. 에러는 `internal/common/errors.go`의 sentinel을 `errors.Is/As`로 식별 가능한
   형태로 반환.
7. 각 Dev는 자기 패키지 README(`doc.go` godoc comment)에 요약 + 의존성 도식을
   남긴다.
8. **산출물 제출 시 체크리스트**:
   - [ ] 본 tasks.md의 해당 섹션 정상/비정상/코너 케이스 전부 구현/테스트
   - [ ] `go vet`, `go test -race` 통과
   - [ ] 인터페이스 변경 유무 명시
   - [ ] 미해결/건의사항 명시
   - [ ] 다음 의존자(패키지)가 알아야 할 주의사항

### G.2 오케스트레이터 루프
1. Phase 0 착수: D1–D4에게 기반 작업 분배 (D5/D6는 Phase 0 bootstrap 후 대기)
2. 6인 제출물 수집 → 인터페이스 드리프트 / 계약 위반 검사 → 리뷰 & 피드백
3. 필요 시 재작업 지시. 인터페이스 조정 필요하면 관련 Dev 전체에 변경 전파.
4. Phase 1 착수: 각 Dev에게 E.* 작업 분배
5. 제출 → 리뷰 → 피드백 → 재작업(통합 테스트 실패 시 관련 Dev로 재분배)
6. 통합 E2E 통과 → 최종 보고

---

## §H. v1 성공 기준 (재확인)
- 두 노드에서 같은 `.credentials.json`을 본다.
- 한쪽이 (외부에서) 갱신되어 파일이 바뀌면 나머지 노드로 자동 전파.
- 로그에는 §14 매트릭스의 모든 이벤트가 redacted 상태로 남는다.
- `serve --also-client` 및 단독 `connect`가 동일한 동기화 결과를 만든다.
