package claudecreds

import (
	"context"
	"net/http"
	"time"

	"github.com/0xc0de1ab/pangaea/internal/common"
	"github.com/0xc0de1ab/pangaea/pkg/formats"
)

// liveCheckURL is the read-only endpoint we probe to determine whether the
// access token is still accepted by Anthropic. The path is documented in
// specs §9.3 and is reverse-engineered — variation in the response is
// expected, hence the conservative status mapping below.
//
// It is a var (not a const) so tests can redirect it to httptest.NewServer
// without spinning up a full HTTPS roundtripper.
var liveCheckURL = "https://api.anthropic.com/api/oauth/profile"

// liveCheck performs a single GET against the OAuth profile endpoint with the
// snapshot's access token. It maps:
//
//	200          -> ok
//	401          -> expired (also covers revoked; we do not try to
//	                 distinguish in v1, see specs §9.3)
//	403          -> scope_warn (token is alive but missing scopes)
//	5xx          -> unreachable
//	timeout/DNS  -> unreachable
//
// It NEVER follows a refresh flow and NEVER POSTs.
func liveCheck(ctx context.Context, snap *snapshot, opts formats.ValidateOpts, checkedAt time.Time) formats.ValidationResult {
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = common.LiveCheckDefaultTimeout
	}

	client := opts.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: timeout}
	}

	reqCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, liveCheckURL, nil)
	if err != nil {
		return formats.ValidationResult{
			Status:    formats.StatusUnreachable,
			Detail:    "build live-check request: " + err.Error(),
			CheckedAt: checkedAt,
		}
	}
	req.Header.Set("Authorization", "Bearer "+snap.accessToken)
	req.Header.Set("Accept", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return formats.ValidationResult{
			Status:    formats.StatusUnreachable,
			Detail:    err.Error(),
			CheckedAt: checkedAt,
		}
	}
	defer resp.Body.Close()

	switch {
	case resp.StatusCode == http.StatusOK:
		return formats.ValidationResult{
			Status:    formats.StatusOK,
			CheckedAt: checkedAt,
		}
	case resp.StatusCode == http.StatusUnauthorized:
		return formats.ValidationResult{
			Status:    formats.StatusExpired,
			Detail:    "live check returned 401",
			CheckedAt: checkedAt,
		}
	case resp.StatusCode == http.StatusForbidden:
		return formats.ValidationResult{
			Status:    formats.StatusScopeWarn,
			Detail:    "live check returned 403 (token alive, scopes insufficient)",
			CheckedAt: checkedAt,
		}
	case resp.StatusCode >= 500:
		return formats.ValidationResult{
			Status:    formats.StatusUnreachable,
			Detail:    "live check upstream " + resp.Status,
			CheckedAt: checkedAt,
		}
	default:
		// Any other status (e.g. 4xx other than 401/403) is treated as
		// unreachable rather than asserting the token is dead — the endpoint
		// is undocumented and silent failure modes exist.
		return formats.ValidationResult{
			Status:    formats.StatusUnreachable,
			Detail:    "live check unexpected status " + resp.Status,
			CheckedAt: checkedAt,
		}
	}
}
