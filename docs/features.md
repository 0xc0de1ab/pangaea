# Feature Overview

This document summarizes the features currently provided by the repository and records the remaining cleanup items after the original implementation plan was completed.

## Status Summary

As of 2026-04-29, the core product work is largely implemented, but not every supporting item is complete.

### Implemented

- Repository bootstrap, Go module layout, and package split
- Shared constants, sentinel errors, structured logging, and redaction
- Safe file IO primitives including atomic write, flock, backup, and rollback
- PKI helpers for CA creation, client/server certificate issuance, TLS config, and verification
- Config loading, path expansion, profile store reload, and SIGHUP-driven reload flow
- WebSocket transport, codec, and reconnect logic
- File watching with debounce/stable-window behavior
- Format framework and three format implementations:
  - Claude
  - Codex
  - Gemini
- Server-side mediation and client-side convergence
- `serve`, `connect`, `setup`, `ca`, `inspect`, `status`, `version`, and `jwt` CLI flows
- `--also-client` server-side self-client support
- `mtls` auth mode
- `jwt` auth mode with header-based auth and `auth.jwt` first-frame fallback
- Reverse connectivity for server-reachable-only networks via:
  - `reverse-client` on the client node
  - `reverse-connect` as a separate bridge process on the server host
  - direct reverse targets (`transport: direct`)
  - SSH-managed reverse targets (`transport: ssh`) using the operator's local `~/.ssh` configuration by default
- Notification sinks:
  - Telegram
  - Slack
  - Discord
  - Mattermost
  - ntfy
  - Teams
- Event-driven propagation notifications enriched with usage/validity metadata
- Telegram bot command polling for `/claude`, `/codex`, `/gemini`, `/status`, and `/help`
- Official-CLI refresh nudges for Claude, Codex, and Gemini when credentials are expired or near expiry and the provider CLI is available in `PATH`
- Periodic npm-based official CLI upgrades for npm-installed Claude, Codex, and Gemini CLIs, executed through `bash -lic` for nvm-managed environments
- Unit tests and end-to-end tests for major synchronization and auth scenarios
- GitHub Actions CI with test, coverage, and multi-target build jobs

### Partially Implemented or Diverged

- The codebase has evolved beyond the original implementation plan in several places:
  - JWT auth was added
  - Codex and Gemini formats were added
  - Rich notifier integrations were added
  - CLI gained JWT tooling and additional verification flows
- CI builds the requested main targets, but the workflow does not currently enforce `go test -race`
- An `integration` test target exists in the Makefile, but CI does not run a dedicated `-tags=integration` job

### Not Yet Present

- `docker-compose.yml` E2E fixture described in §F
- Consistent per-package `doc.go` or package-level README coverage for every package, as suggested in §G

## Product Features

## Secure Credential Synchronization

- Synchronizes auth state across nodes for supported CLI tools
- Propagates only validated, partitioned account state
- Prevents accidental cross-account overwrite by partitioning on per-format account identity
- Uses atomic local writes and rollback-aware apply behavior on the receiving node

## Authentication Modes

- `mtls` mode:
  - mutual TLS client authentication
  - profile ACL enforcement via client identity
- `jwt` mode:
  - JWT bearer validation at WebSocket upgrade time
  - fallback `auth.jwt` first-frame authentication when the `Authorization` header is unavailable
  - profile authorization via JWT claims

## Supported Formats

- Claude credentials JSON
  - token propagation
  - account metadata lookup
  - plan / organization probe
- Codex auth JSON
  - token propagation
  - account extraction from JWT claims
  - numeric usage probe
- Gemini OAuth credentials JSON
  - token propagation
  - account extraction from `id_token`
  - tier / validity probe

See also:

- [Claude notes](./claude.md)
- [Codex notes](./codex.md)
- [Gemini notes](./gemini.md)

## Server Capabilities

- Per-profile session hub
- Mediator-based truth selection
- Stale-only or broader propagation control
- Cooldown-aware truth change suppression
- Duplicate identity displacement
- Optional in-process self-clients via `--also-client`
- Unix socket status endpoint
- Local unix-socket attach endpoint for reverse bridges
- Profile reload on SIGHUP

## Client Capabilities

- Initial scan plus event-driven reporting
- Reconnect loop with backoff
- Local apply with lock, backup, verification, and ACK
- JWT header or first-frame auth behavior
- Format-aware account resolution
- Watcher-driven re-reporting when auxiliary metadata files change
- Near-expiry refresh nudges through the official provider CLI only; `pangaeactl` does not implement provider OAuth refresh itself
- Periodic official CLI upgrades for global npm installs only; non-npm installs are detected and left untouched
- Optional reverse-client listener for server-initiated tunnel topologies

## CLI Features

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

## Notification Features

- Periodic truth summaries
- Periodic summaries are sent no more frequently than once per hour and are skipped when the rendered digest has not changed
- Immediate propagation notifications when a new truth is pushed to peers and the usage/validity probe has useful metadata
- Session connect/disconnect events are currently suppressed at the notifier layer to avoid high-volume noise
- Telegram command replies for `/claude`, `/codex`, `/gemini`, `/status`, and `/help`
- Rich sink formatting per destination
- Included metadata may contain:
  - source node
  - target nodes
  - LLM/provider
  - account
  - validity window
  - usage or remaining quota, where supported
  - plan/tier/organization metadata

## Build and Release Features

- Multi-target Makefile-driven builds
- Release builds for Linux, macOS, and Windows
- Static Linux release output
- Version injection at build time
- CI runs E2E separately, measures coverage for library/internal packages, and generates a coverage badge

## Recommended Next Cleanup Items

- Add `docker-compose.yml` for local multi-node E2E bring-up
- Add `go test -race` to CI
- Add a dedicated CI job for `-tags=integration`
- Normalize documentation into English-first plus `.ko.md` counterparts across the whole docs tree
