# Deployment Guide

This guide covers practical deployment for `pangaeactl` in both supported auth modes:

- `mtls`
- `jwt`

It also covers the interactive `setup` command, Telegram notifications, and systemd service registration.

## Scope and Preconditions

`pangaeactl` is a credential synchronization tool, not an account provisioning tool.

Use it only when all participating machines are already logged in to the same upstream account. In practice that means:

- the same Claude account on two or more PCs
- the same Codex / ChatGPT account on two or more PCs
- the same Gemini account on two or more PCs

Do not use it to share tokens across different human accounts.

## Supported CLI Profiles

The current repository supports these local auth formats:

- Claude CLI
- Codex CLI
- Gemini CLI

Format-specific file details live in:

- [Claude notes](./claude.md)
- [Codex notes](./codex.md)
- [Gemini notes](./gemini.md)

## Generated File Layout

The interactive bootstrap commands write into an output directory:

- server default: `./deploy/server`
- client default: `./deploy/client`

Typical server output:

- `pangaea-server.yaml`
- `profiles.yaml`
- `pki/ca.crt`
- `pki/ca.key`
- `pki/server/server.crt`
- `pki/server/server.key`
- `issued-clients/<node>/...` for `mtls`
- `jwt.secret` and `issued-jwt/<node>.token` for `jwt`
- `systemd/*.service` when requested

Typical client output:

- `pangaea-client.yaml`
- `pangaea-client.env` when JWT token delivery uses an environment variable
- `systemd/*.service` when requested

## mTLS Deployment

### Fast Path: Interactive Setup

Bootstrap the server:

```bash
pangaeactl setup server --out ./deploy/server
```

The wizard will ask for:

- `auth_mode`
- listen address
- TLS server host / SAN
- optional extra SANs
- initial client node IDs
- one or more profiles
- whether to issue initial client certificates
- optional Telegram notifier settings
- optional systemd unit generation

For `mtls`, the important server-side outputs are:

- root CA certificate and key
- server certificate and key
- one client certificate bundle per node ID, when requested

Bootstrap a client:

```bash
pangaeactl setup client --out ./deploy/client
```

For `mtls`, point the client wizard at:

- the server URL, for example `wss://hub.example.com:8443`
- the node ID
- local auth directories such as `~/.claude`, `~/.codex`, `~/.gemini`
- the copied CA cert / client cert / client key paths

Then run:

```bash
pangaeactl serve -c ./deploy/server/pangaea-server.yaml
pangaeactl connect -c ./deploy/client/pangaea-client.yaml
```

### Reverse mTLS Deployment: Server Can Reach Client, But Client Cannot Reach Server

In some networks the direct `client -> server` path is blocked while the server host can still open outbound connections to the client node. For that topology use:

- `pangaeactl reverse-client` on the client node
- `pangaeactl reverse-connect` as a separate bridge process on the server host

The existing `pangaeactl serve` process stays unchanged. The reverse bridge is a second process that attaches to the local unix socket status server and proxies one WebSocket stream per profile.

Important points:

- the client still keeps its normal `pangaea-client.yaml` profile bindings
- the client adds a `reverse:` listener block
- the server keeps normal `profiles.yaml`, but adds `reverse_targets` for the nodes that should be reached this way
- the reverse tunnel itself uses mTLS between the server-host bridge and the reverse-client listener
- `server.self_node.enabled` must be turned on, because the reverse bridge uses the server's self-node client certificate to authenticate to the reverse listener

Client-side example:

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

The client runs:

```bash
pangaeactl reverse-client -c ./deploy/client/pangaea-client.yaml
```

Server-side `profiles.yaml` example:

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
        url: "wss://laptop-a.example.net:9443"
```

Server-side `pangaea-server.yaml` must include self-node credentials:

```yaml
listen: "0.0.0.0:8443"
auth_mode: "mtls"
profiles_file: "/abs/path/deploy/server/profiles.yaml"
self_node:
  enabled: true
  node_id: "hub-bridge"
  client_cert: "/abs/path/deploy/server/issued-clients/hub-bridge/client.crt"
  client_key: "/abs/path/deploy/server/issued-clients/hub-bridge/client.key"
pki:
  ca_cert: "/abs/path/deploy/server/pki/ca.crt"
  server_cert: "/abs/path/deploy/server/pki/server/server.crt"
  server_key: "/abs/path/deploy/server/pki/server/server.key"
```

Run the main server as usual:

```bash
pangaeactl serve -c ./deploy/server/pangaea-server.yaml
```

Then run the separate reverse bridge on the same host:

```bash
pangaeactl reverse-connect \
  -c ./deploy/server/pangaea-server.yaml \
  --socket /tmp/pangaea.sock
```

Recommended systemd split:

- `pangaea-server.service` for `serve`
- `pangaea-reverse-connect.service` for `reverse-connect`

This keeps reverse dialing and reconnect churn isolated from the main server process.

### Manual Server Setup

Create a CA:

```bash
pangaeactl ca init \
  --out ./deploy/server/pki \
  --cn "pangaeactl Root CA" \
  --years 10
```

Issue the backend server certificate:

```bash
pangaeactl ca issue-server \
  --ca ./deploy/server/pki \
  --out ./deploy/server/pki/server \
  --cn hub.example.com \
  --san DNS:hub.example.com,IP:10.0.0.10 \
  --years 1
```

Verify it:

```bash
pangaeactl ca verify-server \
  --ca ./deploy/server/pki \
  --cert ./deploy/server/pki/server/server.crt \
  --server-name hub.example.com
