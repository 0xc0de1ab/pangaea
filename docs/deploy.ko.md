# 배포 가이드

이 문서는 `pangaeactl`의 실제 배포 방법을 정리합니다.

지원 인증 방식:

- `mtls`
- `jwt`

또한 interactive `setup` 커맨드, Telegram 알림 설정, systemd 등록까지 함께 설명합니다.

## 전제 조건

`pangaeactl`은 계정을 새로 만들어 주는 도구가 아니라, 이미 로그인된 인증 상태를 여러 노드에 동기화하는 도구입니다.

반드시 다음 조건에서만 사용해야 합니다.

- 두 대 이상의 PC가 이미 같은 Claude 계정으로 로그인되어 있음
- 두 대 이상의 PC가 이미 같은 Codex / ChatGPT 계정으로 로그인되어 있음
- 두 대 이상의 PC가 이미 같은 Gemini 계정으로 로그인되어 있음

즉, 서로 다른 사람의 계정을 공유하기 위한 용도가 아닙니다.

## 현재 지원하는 CLI

- Claude CLI
- Codex CLI
- Gemini CLI

포맷별 파일 상세는 다음 문서를 참고하세요.

- [Claude notes](./claude.ko.md)
- [Codex notes](./codex.ko.md)
- [Gemini notes](./gemini.ko.md)

## 생성되는 기본 파일 구조

interactive bootstrap 기본 출력 디렉토리:

- 서버: `./deploy/server`
- 클라이언트: `./deploy/client`

서버 쪽에는 보통 다음이 생성됩니다.

- `pangaea-server.yaml`
- `profiles.yaml`
- `pki/ca.crt`
- `pki/ca.key`
- `pki/server/server.crt`
- `pki/server/server.key`
- `issued-clients/<node>/...` (`mtls`일 때)
- `jwt.secret`, `issued-jwt/<node>.token` (`jwt`일 때)
- `systemd/*.service`

클라이언트 쪽에는 보통 다음이 생성됩니다.

- `pangaea-client.yaml`
- `pangaea-client.env` (`jwt.token_env`를 쓸 때)
- `systemd/*.service`

## mTLS 배포

### 빠른 방법: setup 커맨드

서버 bootstrap:

```bash
pangaeactl setup server --out ./deploy/server
```

이 wizard는 다음을 물어봅니다.

- `auth_mode`
- listen address
- TLS server host / SAN
- 추가 SAN
- 초기 client node ID 목록
- profile 목록
- 초기 client certificate 발급 여부
- Telegram notifier 설정 여부
- systemd unit 생성 여부

`mtls` 모드에서 핵심 산출물:

- CA 인증서 / 키
- 서버 인증서 / 키
- 요청 시 node별 client 인증서 번들

클라이언트 bootstrap:

```bash
pangaeactl setup client --out ./deploy/client
```

클라이언트 wizard에는 다음을 넣으면 됩니다.

- 서버 URL 예: `wss://hub.example.com:8443`
- node ID
- 로컬 인증 디렉토리 예: `~/.claude`, `~/.codex`, `~/.gemini`
- 서버에서 받아온 `ca.crt`, `client.crt`, `client.key` 경로

실행:

```bash
pangaeactl serve -c ./deploy/server/pangaea-server.yaml
pangaeactl connect -c ./deploy/client/pangaea-client.yaml
```

### Reverse mTLS 배포: 클라이언트는 서버에 못 붙고, 서버만 클라이언트에 붙을 수 있는 경우

네트워크 정책 때문에 `client -> server`는 막혀 있는데, 서버 호스트에서는 클라이언트 쪽으로 outbound 접속이 가능한 경우가 있습니다. 이때는 다음 조합을 사용합니다.

- 클라이언트 노드: `pangaeactl reverse-client`
- 서버 호스트의 별도 bridge 프로세스: `pangaeactl reverse-connect`

기존 `pangaeactl serve` 프로세스는 그대로 두고, reverse bridge만 별도 프로세스로 띄웁니다. 이 bridge는 로컬 unix socket status 서버에 attach한 뒤, profile별 WebSocket 스트림을 reverse-client 쪽과 프록시합니다.

핵심 사항:

- 클라이언트는 기존 `pangaea-client.yaml`의 profile 설정을 그대로 사용합니다
- 대신 `reverse:` listener 블록을 추가합니다
- 서버는 기존 `profiles.yaml`에 `reverse_targets`를 추가합니다
- reverse tunnel 자체는 서버 호스트 bridge와 reverse-client listener 사이에서 mTLS를 사용합니다
- `server.self_node.enabled`가 반드시 켜져 있어야 합니다. reverse bridge가 서버의 self-node client certificate로 reverse listener에 인증하기 때문입니다

