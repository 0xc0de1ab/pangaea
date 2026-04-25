# Gemini 인증 파일 메모

이 문서는 현재 `pkg/formats/geminioauth` 구현을 기준으로 Google Gemini CLI OAuth 인증 파일을 어떻게 해석하는지 설명한다.

## 기본 경로

Gemini 프로필은 보통 `~/.gemini` 같은 `dir` 하나로 설정한다.

- 주 인증 파일: `~/.gemini/oauth_creds.json`
- 기본 watch 대상:
  - `~/.gemini/oauth_creds.json`

Gemini는 auth 파일 자체에서 계정 정보를 읽기 때문에 기본적으로 별도 메타 파일이 필요 없다.

## 인증 파일 형식

파일은 대체로 `google-auth-library` credentials 구조를 따른다. 필수값은 다음과 같다.

- `access_token`
- `refresh_token`

대표 구조:

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

구현 메모:

- UTF-8 BOM 허용
- `scope`는 공백 기준 split
- `expiry_date`는 Unix milliseconds
- fingerprint는 raw JSON 전체의 `sha256`
- identity는 `sha256(access_token)` 앞 16 hex

## 파일에서 읽는 정보

`oauth_creds.json`에서 직접 읽는 값:

- access token
- refresh token
- scope
- token type
- id token
- expiry date

추가로 `id_token`에서 계정 정보도 읽는다.

- `sub`
- `email`

## 계정 정보 출처

Gemini의 계정 식별은 `id_token`에서 가져온다.

우선순위:

1. `id_token.sub`
2. `id_token.email`

즉 stable opaque user id인 `sub`를 우선 쓰고, 없으면 이메일로 fallback한다.

## validation / freshness

지원 비교 전략:

- `expiry_date_max`

validation은 로컬 판정만 한다.

- `expiry_date`가 없으면 `expired`
- `expiry_date`가 현재 시각 기준 5분 이내면 `expired`
- 그 외는 `ok`

5분 규칙은 Google auth library의 eager refresh threshold를 반영한 것이다.

## validity / tier probe 방식

현재 Gemini 구현은 숫자형 usage counter를 가져오지 않는다. 대신 lightweight validity + tier probe를 수행한다.

요청:

- method: `POST`
- URL: `https://cloudcode-pa.googleapis.com/v1internal:loadCodeAssist`
- headers:
  - `Authorization: Bearer <access_token>`
  - `Content-Type: application/json`
  - `Accept: application/json`

런타임 override:

- `GEMINI_LOADCODEASSIST_URL`

요청 body:

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

관련 응답 조각:

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

현재 `UsageReport` 매핑:

- `PlanTier`: `currentTier.id`
- `Notes`:
  - `tier: <currentTier.name>`
  - `paid-tier: <paidTier.id>` if different
  - `project: <cloudaicompanionProject>`

제한사항:

- 현재 구현은 숫자형 remaining quota를 제공하지 않는다
- 더 자세한 quota endpoint를 쓸 여지는 있지만, 현 notifier 모델에는 너무 noisy해서 아직 쓰지 않는다

## redaction-safe summary

redacted summary에 포함될 수 있는 값:

- `Identity`
- `FingerprintShort`
- `TokenTail4`
- `ExpiresAt`
- `Scopes`
- `Extra.token_type`

포함되지 않는 값:

- `access_token`
- `refresh_token`
- `id_token`

## 운영 메모

- `id_token`이 없거나 깨져 있으면 account partition이 shared bucket으로 약해질 수 있다.
- 5분 eager-refresh window 안에 들어온 토큰은 전파할 가치가 없다고 보고 탈락시킨다.
- Gemini 알림은 현재 usage 숫자보다는 validity, tier, project 정보 중심이다.
