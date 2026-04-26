# Claude Auth File Notes

This document describes how this repository interprets Claude Code credentials, based on the current `pkg/formats/claudecreds` implementation.

## Default Paths

Claude profiles are configured with a single `dir`, typically `~/.claude`.

- Primary credentials file: `~/.claude/.credentials.json`
- Default watched files:
  - `~/.claude/.credentials.json`
  - `~/.claude.json`
  - `~/.claude/.config.json`

The project does not watch the whole directory. It watches only the configured files, or these defaults when `watch_files` is omitted.

## On-Disk Credential Shape

The parser reads the `claudeAiOauth` object. These fields are required:

- `claudeAiOauth.accessToken`
- `claudeAiOauth.refreshToken`
- `claudeAiOauth.expiresAt`

Typical structure:

```json
{
  "claudeAiOauth": {
    "accessToken": "sk-ant-oat01-...",
    "refreshToken": "sk-ant-ort01-...",
    "expiresAt": 1893456000000,
    "scopes": ["user:inference", "user:profile"],
    "subscriptionType": "max",
    "rateLimitTier": "default_claude_max_20x"
  }
}
```

Implementation details:

- UTF-8 BOM is accepted.
- Legacy `access_token` is accepted as a fallback for `accessToken`.
- `expiresAt` is interpreted as Unix milliseconds.
- Snapshot fingerprint is `sha256(raw file bytes)`.
- Snapshot identity is the first 16 hex chars of `sha256(accessToken)`.

## What Is Read From the Credentials File

The primary file directly provides:

- access token
- refresh token
- expiry time
- scopes
- subscription type
- rate limit tier

It does not provide a stable account identifier, so account separation depends on a side metadata file.

## Where Account Identity Comes From

Account identity is resolved by `pkg/formats/claudecreds/account.go`. The lookup order is:

1. `AccountMetaPath` from client config, if set
2. If `path` points to a Claude config directory, try adjacent `~/.claude.json` and legacy `~/.claude/.config.json`
3. If `path` points to `.credentials.json`, derive sibling metadata paths from it
4. `${CLAUDE_CONFIG_DIR}/.claude.json` when `CLAUDE_CONFIG_DIR` is set
5. `$HOME/.claude.json`
6. Legacy fallback `$HOME/.claude/.config.json`

Relevant metadata shape:

```json
{
  "oauthAccount": {
    "accountUuid": "acc_...",
    "emailAddress": "user@example.com",
    "organizationUuid": "org_..."
  }
}
```

Identity priority:

1. `oauthAccount.accountUuid`
2. `oauthAccount.emailAddress`
3. empty string if neither is present

For Claude, account isolation is driven by metadata, not by the token file itself.

## Validation and Freshness

Supported comparison strategy:

- `expires_at_max`

Meaning:

- the snapshot with the later `ExpiresAt` wins

Validation rules:

- missing or expired `expiresAt` => `expired`
- with `live_check=false`, only local clock-based validation runs
- with `live_check=true`, the implementation performs one read-only HTTP GET

Live check endpoint:

- `GET https://api.anthropic.com/api/oauth/profile`
- `Authorization: Bearer <accessToken>`
- `Accept: application/json`

Runtime override:

- `ANTHROPIC_OAUTH_PROFILE_URL`

## Usage / Plan Data

Claude usage probing now mirrors Claude CLI `/usage`.

Request:

- method: `GET`
- URL: `https://api.anthropic.com/api/oauth/usage`
- headers:
  - `Authorization: Bearer <accessToken>`
  - `Accept: application/json`
  - `Content-Type: application/json`
  - `anthropic-beta: oauth-2025-04-20`

Runtime override:

- `ANTHROPIC_OAUTH_USAGE_URL`

Relevant response shape:

```json
{
  "five_hour": { "utilization": 7, "resets_at": "2026-04-26T03:30:00Z" },
  "seven_day": { "utilization": 31, "resets_at": "2026-04-29T07:00:00Z" },
  "seven_day_sonnet": { "utilization": 0, "resets_at": "2026-04-29T07:00:00Z" },
  "extra_usage": {
    "is_enabled": true,
    "monthly_limit": 5000,
    "used_credits": 258,
    "utilization": 5.16
  }
}
```

Current `UsageReport` mapping:

- `PlanTier`: `claudeAiOauth.subscriptionType`
- `Notes`:
  - `rate-limit-tier: <claudeAiOauth.rateLimitTier>` when present
  - `extra usage: not enabled` for `pro` / `max` plans with overage disabled
  - `extra usage: unlimited` for `pro` / `max` plans with unlimited extra usage
  - `extra usage spend: $x / $y` when the API reports monthly extra-usage spend
- `Windows`:
  - `Current session` from `five_hour`
  - `Current week (all models)` from `seven_day`
  - `Current week (Sonnet only)` from `seven_day_sonnet` only for `max`, `team`, or unknown subscription type
  - `Extra usage` only for `pro` / `max` when the API exposes utilization

Subscription-dependent rendering follows Claude CLI:

- `max`, `team`, or unknown subscription: show the separate Sonnet weekly window
- `pro`, `enterprise`: hide the Sonnet-only window because it is redundant with the weekly limit
- `pro`, `max`: show `extra_usage` state when returned by the API
- `team`, `enterprise`: ignore `extra_usage` in notifications, matching the CLI Usage tab

## Redaction-Safe Summary

The redacted summary includes:

- `Identity`
- `Subscription`
- `FingerprintShort`
- `TokenTail4`
- `ExpiresAt`
- `Scopes`

The summary never includes:

- `accessToken`
- `refreshToken`

## Operational Notes

- This project never refreshes Claude tokens. It only reads and propagates what Claude Code already wrote to disk.
- Accurate account partitioning requires `~/.claude.json` or an equivalent metadata file.
- If you override `watch_files`, removing `~/.claude.json` can cause account metadata changes to be missed.
- Notifications for Claude may include validity and plan metadata without a concrete remaining quota value.