클라이언트 예시:

```yaml
server: "wss://hub.example.com:8443"
auth_mode: "mtls"
node_id: "laptop-a"
profiles:
  - name: "claude"
    format: "claude-credentials-json-format"
    dir: "~/.claude"
    watch_files:
      - ".credentials.json"
      - "~/.claude.json"
      - ".config.json"
pki:
  ca_cert: "/abs/path/client/pki/ca.crt"
  client_cert: "/abs/path/client/pki/client.crt"
  client_key: "/abs/path/client/pki/client.key"
reverse:
  listen: ":9443"
  pki:
    ca_cert: "/abs/path/client/pki/ca.crt"
    server_cert: "/abs/path/client/pki/reverse-server.crt"
    server_key: "/abs/path/client/pki/reverse-server.key"
  allowed_peers:
    - "hub-bridge"
```

클라이언트 실행:

```bash
pangaeactl reverse-client -c ./deploy/client/pangaea-client.yaml
```

서버 측 `profiles.yaml` 예시:

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
      - "laptop-a"
    reverse_targets:
      - node_id: "laptop-a"
        transport: "direct"
        url: "wss://laptop-a.example.net:9443"
```

서버 측 `pangaea-server.yaml`에는 self-node 인증서가 있어야 합니다:

```yaml
listen: "0.0.0.0:8443"
auth_mode: "mtls"
profiles_file: "/abs/path/deploy/server/profiles.yaml"
self_node:
  enabled: true
  client_cert: "/abs/path/deploy/server/issued-clients/hub-bridge/client.crt"
  client_key: "/abs/path/deploy/server/issued-clients/hub-bridge/client.key"
pki:
  ca_cert: "/abs/path/deploy/server/pki/ca.crt"
  server_cert: "/abs/path/deploy/server/pki/server/server.crt"
  server_key: "/abs/path/deploy/server/pki/server/server.key"
```

`self_node.node_id` 설정 필드는 없습니다. self-node identity는 `self_node.client_cert`의 Common Name이고, 인증서를 읽지 못하면 `<hostname>(server)`로 fallback됩니다. reverse-client의 `allowed_peers`에는 이 self-node 인증서 CN을 넣어야 합니다.

이 self-node 인증서는 `pangaeactl ca issue-client --cn hub-bridge ...`처럼 명시적인 CN으로 미리 발급하세요. `serve --also-client`도 함께 쓴다면 해당 self-node CN을 관련 profile의 `allowed_clients`에 포함해야 합니다.

메인 서버는 평소처럼 실행:

```bash
pangaeactl serve -c ./deploy/server/pangaea-server.yaml
```

그 다음 같은 호스트에서 reverse bridge를 별도 프로세스로 실행:

```bash
pangaeactl reverse-connect \
  -c ./deploy/server/pangaea-server.yaml \
  --socket /tmp/pangaea.sock
```

권장 systemd 분리:

- `pangaea-server.service`는 `serve`
- `pangaea-reverse-connect.service`는 `reverse-connect`

이렇게 하면 reverse dialing과 reconnect churn이 메인 서버 프로세스에 직접 섞이지 않습니다.

### SSH 기반 Reverse 연결

서버 호스트에서 원격 노드로 SSH 접속이 가능하다면, `reverse-connect`가 고정된 public reverse listener 주소 대신 SSH를 통해 그 노드를 관리할 수 있습니다.

SSH node registry는 `server.yaml`에 둡니다:

```yaml
ssh_nodes:
  - node_id: "a2"
    target: "dh.kam@a2.oci.example.com"
    port: 2222
```

메모:

- `target`은 필수입니다
- `use_ssh_config`의 기본값은 `true`입니다
- bridge는 기본적으로 로컬 실행 계정의 `~/.ssh/config`, `~/.ssh/known_hosts`, SSH agent, identity file을 따릅니다
- `port`는 optional이며 SSH config의 `Port`를 override합니다

그 다음 `profiles.yaml`에서 해당 노드를 참조합니다:

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
      - "a2"
    reverse_targets:
      - node_id: "a2"
        transport: "ssh"
```

기본적으로 SSH bridge는 원격에서 다음 조건으로 `pangaeactl reverse-client`를 실행합니다:

- command: `pangaeactl`
- config path: `$HOME/pangaea-client.yaml`
- listen address: `127.0.0.1:0`

