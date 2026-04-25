# Gemini Auth File Notes

This document describes how this repository interprets Google Gemini CLI OAuth credentials, based on the current `pkg/formats/geminioauth` implementation.

## Default Paths

Gemini profiles are configured with a single `dir`, typically `~/.gemini`.

- Primary auth file: `~/.gemini/oauth_creds.json`
- Default watched files:
  - `~/.gemini/oauth_creds.json`

Gemini account identity is derived from the auth file itself, so no extra metadata file is needed by default.

## On-Disk Auth Shape

The file follows the general `google-auth-library` credentials shape. Required fields:

- `access_token`
- `refresh_token`

Typical structure:

```json
{
  "access_token": "ya29....",
  "refresh_token": "1//0....",
  "scope": "openid email profile https://www.googleapis.com/auth/cloud-platform",
  "token_type": "Bearer",
  "id_token": "eyJ...",
  "expiry_date": 1893456000000
}
```

Implementation details:

- UTF-8 BOM is accepted.
- `scope` is split on whitespace into scopes.
- `expiry_date` is interpreted as Unix milliseconds.
- Fingerprint is `sha256(raw file bytes)`.
- Snapshot identity is the first 16 hex chars of `sha256(access_token)`.

## What Is Read From the File

`oauth_creds.json` directly provides:

- access token
- refresh token
- scope
- token type
- id token
- expiry date

The implementation also decodes the `id_token` for account identity:

- `sub`
- `email`

## Where Account Identity Comes From

Gemini account identity is derived from the `id_token`.

Priority:

1. `id_token.sub`
2. `id_token.email`

So the stable opaque Google subject ID is preferred, with email as a fallback.

## Validation and Freshness

Supported comparison strategy:

- `expiry_date_max`

Validation is local-only:

- missing `expiry_date` => `expired`
- `expiry_date` within 5 minutes of now => `expired`
- otherwise => `ok`

The 5-minute rule mirrors the eager refresh threshold used by the underlying Google auth library.

## How Validity / Tier Data Is Queried

The current Gemini implementation does not expose a numeric usage counter. Instead, it performs a lightweight validity and tier probe.

Request:

- method: `POST`
- URL: `https://cloudcode-pa.googleapis.com/v1internal:loadCodeAssist`
- headers:
  - `Authorization: Bearer <access_token>`
  - `Content-Type: application/json`
  - `Accept: application/json`

Runtime override:

- `GEMINI_LOADCODEASSIST_URL`

Request body:

```json
{
  "metadata": {
    "ideType": "IDE_UNSPECIFIED",
    "platform": "PLATFORM_UNSPECIFIED",
    "pluginType": "GEMINI"
  },
  "mode": "HEALTH_CHECK"
}
```

Relevant response shape:

```json
{
  "currentTier": {
    "id": "standard-tier",
    "name": "Gemini Standard",
    "hasOnboardedPreviously": true
  },
  "paidTier": {
    "id": "standard-tier",
    "name": "Gemini Standard"
  },
  "cloudaicompanionProject": "projects/..."
}
```

Current `UsageReport` mapping:

- `PlanTier`: `currentTier.id`
- `Notes`:
  - `tier: <currentTier.name>`
  - `paid-tier: <paidTier.id>` when different
  - `project: <cloudaicompanionProject>`

Important limitation:

- this implementation does not currently expose a numeric remaining quota
- richer quota endpoints may exist, but they are not used by the current notifier because they are noisier than the current summary model

## Redaction-Safe Summary

The redacted summary may include:

- `Identity`
- `FingerprintShort`
- `TokenTail4`
- `ExpiresAt`
- `Scopes`
- `Extra.token_type`

It never includes:

- `access_token`
- `refresh_token`
- `id_token`

## Operational Notes

- If `id_token` is absent or malformed, account partitioning may fall back to the shared bucket.
- A token inside the 5-minute eager-refresh window is treated as not worth propagating.
- Gemini notifications currently emphasize validity, tier, and assigned project rather than a remaining usage number.
