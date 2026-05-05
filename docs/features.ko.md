# 기능 개요

이 문서는 현재 저장소가 제공하는 기능을 요약하고, 초기 구현 계획이 끝난 뒤 남아 있는 정리 항목을 함께 정리한다.

## 상태 요약

2026-04-29 기준으로 핵심 제품 기능은 대부분 구현되었지만, 보조 항목까지 완전히 끝난 것은 아니다.

### 구현 완료

- 저장소 bootstrap, Go module 구조, 패키지 분리
- 공용 상수, sentinel error, structured logging, redaction
- atomic write, flock, backup, rollback을 포함한 safe file IO
- CA 생성, 서버/클라이언트 인증서 발급, TLS config, 검증을 포함한 PKI 도구
- config 로드, 경로 확장, profile store reload, SIGHUP reload
- WebSocket transport, codec, reconnect 로직
- debounce/stable-window 기반 파일 watcher
- format 프레임워크와 3개 format 구현
  - Claude
  - Codex
  - Gemini
- 서버 mediator와 클라이언트 수렴 로직
- `serve`, `connect`, `setup`, `ca`, `inspect`, `status`, `version`, `jwt` CLI
- `--also-client` self-client 기능
- `mtls` 인증 모드
- `jwt` 인증 모드
  - upgrade 시 Authorization 헤더 사용
  - 헤더가 없을 때 `auth.jwt` first-frame fallback
- `client -> server`가 안 되고 `server -> client`만 가능한 환경을 위한 reverse 연결 지원
  - 클라이언트의 `reverse-client`
  - 서버 호스트의 별도 bridge 프로세스 `reverse-connect`
  - `transport: direct` 직접 reverse target
  - 기본적으로 로컬 `~/.ssh` 설정을 따르는 `transport: ssh` managed reverse target
- notifier sink
  - Telegram
  - Slack
  - Discord
  - Mattermost
  - ntfy
  - Teams
- usage/validity 메타데이터를 포함한 event-driven propagation notification
- `/claude`, `/codex`, `/gemini`, `/status`, `/help` Telegram bot command polling
- 인증 정보가 만료됐거나 만료 임박이고 provider CLI가 `PATH`에서 발견될 때 Claude, Codex, Gemini 공식 CLI를 통한 refresh nudge
- nvm 환경을 지원하도록 `bash -lic`로 실행되는, npm 설치 Claude/Codex/Gemini CLI의 주기적 upgrade
- 주요 sync/auth 시나리오를 다루는 unit/e2e 테스트
- test, coverage, multi-target build를 수행하는 GitHub Actions CI

### 부분 완료 또는 계획과 달라진 부분

- 현재 코드는 초기 구현 계획 범위를 몇 군데서 넘어섰다
  - JWT auth 추가
  - Codex/Gemini format 추가
  - richer notifier 추가
  - JWT tooling과 추가 verify CLI 추가
- CI가 주요 타깃 빌드는 수행하지만 `go test -race`를 강제하지는 않는다
- Makefile에는 `integration` 테스트 대상이 있지만, CI에 별도 `-tags=integration` job은 아직 없다

### 아직 없는 항목

- §F에 적힌 `docker-compose.yml` 기반 E2E fixture
- §G에서 기대한 형태의 일관된 package별 `doc.go` 또는 package README

## 제품 기능

## 보안 인증 정보 동기화

- 지원되는 CLI 도구의 인증 상태를 여러 노드 간 동기화
- validation을 통과한 account-partitioned state만 전파
- format별 account identity로 분리해서 다른 계정 토큰이 덮어써지는 것을 방지
- 수신측에서는 atomic write와 rollback-aware apply를 사용

## 인증 모드

- `mtls` 모드
  - mutual TLS client authentication
  - client identity 기반 profile ACL
- `jwt` 모드
  - WebSocket upgrade 시 JWT bearer 검증
  - `Authorization` 헤더가 없을 때 `auth.jwt` first-frame fallback
  - JWT claim 기반 profile authorization

## 지원 포맷

- Claude credentials JSON
  - token propagation
  - account metadata lookup
  - plan / organization probe
- Codex auth JSON
  - token propagation
  - JWT claim 기반 account extraction
  - numeric usage probe
- Gemini OAuth credentials JSON
  - token propagation
  - `id_token` 기반 account extraction
  - tier / validity probe

관련 문서:

- [Claude notes](./claude.ko.md)
- [Codex notes](./codex.ko.md)
- [Gemini notes](./gemini.ko.md)

## 서버 기능

- profile별 session hub
- mediator 기반 truth selection
- stale-only 또는 더 넓은 propagation 제어
- cooldown 기반 truth change suppression
- duplicate identity displacement
- `--also-client`를 통한 in-process self-client
- unix socket status endpoint
- reverse bridge가 붙을 수 있는 local unix-socket attach endpoint
- SIGHUP 기반 profile reload

## 클라이언트 기능

- 최초 스캔 + 이벤트 기반 보고
- backoff가 있는 reconnect loop
- lock, backup, verification, ACK를 포함한 로컬 apply
- JWT header 또는 first-frame 인증
- format-aware account resolution
- 보조 메타 파일 변경 시 watcher 기반 재보고
- 만료 임박 시 공식 provider CLI를 통한 refresh nudge. `pangaeactl` 자체가 provider OAuth refresh를 구현하지는 않음
- global npm 설치에 한정한 공식 CLI 주기적 upgrade. npm이 아닌 설치는 감지 후 건드리지 않음
- 서버가 먼저 접속하는 reverse-client listener 옵션

## CLI 기능

- `serve`
- `connect`
- `reverse-client`
- `reverse-connect`
- `setup server`
- `setup client`
- `ca init`
- `ca issue-server`
- `ca issue-client`
- `ca verify-server`
- `ca verify-client`
- `inspect`
- `status`
- `version`
- `jwt init`
- `jwt issue`
- `jwt verify`

## 알림 기능

- 주기적인 truth summary
- 주기 summary는 최소 1시간 간격으로만 보내며, 렌더링 digest가 이전과 같으면 생략
- 새 truth가 peer로 push되고 usage/validity probe가 의미 있는 metadata를 만들었을 때 즉시 전파 알림
- session connect/disconnect 이벤트는 알림 노이즈를 줄이기 위해 현재 notifier layer에서 억제
- `/claude`, `/codex`, `/gemini`, `/status`, `/help` Telegram command 응답
- 목적지별 렌더링 최적화
- 알림에 포함될 수 있는 정보
  - source node
  - target nodes
  - LLM/provider
  - account
  - validity window
  - usage 또는 remaining quota
  - plan/tier/organization metadata

## 빌드 / 릴리스 기능

- Makefile 기반 multi-target build
- Linux, macOS, Windows release build
- Linux static release output
- build time version injection
- CI에서 E2E를 별도 실행하고 library/internal package coverage를 측정해 coverage badge 생성

## 다음 정리 우선순위

- 로컬 multi-node E2E용 `docker-compose.yml` 추가
- CI에 `go test -race` 추가
- `-tags=integration` 전용 CI job 추가
- 전체 docs 트리를 영문 기본 + `.ko.md` 병행 체계로 계속 정리