즉 원격 reverse-client가 loopback의 빈 포트를 자동으로 하나 골라서 열고, 그 실제 포트를 bridge에 다시 알려줍니다. 운영자가 미리 reverse listener 포트를 정할 필요는 없습니다.

필요하면 고급 override도 가능합니다:

```yaml
ssh_nodes:
  - node_id: "a2"
    target: "dh.kam@a2.oci.example.com"
    use_ssh_config: true
    port: 2222
    command: "/usr/local/bin/pangaeactl"
    config_path: "/home/dh.kam/pangaea-client.yaml"
    reverse_addr: "127.0.0.1:9443"
```

`reverse_addr`를 주면 managed mode 대신 attach mode로 동작합니다. 즉 bridge가 원격에서 이미 떠 있는 reverse-client에 붙고, 새 프로세스를 직접 시작하지 않습니다.

### 수동 서버 설정

CA 생성:

```bash
pangaeactl ca init \
  --out ./deploy/server/pki \
  --cn "pangaeactl Root CA" \
  --years 10
```

서버 인증서 발급:

```bash
pangaeactl ca issue-server \
  --ca ./deploy/server/pki \
  --out ./deploy/server/pki/server \
  --cn hub.example.com \
  --san DNS:hub.example.com,IP:10.0.0.10 \
  --years 1
```

검증:

```bash
pangaeactl ca verify-server \
  --ca ./deploy/server/pki \
  --cert ./deploy/server/pki/server/server.crt \
  --server-name hub.example.com
```

클라이언트 인증서 발급. CN은 반드시 `node_id`와 같아야 합니다.

```bash
pangaeactl ca issue-client \
  --ca ./deploy/server/pki \
  --out ./deploy/server/issued-clients/laptop-a \
  --cn laptop-a \
  --years 1
```

검증:

```bash
pangaeactl ca verify-client \
  --ca ./deploy/server/pki \
  --cert ./deploy/server/issued-clients/laptop-a/client.crt \
  --cn laptop-a
```

`pangaea-server.yaml` 예시:

```yaml
listen: "0.0.0.0:8443"
auth_mode: "mtls"
pki:
  ca_cert: "/abs/path/deploy/server/pki/ca.crt"
  server_cert: "/abs/path/deploy/server/pki/server/server.crt"
  server_key: "/abs/path/deploy/server/pki/server/server.key"
log:
  level: "info"
  format: "json"
profiles_file: "/abs/path/deploy/server/profiles.yaml"
```

`profiles.yaml` 예시:

```yaml
profiles:
  - name: "claude-prod"
    format: "claude-credentials-json-format"
    dir: "~/.claude"
    watch_files:
      - ".credentials.json"
      - "~/.claude.json"
      - ".config.json"
    allowed_clients:
      - "laptop-a"
      - "desktop-b"
    validate:
      strategy: "expires_at_max"
      live_check: false
      live_check_timeout: "5s"
    propagate:
      mode: "to_stale_only"
      cooldown: "2s"
```

서버 실행:

```bash
pangaeactl serve -c ./deploy/server/pangaea-server.yaml
```

### 수동 클라이언트 설정

서버에서 다음 파일을 클라이언트로 복사합니다.

- `ca.crt`
- `client.crt`
- `client.key`

`pangaea-client.yaml` 예시:

```yaml
server: "wss://hub.example.com:8443"
auth_mode: "mtls"
node_id: "laptop-a"
profiles:
  - name: "claude-prod"
    format: "claude-credentials-json-format"
    dir: "~/.claude"
    watch_files:
      - ".credentials.json"
      - "~/.claude.json"
      - ".config.json"
    account_meta_path: "~/.claude.json"
pki:
  ca_cert: "/abs/path/client/pki/ca.crt"
  client_cert: "/abs/path/client/pki/client.crt"
  client_key: "/abs/path/client/pki/client.key"
reconnect:
  initial_delay: "5s"
  jitter: "1s"
  max_delay: "60s"
log:
  level: "info"
  format: "json"
```

실행:

```bash
pangaeactl connect -c ./deploy/client/pangaea-client.yaml
```

## JWT 배포

`jwt` 모드는 ingress 같은 중간 TLS termination 환경을 위해 준비된 모드입니다.

중요:

