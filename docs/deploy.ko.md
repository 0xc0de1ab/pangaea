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
- backend plain HTTP는 정책상 막지는 않지만 권장하지 않습니다
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
