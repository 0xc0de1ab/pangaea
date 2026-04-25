# Claude 인증 파일 메모

이 문서는 현재 `pkg/formats/claudecreds` 구현을 기준으로 Claude Code 인증 파일을 어떻게 해석하는지 설명한다.

## 기본 경로

Claude 프로필은 보통 `~/.claude` 같은 `dir` 하나로 설정한다.

- 주 인증 파일: `~/.claude/.credentials.json`
- 기본 watch 대상:
  - `~/.claude/.credentials.json`
  - `~/.claude.json`
  - `~/.claude/.config.json`

이 프로젝트는 디렉토리 전체를 감시하지 않고 지정된 파일만 감시한다.

## 인증 파일 형식

파서는 `claudeAiOauth` 객체를 읽는다. 필수값은 다음과 같다.

- `claudeAiOauth.accessToken`
- `claudeAiOauth.refreshToken`
- `claudeAiOauth.expiresAt`

대표 구조:

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

구현 메모:

- UTF-8 BOM 허용
- 구버전 호환을 위해 `access_token`도 fallback 허용
- `expiresAt`은 Unix milliseconds
- fingerprint는 raw JSON 전체의 `sha256`
- identity는 `sha256(accessToken)` 앞 16 hex

## 파일에서 읽는 정보

`.credentials.json`에서 직접 얻는 값:

- access token
- refresh token
- expiry
- scopes
- subscription type
- rate limit tier

이 파일만으로는 stable account id가 없어서, 계정 분리는 별도 메타 파일에 의존한다.

## 계정 정보 출처

계정 식별은 `pkg/formats/claudecreds/account.go`가 처리한다. 우선순위는 다음과 같다.

1. client config의 `AccountMetaPath`
2. `path`가 Claude config dir이면 인접한 `~/.claude.json`, `~/.claude/.config.json`
3. `path`가 `.credentials.json`이면 그 기준으로 메타 파일 경로 유도
4. `${CLAUDE_CONFIG_DIR}/.claude.json`
5. `$HOME/.claude.json`
6. legacy fallback `$HOME/.claude/.config.json`

메타 구조:

```json
{
  "oauthAccount": {
    "accountUuid": "acc_...",
    "emailAddress": "user@example.com",
    "organizationUuid": "org_..."
  }
}
```

식별 우선순위:

1. `oauthAccount.accountUuid`
2. `oauthAccount.emailAddress`
3. 둘 다 없으면 빈 문자열

즉 Claude는 토큰 파일이 아니라 메타 파일이 계정 분리의 핵심이다.

## validation / freshness

지원 비교 전략:

- `expires_at_max`

의미:

- `ExpiresAt`이 더 늦은 snapshot이 더 새롭다

validation 규칙:

- `expiresAt`이 없거나 만료면 `expired`
- `live_check=false`면 로컬 시계 기준 판정만 수행
- `live_check=true`면 읽기 전용 GET 호출 추가

live check endpoint:

- `GET https://api.anthropic.com/api/oauth/profile`
- `Authorization: Bearer <accessToken>`
- `Accept: application/json`

런타임 override:

- `ANTHROPIC_OAUTH_PROFILE_URL`

## usage / plan / organization 정보

현재 Claude probe는 직접적인 usage 숫자 `Used`, `Limit`를 반환하지 않는다. 대신 OAuth profile endpoint에서 plan과 organization 정보를 읽는다.

요청:

- method: `GET`
- URL: `https://api.anthropic.com/api/oauth/profile`
- headers:
  - `Authorization: Bearer <accessToken>`
  - `Accept: application/json`

관련 응답 조각:

```json
{
  "account": {
    "uuid": "acc_...",
    "email_address": "user@example.com"
  },
  "organization": {
    "uuid": "org_...",
    "name": "Acme Inc",
    "organization_type": "claude_max",
    "rate_limit_tier": "default_claude_max_20x"
  }
}
```

현재 `UsageReport` 매핑:

- `PlanTier`: `organization.organization_type`
- `Notes`:
  - `org: <organization.name>`
  - `rate-limit-tier: <organization.rate_limit_tier>`
  - `email: <account.email_address>`

제한사항:

- 현재 구현은 Anthropic의 실시간 usage counter를 직접 노출하지 않는다
- 그래서 Claude 알림은 plan, org, validity 위주가 될 수 있고, 남은 quota 숫자가 항상 나오지는 않는다

## redaction-safe summary

redacted summary에 포함되는 값:

- `Identity`
- `Subscription`
- `FingerprintShort`
- `TokenTail4`
- `ExpiresAt`
- `Scopes`

포함되지 않는 값:

- `accessToken`
- `refreshToken`

## 운영 메모

- 이 프로젝트는 Claude token refresh를 하지 않는다. Claude Code가 디스크에 쓴 결과만 읽고 전파한다.
- 정확한 account partition을 위해서는 `~/.claude.json` 또는 동등한 메타 파일이 필요하다.
- `watch_files`를 override할 때 `~/.claude.json`을 빼면 계정 메타 변경을 놓칠 수 있다.
- Claude 알림은 validity와 plan 정보는 잘 나오지만, 수치형 남은 quota는 현재 구현상 없을 수 있다.