- backend 서버는 여전히 `wss://`로 떠야 합니다
- backend TLS는 계속 필요합니다
- JWT는 client identity를 대체할 뿐 transport 암호화를 대체하지 않습니다
- backend plain HTTP는 현재 CLI/config 경로에서 지원하지 않습니다. 클라이언트는 `wss://`를 요구하고 서버도 TLS certificate/key를 요구합니다
- Kubernetes ingress라면 ingress -> backend HTTPS 재암호화를 강하게 권장합니다

클라이언트는 먼저 `Authorization: Bearer <jwt>`를 시도합니다. 그 헤더가 프록시에서 제거되면 서버가 `auth.jwt` 첫 프레임 fallback을 요구할 수 있습니다. 특별한 이유가 없으면 `jwt.send_via: auto`를 유지하세요.

### 빠른 방법: setup 커맨드

서버 bootstrap:

```bash
pangaeactl setup server --out ./deploy/server
```

wizard에서 `auth_mode=jwt`를 선택합니다.

생성되는 핵심 산출물:

- CA 인증서 / 키
- backend server 인증서 / 키
- `jwt.secret`
- 요청 시 node별 JWT token
- `pangaea-server.yaml`
- `profiles.yaml`

클라이언트 bootstrap:

```bash
pangaeactl setup client --out ./deploy/client
```

wizard에서 `auth_mode=jwt`를 선택합니다.

토큰 전달 방식은 다음 중 하나를 선택하면 됩니다.

- token file
- environment variable

보통은:

- 별도 파일 배포면 `token_file`
- secret manager / systemd env file이면 `token_env`
- 전송 방식은 `send_via: auto`

### 수동 서버 설정

CA와 서버 인증서는 `mtls` 절과 동일하게 생성합니다.

그 다음 JWT secret 생성:

```bash
pangaeactl jwt init --out ./deploy/server/jwt.secret
```

node별 token 발급:

```bash
pangaeactl jwt issue \
  --secret-key ./deploy/server/jwt.secret \
  --node-id laptop-a \
  --profile claude-prod \
  --profile codex-prod \
  --issuer pangaea \
  --audience pangaea \
  --ttl 720h \
  --out ./deploy/server/issued-jwt/laptop-a.token
```

검증:

```bash
pangaeactl jwt verify \
  --secret-key ./deploy/server/jwt.secret \
  --token @./deploy/server/issued-jwt/laptop-a.token \
  --issuer pangaea \
  --audience pangaea
```

`pangaea-server.yaml` 예시:

```yaml
listen: "0.0.0.0:8443"
auth_mode: "jwt"
pki:
  ca_cert: "/abs/path/deploy/server/pki/ca.crt"
  server_cert: "/abs/path/deploy/server/pki/server/server.crt"
  server_key: "/abs/path/deploy/server/pki/server/server.key"
jwt:
  secret_key_file: "/abs/path/deploy/server/jwt.secret"
  issuer: "pangaea"
  audience: "pangaea"
  allow_first_frame_fallback: true
log:
  level: "info"
  format: "json"
profiles_file: "/abs/path/deploy/server/profiles.yaml"
```

서버 실행:

```bash
pangaeactl serve -c ./deploy/server/pangaea-server.yaml
```

### 수동 클라이언트 설정

backend TLS 검증을 위해 `ca.crt`를 클라이언트로 복사합니다.

token file 기반 예시:

```yaml
server: "wss://hub.example.com:8443"
auth_mode: "jwt"
jwt:
  token_file: "/abs/path/client/jwt.token"
  send_via: "auto"
node_id: "laptop-a"
profiles:
  - name: "codex-prod"
    format: "codex-auth-json-format"
    dir: "~/.codex"
    watch_files:
      - "auth.json"
pki:
  ca_cert: "/abs/path/client/pki/ca.crt"
reconnect:
  initial_delay: "5s"
  jitter: "1s"
  max_delay: "60s"
log:
  level: "info"
  format: "json"
```

environment variable 기반 예시:

```yaml
server: "wss://hub.example.com:8443"
auth_mode: "jwt"
jwt:
  token_env: "PANGAEA_JWT_TOKEN"
  send_via: "auto"
node_id: "laptop-a"
profiles:
  - name: "claude-prod"
    format: "claude-credentials-json-format"
    dir: "~/.claude"
    watch_files:
      - ".credentials.json"
      - "~/.claude.json"
      - ".config.json"
    account_meta_path: "~/.claude.json"
pki:
  ca_cert: "/abs/path/client/pki/ca.crt"
reconnect:
  initial_delay: "5s"
  jitter: "1s"
  max_delay: "60s"
log:
  level: "info"
  format: "json"
```

실행:

```bash
pangaeactl connect -c ./deploy/client/pangaea-client.yaml
```

