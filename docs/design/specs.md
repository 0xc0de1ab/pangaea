# Current Design Notes

이 문서는 현재 소스 구현을 기준으로 한 설계 메모다. 초기 구현 계획이나 로드맵이 아니라, 2026-04-29 현재 코드와 맞는 동작만 기록한다.

## Scope

`pangaeactl`은 여러 노드에서 같은 LLM CLI 계정을 사용할 때 로컬 OAuth 인증 파일을 수렴시키는 도구다.

지원 포맷:

- Claude Code CLI: `claude-credentials-json-format`
- OpenAI Codex CLI: `codex-auth-json-format`
- Gemini CLI: `gemini-oauth-creds-json-format`

지원 인증 모드:

- `mtls`: 기본값. TLS client certificate의 CN을 node identity로 사용한다.
- `jwt`: WebSocket upgrade의 `Authorization: Bearer <token>` 또는 첫 프레임 `auth.jwt`를 node identity로 사용한다.

## CLI

현재 root command:

```text
pangaeactl
├── ca
│   ├── init
│   ├── issue-server
│   ├── issue-client
│   ├── verify-server
│   └── verify-client
├── connect
├── inspect
├── jwt
│   ├── init
│   ├── issue
│   └── verify
├── reverse-client
├── reverse-connect
├── serve
├── setup
│   ├── server
│   └── client
├── status
└── version
```

`serve`는 `--also-client <profile,...>`를 지원한다. 이 경우 서버 프로세스 안에서 지정 profile의 self-client agent를 함께 실행한다. `--also-client`를 쓰려면 `server.self_node.enabled=true`와 `self_node.client_cert`, `self_node.client_key`가 필요하다.

## Config Files

### `pangaea-server.yaml`

```yaml
listen: "0.0.0.0:8443"
auth_mode: "mtls"
pki:
  ca_cert: "/etc/pangaea/pki/ca.crt"
  server_cert: "/etc/pangaea/pki/server/server.crt"
  server_key: "/etc/pangaea/pki/server/server.key"
profiles_file: "/etc/pangaea/profiles.yaml"
self_node:
  enabled: true
  client_cert: "/etc/pangaea/issued-clients/opi5/server-client.crt"
  client_key: "/etc/pangaea/issued-clients/opi5/server-client.key"
log:
  level: "info"
  format: "json"
```

`self_node.node_id` 필드는 없다. self-node identity는 self-node client certificate의 CN에서 읽고, 읽지 못하면 `<hostname>(server)`로 fallback한다.

`jwt` 모드에서는 다음이 추가로 필요하다.

```yaml
auth_mode: "jwt"
jwt:
  secret_key_file: "/etc/pangaea/jwt.secret"
  issuer: "pangaea"
  audience: "pangaea"
  allow_first_frame_fallback: true
  auth_timeout: "45s"
```

서버는 현재 TLS certificate/key를 필수로 요구한다. plain HTTP backend는 현재 config/CLI 경로에서 지원하지 않는다.

### `profiles.yaml`

```yaml
profiles:
  - name: "claude"
    format: "claude-credentials-json-format"
    dir: "~/.claude"
    watch_files:
      - ".credentials.json"
      - "~/.claude.json"
      - ".config.json"
    allowed_clients:
      - "snowbox"
      - "opi5(server)"
    validate:
      strategy: "expires_at_max"
      live_check: false
      live_check_timeout: "5s"
    propagate:
      mode: "to_stale_only"
      cooldown: "2s"
```

`dir`는 profile의 설정 디렉토리다. `~`, `$HOME`, `${HOME}` 같은 환경변수 경로가 확장된다. `watch_files`가 비어 있으면 format의 기본 watch 파일이 사용된다. watcher는 지정 파일의 부모 디렉토리를 감시하되, 이벤트는 지정 파일로 필터링한다.

`reverse_targets`는 `reverse-connect` 전용이다.

