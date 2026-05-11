# Antigravity Compatibility Proxy (Go Implementation Plan)

이 프로젝트는 Antigravity Language Server (`ls_core`)를 Headless 환경(Docker)에서 실행하고, 표준 AI API(OpenAI, Anthropic, Gemini)로 변환해주는 고성능 Go 프록시 서버를 구축하는 것을 목표로 합니다.

## 1. 아키텍처 개요

전체 시스템은 단일 Docker 컨테이너 내에서 세 개의 핵심 프로세스가 협력하는 구조입니다.

### A. Antigravity Server (Headless IDE)
- **역할**: 구글 계정 인증 유지 및 토큰 자동 갱신.
- **동작**: `--headless` 모드로 실행되어 백그라운드에서 `state.vscdb` 파일을 업데이트하는 "인증 봇" 역할을 수행합니다.
- **추출 전략**: 로컬에 설치된 `~/.antigravity-server/bin/${ideVersion}-${commit}/` 디렉토리(약 340MB)를 통째로 추출하여 Docker 이미지에 포함합니다. 이 디렉토리는 Node.js 런타임, `ls_core` 바이너리, 그리고 필요한 모든 익스텐션을 포함하고 있어 고립된 환경 구축에 최적화되어 있습니다.

### B. Standalone `ls_core` (AI 엔진)
- **역할**: 실제 LLM 추론 및 컨텍스트 분석 수행.
- **동작**: IDE 프로세스와 분리되어 독립적으로 실행되며, Go 프록시로부터 주입받은 토큰을 사용하여 구글 클라우드 엔드포인트와 통신합니다.
- **경로**: `${SERVER_BUNDLE}/extensions/antigravity/bin/language_server_linux_x64`

### C. Go Proxy Server (오케스트레이터)
- **역할**: 토큰 관리 및 프로토콜 변환.
- **핵심 기능**:
    - **SQLite Scraper**: `state.vscdb`를 주기적으로 읽어 최신 OAuth 토큰 추출.
    - **Process Manager**: `ls_core` 바이너리의 생명주기 관리 및 필요 시 토큰 재주입/프로세스 재시작.
    - **API Transcoder**: OpenAI/Anthropic/Gemini 형식을 Antigravity의 HTTP/2 ConnectRPC 형식으로 상호 변환.

## 2. 주요 구현 포인트 (Golang)

### 2.1. 데이터 저장소 연동 (vscdb Scraper)
- `github.com/mattn/go-sqlite3`를 사용하여 `state.vscdb` 파일에 접근합니다.
- Antigravity Server가 파일을 점유하고 있을 수 있으므로, Read-Only 모드 또는 임시 복사본 생성 후 읽기 전략을 사용합니다.
- 토큰이 저장된 테이블(`globalState` 등)과 키 값을 명확히 식별하여 JSON 파싱을 수행합니다.

### 2.2. 통신 프로토콜 (HTTP/2 ConnectRPC Client)
- `golang.org/x/net/http2`를 사용하여 `ls_core`와 통신합니다.
- `ls_core`의 `GetModelResponse` 엔드포인트에 필요한 헤더(`connect-protocol-version`, `x-codeium-csrf-token` 등)를 정확히 구현합니다.

### 2.3. 표준 API 서버
- `github.com/gin-gonic/gin`을 사용하여 REST API 엔드포인트를 노출합니다.
- **지원 엔드포인트**:
    - `POST /v1/chat/completions` (OpenAI 형식)
    - `POST /v1/messages` (Anthropic 형식)
    - `POST /v1beta/models/:model` (Gemini 형식)
- 대화 기록(Messages)을 Antigravity가 이해할 수 있는 단일 `prompt` 문자열로 병합하는 포맷터(Formatter)를 구현합니다.

## 3. Docker 구동 전략

### 3.1. 볼륨 마운트
- 컨테이너 실행 시 호스트 머신에서 이미 인증된 `state.vscdb` 파일을 컨테이너 내부의 `/root/.antigravity-server/data/User/globalStorage/` 경로로 마운트합니다.
- 이를 통해 최초 실행 시 별도의 수동 로그인 과정 없이 즉시 서비스를 시작할 수 있습니다.

### 3.2. 멀티 프로세스 관리
- `supervisord` 또는 간단한 Go 기반의 프로세스 매니저를 사용하여 Antigravity Server와 Go Proxy를 동시에 실행하고 모니터링합니다.

## 4. 업데이트 및 동적 프로비저닝 전략

Antigravity는 표준 VS Code의 업데이트 경로 대신 구글의 Artifact Registry(APT)를 통해 배포됩니다.

- **업데이트 소스**: `https://us-central1-apt.pkg.dev/projects/antigravity-auto-updater-dev/dists/antigravity-debian/main/binary-amd64/Packages`
- **업데이트 로직 (Go Proxy)**:
    1. 주기적으로 위 APT `Packages` 인덱스를 파싱하여 최신 `Version` 및 `Filename`(.deb)을 확인합니다.
    2. 최신 버전을 감지하면 해당 `.deb` 파일을 다운로드합니다.
    3. `ar` 및 `tar` 명령(또는 Go 라이브러리)을 사용하여 `.deb` 내부의 `/opt/antigravity/` 또는 유사 경로에 포함된 서버 번들을 추출합니다.
    4. 추출된 번들을 새 버전 디렉토리에 설치하고, 기존 프로세스를 종료한 뒤 새 버전으로 재시작합니다.

## 5. 단계별 실행 계획

1.  **Phase 1: Research (역공학)**
    - `ls_core`가 실행될 때 받는 인자와 환경변수 전수 조사.
    - `state.vscdb` 내의 토큰 저장 위치 및 JSON 스키마 확정.
2.  **Phase 2: Scraper 개발**
    - Go로 SQLite에서 토큰을 추출하고 구조체로 매핑하는 기능 구현.
3.  **Phase 3: Bridge 개발**
    - `ls_core`와 HTTP/2 통신을 주고받는 핵심 클라이언트 로직 구현.
4.  **Phase 4: API Layer 개발**
    - OpenAI 등 표준 API 포맷 매핑 및 Gin 서버 구축.
5.  **Phase 5: Dockerization**
    - Antigravity 설치 및 Go 바이너리가 포함된 Dockerfile 작성 및 배포.

---
**작성일**: 2026-05-01
**상태**: Draft / Planning