## Telegram 알림 설정

Telegram 설정은 서버 쪽에서만 합니다.

### Bot 준비

1. `@BotFather`로 bot 생성
2. bot token을 env var 또는 env file에 저장
3. bot에 한 번 메시지를 보내거나, 대상 group/channel에 bot을 추가
4. `getUpdates`로 `chat_id` 확인

예시:

```bash
export PANGAEA_TELEGRAM_BOT_TOKEN='123456:abc...'
curl "https://api.telegram.org/bot${PANGAEA_TELEGRAM_BOT_TOKEN}/getUpdates"
```

반환 JSON에서 원하는 `chat.id`를 확인하면 됩니다.

서버 config 예시:

```yaml
notifier:
  telegram:
    enabled: true
    bot_token_env: "PANGAEA_TELEGRAM_BOT_TOKEN"
    default_chat_id: "-1001234567890"
    interval: "1h"
    probe_timeout: "8s"
    disable_notification: false
```

profile/account별 routing도 가능합니다.

```yaml
notifier:
  telegram:
    enabled: true
    bot_token_env: "PANGAEA_TELEGRAM_BOT_TOKEN"
    routes:
      - profile: "claude-prod"
        account: "user@example.com"
        chat_id: "-1001234567890"
```

`setup server`에서 Telegram을 켰다면 placeholder env file도 생성됩니다.

```bash
./deploy/server/pangaea-server.env
```

서비스 시작 전에 bot token 값을 채워 넣으세요.

notifier는 주기 알림의 최소 interval을 1시간으로 강제합니다. 주기 summary는 sink/route별 digest가 이전과 같으면 다시 보내지 않습니다. event-driven propagation 알림은 usage/validity probe가 의미 있는 metadata를 만들었을 때만 보내며, session connect/disconnect 알림은 노이즈를 줄이기 위해 현재 sink로 보내지 않습니다.

### Telegram 명령

Telegram이 활성화되어 있으면 서버는 설정된 chat의 profile 명령도 polling합니다.

지원 명령:

- `/claude`
- `/codex`
- `/gemini`
- `/status`
- `/help`

profile 명령은 연결된 노드에 즉시 snapshot report를 요청하고, 잠깐 기다린 뒤 서버가 알고 있는 현재 redacted auth state와 usage metadata를 응답합니다.

명령은 설정된 Telegram chat에서만 허용됩니다.

- `default_chat_id`
- `routes[].chat_id`

다른 chat의 명령은 무시됩니다. 알 수 없는 profile 명령은 사용 가능한 profile 이름과 함께 에러 메시지를 응답합니다.

## systemd 등록

interactive `setup`은 다음 경로에 systemd unit을 생성할 수 있습니다.

- `./deploy/server/systemd/`
- `./deploy/client/systemd/`

서버 설치 예시:

```bash
sudo cp ./deploy/server/systemd/pangaea-server.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now pangaea-server.service
```

클라이언트 설치 예시:

```bash
sudo cp ./deploy/client/systemd/pangaea-client-<node>.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now pangaea-client-<node>.service
```

주의:

- 생성된 unit은 wizard에서 입력한 user로 실행됩니다
- Telegram 또는 `jwt.token_env`를 쓰면 unit이 생성된 env file을 참조합니다
- `/etc/systemd/system`에 복사하기 전에 unit 파일을 직접 수정해도 됩니다

## Kubernetes / Ingress 메모

권장 topology:

- `mtls`: TLS passthrough로 backend가 원래 client cert를 그대로 보게 한다
- `jwt`: public TLS는 ingress에서 종료할 수 있지만, backend는 계속 TLS listener를 유지하고 ingress는 `wss://`로 재암호화하는 것을 권장한다

`jwt` + ingress 환경에서는:

- backend listener를 TLS로 유지
- ingress -> backend HTTPS 사용 권장
- 가능하면 ingress가 backend certificate를 검증
- proxy가 `Authorization`를 제거할 수 있으면 `allow_first_frame_fallback: true` 유지

## 운영 체크리스트

- 모든 머신이 이미 동일한 upstream 계정으로 로그인되어 있다
- `mtls`에서는 `node_id == client cert CN`
- `jwt`에서는 `node_id == JWT subject`
- backend CA cert가 모든 client에 배포되었다
- profile 이름과 provider format이 양쪽에서 맞는다
- Telegram bot token / chat ID는 서버에만 설정한다
- public TLS를 다른 곳에서 종료하더라도 backend는 계속 `wss://`로 유지한다