```yaml
reverse_targets:
  - node_id: "a2"
    transport: "ssh"
```

`transport` 기본값은 `direct`다. `direct`는 `url`이 필요하고, `ssh`는 `url`이 없어야 한다.

### `pangaea-client.yaml`

```yaml
server: "wss://hub.example.com:8443"
auth_mode: "mtls"
node_id: "snowbox"
profiles:
  - name: "claude"
    format: "claude-credentials-json-format"
    dir: "~/.claude"
    watch_files:
      - ".credentials.json"
      - "~/.claude.json"
      - ".config.json"
    account_meta_path: "~/.claude.json"
pki:
  ca_cert: "/etc/pangaea/ca.crt"
  client_cert: "/etc/pangaea/client.crt"
  client_key: "/etc/pangaea/client.key"
reconnect:
  initial_delay: "5s"
  jitter: "1s"
  max_delay: "60s"
```

`jwt` client는 `jwt.token_file` 또는 `jwt.token_env` 중 하나가 필요하다. `jwt.send_via` 값은 `auto`, `header`, `first_frame`을 지원한다.

## Paths And Format Defaults

Format별 primary credentials path:

- Claude: `<dir>/.credentials.json`
- Codex: `<dir>/auth.json`
- Gemini: `<dir>/oauth_creds.json`

Format별 기본 watch 파일:

- Claude: `<dir>/.credentials.json`, `~/.claude.json`, `<dir>/.config.json`
- Codex: `<dir>/auth.json`
- Gemini: `<dir>/oauth_creds.json`

Claude는 계정 식별용 메타데이터가 credentials 파일에 없어서 `~/.claude.json` 또는 legacy `<dir>/.config.json`을 함께 본다. Codex와 Gemini는 기본적으로 인증 파일 하나에서 계정 식별 정보를 얻는다.

## Transport

일반 연결:

- client가 `wss://<server>/ws/profile/<profile>`로 접속한다.
- `mtls`에서는 TLS client cert를 검증하고 CN을 node identity로 사용한다.
- `jwt`에서는 upgrade header 또는 첫 프레임 JWT를 검증하고 JWT subject를 node identity로 사용한다.
- `allowed_clients`는 이 node identity 기준으로 검사된다.

Reverse 연결:

- `reverse-client`는 클라이언트 노드에서 TLS WebSocket listener를 연다.
- `reverse-connect`는 서버 호스트의 별도 프로세스다.
- `reverse-connect`는 remote reverse-client와 local unix attach socket을 연결한다.
- local attach는 `/attach/profile/<profile>` 경로를 사용한다.
- remote reverse-client는 `/reverse/profile/<profile>` 경로를 사용한다.

SSH managed reverse:

- `server.ssh_nodes[].target`은 필수다.
- `use_ssh_config` 기본값은 `true`다.
- 기본적으로 실행 계정의 `~/.ssh/config`, `~/.ssh/known_hosts`, SSH agent, identity file을 따른다.
- `reverse_addr`가 비어 있으면 원격에서 `pangaeactl reverse-client --listen 127.0.0.1:0 --print-listen-addr` 형태로 managed process를 시작한다.
- `reverse_addr`가 있으면 이미 떠 있는 remote reverse-client에 attach한다.

## Mediation

서버 mediator는 profile 안에서도 account별로 독립된 truth를 유지한다.

핵심 규칙:

- 후보는 `(profile, account, node)` 단위로 관리된다.
- 같은 profile이라도 account가 다르면 서로 전파하지 않는다.
- truth selection은 format별 strategy를 따른다.
- 기본 propagation mode는 `to_stale_only`다.
- truth source node 자신에게는 같은 truth를 다시 push하지 않는다.
- duplicate node identity가 새로 붙으면 이전 session은 displacement된다.

Format별 strategy:

- Claude: `expires_at_max`
- Codex: `jwt_exp_max`, 동률이면 `last_refresh`
- Gemini: `expiry_date_max`

