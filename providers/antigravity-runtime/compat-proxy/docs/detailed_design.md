# Antigravity Compat Proxy 상세 설계서 (Detailed Design)

**작성일**: 2026-05-01
**작성자**: Virtual Task Force `team-setup` (아키텍트, 개발, QA, UI/UX, 보안, 성능, AI 전문가 연합) 및 Gemini CLI 통합

## 1. Team-Setup 역할별 설계 핵심 요약

- **S/W 아키텍트 (20년차)**: 3-Process 모델(Proxy, ls_core, Headless Server)의 생명주기 완벽 분리. ConnectRPC 오버헤드를 최소화하기 위한 커넥션 풀링 및 gRPC Multiplexing 설계.
- **S/W 개발자 (20년차)**: Go의 강력한 Concurrency를 활용한 스트리밍(SSE) 완벽 구현. OpenAI/Anthropic/Gemini SDK의 공식 구조체를 `LanguageServerService` Protobuf에 매핑하는 Mapper 패턴 도입.
- **S/W 테스트 엔지니어 (20년차)**: 프로토콜 변환 검증을 위한 Golden Master Testing 기법 도입. 각 API 모델별 요청/응답 쌍의 JSON 덤프를 생성하여 회귀 테스트 자동화.
- **UX/UI 디자이너 (20년차)**: Headless 환경이므로 프록시 상태와 로그를 모니터링할 수 있는 심플한 Web Dashboard (또는 CLI TUI) 상태 API 제공 제안. (예: `/health`, `/metrics`)
- **보안 전문가 (20년차)**: `state.vscdb`의 OAuth 토큰이 메모리에 상주할 때 Secure Memory 기법 적용. 외부 노출 API에 대한 API Key 기반 인증(Authentication) 미들웨어 강제.
- **성능/메모리 전문가 (20년차)**: HTTP/2 스트리밍 파싱 시 GC 부하를 줄이기 위한 `sync.Pool` 객체 풀링(Byte Buffer) 설계.
- **AI 에이전트 개발자 (3년차)**: System Prompt, Tool Calling, MCP(Model Context Protocol) 등 최신 LLM 트렌드의 복잡한 프롬프트 구조를 Antigravity의 단일 `PromptElement` 배열로 손실 없이 평탄화(Flattening)하는 로직 집중.

---

## 2. 모듈식 아키텍처 및 인터페이스 설계 (Interface Segregation & Modularity)

모든 모듈은 독립적으로 빌드 및 테스트가 가능해야 하며, 상호 의존성은 구체적인 구현체가 아닌 **역할별로 세분화된 인터페이스(Granular Interfaces)**를 통해 관리됩니다. 외부로 노출되는 기능은 이러한 세분화된 인터페이스들의 **Composite(합성)** 형태로 구성되어 캡슐화를 극대화합니다.

### 2.1. 모듈간 의존성 원칙
- **독립성**: 각 패키지(`scraper`, `bridge`, `api`, `proc`)는 자체적인 `go.mod`(선택사항) 또는 완벽한 로직 분리를 유지합니다.
- **의존성 주입(DI)**: 모듈 생성 시 필요한 기능을 인터페이스 형태로 주입받습니다.
- **인터페이스 분리 원칙(ISP)**: 하나의 거대한 인터페이스 대신, `TokenReader`, `TokenWatcher`, `ModelInvoker` 등으로 쪼개어 필요한 기능만 노출합니다.

### 2.2. 핵심 인터페이스 및 합성(Composite) 구조

#### A. Auth & Storage (Scraper Module)
```go
package scraper

// 세분화된 인터페이스
type TokenReader interface {
    GetLatestToken() (string, error)
}

type TokenWatcher interface {
    WatchTokenChanges(ctx context.Context) (<-chan string, error)
}

// Composite 인터페이스: 외부(Proxy)에 노출할 서비스 규격
type AuthProvider interface {
    TokenReader
    TokenWatcher
}
```

#### B. AI Engine Bridge (Bridge Module)
```go
package bridge

// 세분화된 인터페이스
type ModelInvoker interface {
    Invoke(req *antigravity.Request) (*antigravity.Response, error)
}

type StreamInvoker interface {
    InvokeStream(req *antigravity.Request) (<-chan *antigravity.StreamChunk, error)
}

// Composite 인터페이스: 추론 엔진의 기능 캡슐화
type EngineBridge interface {
    ModelInvoker
    StreamInvoker
}
```

#### C. Process Management (Proc Module)
```go
package proc

type ProcessController interface {
    Start() error
    Stop() error
    Restart() error
}

type HealthChecker interface {
    IsHealthy() bool
}

// Composite 인터페이스
type LifecycleManager interface {
    ProcessController
    HealthChecker
}
```

---

## 3. API Endpoints (외부 & 내부)
... (기존 내용 유지) ...

---

## 3. 핵심 자료구조 (Data Structures in Go)

