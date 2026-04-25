# Codex 인증 파일 메모

이 문서는 현재 `pkg/formats/codexauth` 구현을 기준으로 OpenAI Codex CLI 인증 파일을 어떻게 해석하는지 설명한다.

## 기본 경로

Codex 프로필은 보통 `~/.codex` 같은 `dir` 하나로 설정한다.

- 주 인증 파일: `~/.codex/auth.json`
- 기본 watch 대상:
  - `~/.codex/auth.json`

Codex는 계정 식별과 usage probe에 필요한 정보를 `auth.json` 하나에 담고 있어서 기본적으로 별도 메타 파일이 필요 없다.

## 인증 파일 형식

이 구현은 OAuth 기반 로그인 상태만 공유 대상으로 본다. 필수값은 다음과 같다.

- `tokens.access_token`
- `tokens.refresh_token`

대표 구조:

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

구현 메모:

- UTF-8 BOM 허용
- API-key-only 상태는 공유 대상으로 취급하지 않음
- `access_token` JWT `exp`를 만료 판단에 사용
- `id_token`에서 ChatGPT 계정 관련 claims 추출
- fingerprint는 raw JSON 전체의 `sha256`
- identity는 `sha256(access_token)` 앞 16 hex

## 파일에서 읽는 정보

`auth.json`에서 읽는 값:

- `auth_mode`
- `access_token`
- `refresh_token`
- `id_token`
- `tokens.account_id`
- `last_refresh`
- access token의 `exp`
- `id_token`의 email
- `chatgpt_user_id`
- `chatgpt_account_id`

Codex는 Claude와 달리 별도 account metadata 파일이 필요 없다.

## 계정 정보 출처

계정 식별은 `auth.json`만으로 처리한다. 우선순위는 다음과 같다.

1. `id_token` claim `https://api.openai.com/auth.chatgpt_user_id`
2. `tokens.account_id`
3. `id_token` email

관련 claims:

- `https://api.openai.com/auth.chatgpt_user_id`
- `https://api.openai.com/auth.chatgpt_account_id`
- `https://api.openai.com/profile.email`

정리하면:

- `chatgpt_user_id`는 account partition용 preferred stable id
- `chatgpt_account_id`는 usage probe 헤더용
- email은 마지막 fallback

## validation / freshness

지원 비교 전략:

- `jwt_exp_max`

의미:

- access-token JWT의 `exp`가 더 늦은 snapshot이 더 새롭다
- 같으면 `last_refresh`로 tie-break

validation은 로컬 판정만 한다.

- `access_token` JWT `exp`를 읽지 못하면 `scope_warn`
- `exp`가 지났으면 `expired`
- `last_refresh`가 8일보다 오래됐으면 `expired`
- 나머지는 `ok`

8일 규칙은 Codex의 proactive refresh interval을 반영한 것이다.

## usage 요청 방식

Codex는 세 포맷 중 usage 숫자를 가장 직접적으로 가져오는 구현이다.

요청:

- method: `GET`
- URL: `https://chatgpt.com/backend-api/wham/usage`
- headers:
  - `Authorization: Bearer <access_token>`
  - `ChatGPT-Account-ID: <chatgpt_account_id>`
  - `Accept: application/json`
  - `User-Agent: pangaeactl/notifier`

런타임 override:

- `CHATGPT_USAGE_URL`

중요:

- `ChatGPT-Account-ID`는 보통 `id_token`의 `chatgpt_account_id` claim에서 가져온다
- 없으면 `tokens.account_id`로 fallback
- 둘 다 없으면 probe 실패

관련 응답 조각:

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

현재 `UsageReport` 매핑:

- `PlanTier`: `plan_type`
- `RemainingPct`: `100 - primary_window.used_percent`
- `Unit`: `limit_window_seconds` 기반 human label
- `ResetAt`: `primary_window.reset_at`
- `Notes`:
  - secondary window 사용률/리셋 시각
  - limit reached note

## redaction-safe summary

redacted summary에 포함될 수 있는 값:

- `Identity`
- `FingerprintShort`
- `TokenTail4`
- `ExpiresAt`
- `Extra.auth_mode`
- `Extra.email`
- `Extra.account_id`
- `Extra.last_refresh`

포함되지 않는 값:

- `access_token`
- `refresh_token`
- `id_token`
- `OPENAI_API_KEY`

## 운영 메모

- API-key-only Codex login은 동기화 대상에서 제외된다.
- usage probe는 `chatgpt_account_id` 또는 `tokens.account_id`가 있어야 한다.
- JWT가 아직 살아 있어도 `last_refresh`가 8일을 넘으면 stale로 간주한다.
- Codex는 파일 하나에 토큰과 계정 메타가 함께 있어서 account partition 신뢰도가 높다.