## Validation And Usage Probes

Validation은 snapshot을 전파할 가치가 있는지 판단한다.

- Claude: `expiresAt` 기반. `live_check=true`면 `GET /api/oauth/profile`을 한 번 수행한다.
- Codex: access token JWT `exp`와 `last_refresh` 기반. `last_refresh`가 8일보다 오래되면 stale로 본다.
- Gemini: `expiry_date` 기반. 만료 5분 이내는 전파 가치가 낮은 것으로 본다.

Usage probe는 notifier가 메시지를 보낼 때 수행한다.

- Claude: Anthropic OAuth usage endpoint를 사용해 session/week/Sonnet/extra usage window를 만든다.
- Codex: `https://chatgpt.com/backend-api/wham/usage`를 사용해 5h/weekly 및 추가 model-specific window를 만든다.
- Gemini: Code Assist load/quota endpoint를 사용해 `Flash`, `Flash Lite`, `Pro` window를 만든다.

## Refresh Nudge

`pangaeactl`은 provider OAuth refresh protocol을 직접 구현하지 않는다. 대신 client agent가 인증 파일이 만료됐거나 만료 임박한 경우 공식 CLI를 짧게 실행해 refresh를 유도할 수 있다.

조건:

- provider command가 `PATH`에서 발견되어야 한다.
- 동일 fingerprint/reason에 대해서는 cooldown이 적용된다.
- 명령 실행 후 파일 fingerprint가 바뀌면 기존 watcher 경로로 재보고된다.

현재 nudge 대상:

- Claude: `claude auth login` 또는 oneshot prompt
- Codex: `codex exec` oneshot prompt
- Gemini: `gemini -p` oneshot prompt

## Notifications

지원 sink:

- Telegram
- Slack
- Discord
- Mattermost
- ntfy
- Teams

주기 summary:

- `notifier.*.interval`이 설정되어도 최소 1시간보다 짧게 실행되지 않는다.
- sink/route별 digest가 이전과 같으면 보내지 않는다.
- Telegram은 profile/account별 제목과 `<pre>` usage block을 사용한다.

Event-driven 알림:

- truth lost/restored는 startup grace 이후에만 전송된다.
- propagation 알림은 usage/validity probe가 의미 있는 결과를 낸 경우에만 전송된다.
- session connect/disconnect 이벤트는 현재 `Notifier.Emit`에서 sink로 보내지 않는다.

Telegram command polling:

- Telegram sink가 켜져 있으면 `/claude`, `/codex`, `/gemini`, `/status`, `/help`를 처리한다.
- 설정된 `default_chat_id`와 `routes[].chat_id`에서 온 명령만 허용한다.
- profile 명령은 연결된 node에 snapshot request를 보내고 잠깐 기다린 뒤 현재 서버가 아는 auth state와 usage metadata를 응답한다.

## Build And Release

Makefile matrix:

- OS: `linux`, `darwin`, `windows`
- arch: `amd64`, `arm64`
- variant: `debug`, `release`

Release build:

- `CGO_ENABLED=0`
- stripped binary
- Linux는 static link를 목표로 한다.

Version:

- base: `v0.9.0-202604.1`
- build-time output: `vSEMVER-YYYYMM.seq.<short-git-sha>`
- tag/release format: `vSEMVER-YYYYMM.seq`

CI:

- `go test ./e2e`
- package coverage with `go test -covermode=atomic`, excluding `cmd/pangaeactl` and `e2e`
- Linux/macOS/Windows release build matrix
- coverage badge update on `main`
- tag push release workflow for `vSEMVER-YYYYMM.seq`

## Known Gaps

현재 소스에 없는 항목:

- Docker Compose 기반 multi-node fixture
- CI의 `go test -race` 강제 실행
- CI의 별도 `-tags=integration` job
- provider OAuth refresh protocol의 직접 구현
