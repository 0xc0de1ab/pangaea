# Codex Auth File Notes

This document describes how this repository interprets OpenAI Codex CLI authentication files, based on the current `pkg/formats/codexauth` implementation.

## Default Paths

Codex profiles are configured with a single `dir`, typically `~/.codex`.

- Primary auth file: `~/.codex/auth.json`
- Default watched files:
  - `~/.codex/auth.json`

Codex account identity and usage probe inputs are self-contained in `auth.json`, so no extra metadata file is required by default.

## On-Disk Auth Shape

This implementation treats OAuth-based login state as the shareable unit. The required fields are:

- `tokens.access_token`
- `tokens.refresh_token`

Typical structure:

```json
{
  "auth_mode": "chatgpt",
  "OPENAI_API_KEY": null,
  "last_refresh": "2026-04-25T12:34:56Z",
  "tokens": {
    "id_token": "eyJ...",
    "access_token": "eyJ...",
    "refresh_token": "rt-...",
    "account_id": "acc_..."
  },
  "agent_identity": {
    "...": "..."
  }
}
```

Implementation details:

- UTF-8 BOM is accepted.
- API-key-only state is not considered shareable.
- `access_token` JWT `exp` is parsed as the primary expiry signal.
- `id_token` is parsed for ChatGPT identity claims.
- Fingerprint is `sha256(raw file bytes)`.
- Snapshot identity is the first 16 hex chars of `sha256(access_token)`.

## What Is Read From the File

`auth.json` provides:

- `auth_mode`
- `access_token`
- `refresh_token`
- `id_token`
- `tokens.account_id`
- `last_refresh`
- access token `exp`
- `id_token` email
- `chatgpt_user_id`
- `chatgpt_account_id`

Unlike Claude, Codex does not require a separate account metadata file.

## Where Account Identity Comes From

Account identity is derived entirely from `auth.json`. Priority:

1. `id_token` claim `https://api.openai.com/auth.chatgpt_user_id`
2. `tokens.account_id`
3. `id_token` email

Related claims:

- `https://api.openai.com/auth.chatgpt_user_id`
- `https://api.openai.com/auth.chatgpt_account_id`
- `https://api.openai.com/profile.email`

In practice:

- `chatgpt_user_id` is the preferred stable partition key
- `chatgpt_account_id` is required for usage probing
- email is a fallback only

## Validation and Freshness

Supported comparison strategy:

- `jwt_exp_max`

Meaning:

- the snapshot with the later access-token JWT `exp` wins
- `last_refresh` is used as a tie-breaker

Validation is local-only:

- unreadable `access_token` JWT `exp` => `scope_warn`
- expired `exp` => `expired`
- `last_refresh` older than 8 days => `expired`
- otherwise => `ok`

The 8-day rule mirrors Codex's proactive refresh interval. A token can still be JWT-valid but too stale to be worth propagating.

## How Usage Is Queried

Codex is the most complete of the three formats in terms of numeric usage reporting. The implementation probes:

- method: `GET`
- URL: `https://chatgpt.com/backend-api/wham/usage`
- headers:
  - `Authorization: Bearer <access_token>`
  - `ChatGPT-Account-ID: <chatgpt_account_id>`
  - `Accept: application/json`
  - `User-Agent: pangaeactl/notifier`

Runtime override:

- `CHATGPT_USAGE_URL`

Important:

- `ChatGPT-Account-ID` usually comes from the `chatgpt_account_id` claim in `id_token`
- if that claim is absent, the implementation falls back to `tokens.account_id`
- if both are absent, the probe fails

Relevant response shape:

```json
{
  "plan_type": "gpt_pro",
  "rate_limit": {
    "allowed": true,
    "limit_reached": false,
    "primary_window": {
      "used_percent": 12.5,
      "limit_window_seconds": 10800,
      "reset_after_seconds": 1800,
      "reset_at": "2026-04-26T03:00:00Z"
    },
    "secondary_window": {
      "used_percent": 55.0,
      "limit_window_seconds": 86400,
      "reset_at": "2026-04-26T18:00:00Z"
    }
  }
}
```

Current `UsageReport` mapping:

- `PlanTier`: `plan_type`
- `RemainingPct`: `100 - primary_window.used_percent`
- `Unit`: a human window label derived from `limit_window_seconds`
- `ResetAt`: `primary_window.reset_at`
- `Notes`:
  - secondary window utilization and reset time
  - limit reached note when applicable

## Redaction-Safe Summary

The redacted summary may include:

- `Identity`
- `FingerprintShort`
- `TokenTail4`
- `ExpiresAt`
- `Extra.auth_mode`
- `Extra.email`
- `Extra.account_id`
- `Extra.last_refresh`

It never includes:

- `access_token`
- `refresh_token`
- `id_token`
- `OPENAI_API_KEY`

## Operational Notes

- API-key-only Codex login is intentionally excluded from synchronization.
- Usage probing requires `chatgpt_account_id` or `tokens.account_id`.
- A JWT can still be valid while being considered stale due to `last_refresh > 8 days`.
- Codex account partitioning is generally strong because the file is self-contained.