### 3.1. 표준 API 요청 구조체 (예: OpenAI)
```go
package proxy

// OpenAI Chat Completion Request
type OpenAIChatRequest struct {
    Model       string            `json:"model"`
    Messages    []OpenAIMessage   `json:"messages"`
    Tools       []OpenAITool      `json:"tools,omitempty"`
    System      string            `json:"system,omitempty"`
    Stream      bool              `json:"stream,omitempty"`
    Temperature float32           `json:"temperature,omitempty"`
}

type OpenAIMessage struct {
    Role    string `json:"role"`    // "system", "user", "assistant", "tool"
    Content string `json:"content"`
}
```

### 3.2. 내부 ConnectRPC 구조체 (Antigravity/Codeium Protobuf 매핑)
```go
package antigravity

// ls_core가 기대하는 최상위 요청 구조체
type GetModelResponseRequest struct {
    Metadata       *RequestMetadata `json:"metadata"`
    Prompt         *Prompt          `json:"prompt"`
    Model          string           `json:"model"`
    Stream         bool             `json:"stream"`
    ToolDefintions []ToolDefinition `json:"tool_definitions,omitempty"` // MCP 및 Tool
}

type Prompt struct {
    Elements []PromptElement `json:"elements"`
}

type PromptElement struct {
    Kind    string `json:"kind"`    // "TEXT", "SYSTEM", "TOOL_RESULT"
    Content string `json:"content"`
}

type RequestMetadata struct {
    WorkspaceId string `json:"workspace_id"`
    AuthToken   string `json:"auth_token"` // state.vscdb에서 추출한 OAuth 토큰
}
```

---

## 4. 프로토콜 변환 로직 (Transcoder)

가장 중요한 부분인 System Prompt, Tool Calling, MCP 변환 로직입니다. (AI 에이전트 개발자 제안 반영)

### 4.1. Message Flattening (역할 통합)
OpenAI나 Anthropic은 `Role` (system, user, assistant) 구분이 명확하지만, `ls_core`의 `PromptElement`는 순차적인 배열입니다.

1. **System Prompt**: OpenAI의 `system` 메시지 또는 Anthropic의 `system` 파라미터는 `PromptElement{Kind: "SYSTEM", Content: ...}`로 변환하여 배열의 맨 앞에 삽입합니다.
2. **User/Assistant**: 각각 `TEXT` kind로 삽입하되, 컨텍스트 유지를 위해 내부 포맷(예: `<|user|>...`, `<|assistant|>...`) 래핑이 필요할 수 있습니다.

### 4.2. Tool Calling 및 MCP (Model Context Protocol) 변환
- 외부 API로 들어온 Tool 정의(OpenAI의 `functions`, Anthropic의 `tools`)는 `ls_core`의 `ToolDefinition` 배열로 변환합니다.
- **MCP 통합**: 클라이언트가 MCP 리소스(예: `view_file`, `grep_search`)를 요청할 때, 이를 Antigravity 엔진이 인식하는 내장 툴(`find_by_name`, `read_file` 등)의 구조로 맵핑하거나, 프록시 단에서 직접 실행 후 `TOOL_RESULT`를 주입하여 `ls_core`를 속이는(Mocking) 방법 두 가지를 지원합니다.
    - *권장*: `ls_core`의 Tool 기능이 완벽하지 않다면, Proxy가 직접 MCP 서버 릴레이 역할을 수행하고, `ls_core`에게는 "이미 Tool이 실행된 결과"(`PromptElement{Kind: "TOOL_RESULT"}`)를 주입하여 추론만 맡깁니다.

---

## 5. 검증 및 테스트 방법 (Verification Strategy)

S/W 테스트 엔지니어가 확립한 검증 방법입니다.

### 5.1. Golden File Testing (데이터 구조 검증)
다양한 형태의 OpenAI/Anthropic/Gemini 요청 JSON을 준비하고, 이를 변환기(`Transcoder`)에 통과시켰을 때 기대되는 `GetModelResponseRequest` JSON 결과를 비교합니다.
```go
// transcoder_test.go
func TestOpenAIToAntigravity(t *testing.T) {
    tests := []struct {
        name       string
        inputFile  string // e.g., "testdata/openai_tool_request.json"
        goldenFile string // e.g., "testdata/antigravity_expected.json"
    }{ ... }
    // ...
}
```

### 5.2. e2e Mock Server 테스트 (통신 검증)
- `ls_core`를 대체하는 Mock ConnectRPC 서버를 Go로 띄웁니다.
- 클라이언트(curl 등)로 표준 OpenAI 요청을 쏘면, Proxy가 Mock 서버에 올바른 헤더(CSRF 토큰, OAuth 토큰)와 Body를 전송하는지 패킷 캡처 레벨에서 검증합니다.

### 5.3. 성능 프로파일링 (메모리 검증)
- 성능/메모리 전문가의 가이드에 따라 `net/http/pprof`를 Proxy에 연동합니다.
- 로드 테스트 도구(`vegeta`, `k6`)로 대량의 스트리밍 응답을 모의 발생시키고, `sync.Pool` 적용 전후의 GC 횟수와 Heap 메모리 사용량을 측정하여 누수(Leak)를 방지합니다.

---