```

Issue one client certificate per node. The CN must equal `node_id`:

```bash
pangaeactl ca issue-client \
  --ca ./deploy/server/pki \
  --out ./deploy/server/issued-clients/laptop-a \
  --cn laptop-a \
  --years 1
```

Verify it:

```bash
pangaeactl ca verify-client \
  --ca ./deploy/server/pki \
  --cert ./deploy/server/issued-clients/laptop-a/client.crt \
  --cn laptop-a
```

Write `pangaea-server.yaml`:

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

Write `profiles.yaml`:

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

Start the server:

```bash
pangaeactl serve -c ./deploy/server/pangaea-server.yaml
```

### Manual Client Setup

Copy these files from the server bundle to the client machine:

- `ca.crt`
- `client.crt`
- `client.key`

Create `pangaea-client.yaml`:

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

Start the client:

```bash
pangaeactl connect -c ./deploy/client/pangaea-client.yaml
```

## JWT Deployment

`jwt` mode is intended for deployments where TLS is terminated before the backend, such as an ingress controller.

Important points:

- the backend server still runs `wss://`
- TLS on the backend is still required
- JWT replaces client identity authentication, not transport encryption
- backend plain HTTP is allowed by policy, but not recommended
- for Kubernetes ingress, HTTPS re-encryption from ingress to backend is strongly recommended

The client first tries `Authorization: Bearer <jwt>`. If that header is unavailable or stripped, the server can require `auth.jwt` as the first WebSocket frame. Use `jwt.send_via: auto` unless you know you need `header` or `first_frame`.

### Fast Path: Interactive Setup

Bootstrap the server:

```bash
pangaeactl setup server --out ./deploy/server
```

Choose `jwt` when asked for auth mode.

The server wizard will generate:

- root CA certificate and key
- backend server certificate and key
- `jwt.secret`
- optional initial JWT tokens for listed node IDs
- `pangaea-server.yaml`
- `profiles.yaml`

Bootstrap a client:

```bash
pangaeactl setup client --out ./deploy/client
```

Choose `jwt` when asked for auth mode.

The client wizard will ask whether the token arrives via:

- token file
- environment variable

For most deployments:

- use `token_file` when you distribute per-node token files out of band
- use `token_env` when the token comes from a secret manager or systemd env file
- keep `send_via: auto`

### Manual Server Setup

Create the CA and backend server certificate exactly as in the `mtls` section.

Then create the JWT secret:

```bash
pangaeactl jwt init --out ./deploy/server/jwt.secret
```

Issue a per-node token:

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

Verify it:

```bash
pangaeactl jwt verify \
  --secret-key ./deploy/server/jwt.secret \
  --token @./deploy/server/issued-jwt/laptop-a.token \
  --issuer pangaea \
  --audience pangaea
```

Write `pangaea-server.yaml`:

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

Start the server:

```bash
pangaeactl serve -c ./deploy/server/pangaea-server.yaml
```

### Manual Client Setup

Copy the backend CA cert to the client so it can verify the backend TLS endpoint.

Create `pangaea-client.yaml` with a token file:

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

Or with an environment variable:

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

Then start the client:

```bash
pangaeactl connect -c ./deploy/client/pangaea-client.yaml
```

## Telegram Notifications

Telegram is configured on the server side only.

### Bot Setup

1. Create a bot with `@BotFather`.
2. Save the bot token in an environment variable or env file.
3. Send at least one message to the bot, or add the bot to the target group/channel.
4. Fetch the chat ID from `getUpdates`.

Example:

```bash
export PANGAEA_TELEGRAM_BOT_TOKEN='123456:abc...'
curl "https://api.telegram.org/bot${PANGAEA_TELEGRAM_BOT_TOKEN}/getUpdates"
```

Look for the target `chat.id` in the JSON response.

### Server Config

```yaml
notifier:
  telegram:
    enabled: true
    bot_token_env: "PANGAEA_TELEGRAM_BOT_TOKEN"
    default_chat_id: "-1001234567890"
    disable_notification: false
```

Per-profile or per-account routing is also supported:

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

If you used `pangaeactl setup server` and enabled Telegram, the wizard created a placeholder env file such as:

```bash
./deploy/server/pangaea-server.env
```

Fill in the bot token before starting the service.

## systemd Service Registration

The interactive `setup` commands can generate systemd unit files under:

- `./deploy/server/systemd/`
- `./deploy/client/systemd/`

Typical installation flow:

```bash
sudo cp ./deploy/server/systemd/pangaea-server.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now pangaea-server.service
```

And for a client:

```bash
sudo cp ./deploy/client/systemd/pangaea-client-<node>.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now pangaea-client-<node>.service
```

Notes:

- generated units run as the user you entered in the setup wizard
- when Telegram or `jwt.token_env` is used, the unit points at the generated env file
- you can edit the generated unit before copying it into `/etc/systemd/system`

## Kubernetes / Ingress Notes

Recommended topology:

- `mtls`: use TLS passthrough so the backend sees the original client certificate
- `jwt`: ingress may terminate public TLS, but the backend should still listen with TLS and the ingress should re-encrypt to `wss://`

For `jwt` mode on Kubernetes ingress:

- keep the backend listener on TLS
- prefer ingress-to-backend HTTPS
- if possible, validate the backend certificate from the ingress
- keep `allow_first_frame_fallback: true` when proxies may strip `Authorization`

## Operational Checklist

- every machine already has a valid upstream login for the same human account
- `node_id` matches the mTLS client cert CN in `mtls` mode
- `node_id` matches the JWT subject in `jwt` mode
- the backend CA certificate is copied to every client
- profile names and provider formats match on both ends
- Telegram bot token and chat ID are configured only on the server
- the backend remains `wss://` even when public TLS is terminated elsewhere
