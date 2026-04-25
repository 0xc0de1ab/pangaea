package claudecreds

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"

	"github.com/0xc0de1ab/pangaea/pkg/formats"
)

// AnthropicProfileEndpoint is the OAuth profile endpoint Claude Code hits to
// populate `oauthAccount.organizationName/Type/RateLimitTier` after login.
// Documented via cli.js inspection: function `m51(A)` issues
// `GET /api/oauth/profile` with `Authorization: Bearer <access_token>`.
//
// Overridable for tests via the package-level variable (compile-time) or
// via the ANTHROPIC_OAUTH_PROFILE_URL env var (runtime — useful for
// black-box e2e against a fake host without recompiling).
var AnthropicProfileEndpoint = func() string {
	if v := os.Getenv("ANTHROPIC_OAUTH_PROFILE_URL"); v != "" {
		return v
	}
	return "https://api.anthropic.com/api/oauth/profile"
}()

// oauthProfileResponse mirrors the slice the CLI reads. Other fields exist
// (subscription details, addons) but we deliberately ignore them.
type oauthProfileResponse struct {
	Account struct {
		UUID         string `json:"uuid"`
		EmailAddress string `json:"email_address"`
	} `json:"account"`
	Organization struct {
		UUID             string `json:"uuid"`
		Name             string `json:"name"`
		OrganizationType string `json:"organization_type"`
		RateLimitTier    string `json:"rate_limit_tier"`
	} `json:"organization"`
}

// Probe issues GET /api/oauth/profile with the cached access_token and
// returns plan/org info. Anthropic does not expose a "current usage" GET —
// the CLI reads rate-limit numbers from headers on /v1/messages calls — so
// the report carries plan tier + org notes only, no Used/Limit numbers.
func (Format) Probe(ctx context.Context, snap formats.Snapshot, _ string, httpClient *http.Client) (formats.UsageReport, error) {
	cs, ok := snap.(*snapshot)
	if !ok {
		return formats.UsageReport{}, fmt.Errorf("claudecreds.Probe: foreign snapshot")
	}
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, AnthropicProfileEndpoint, nil)
	if err != nil {
		return formats.UsageReport{}, err
	}
	req.Header.Set("Authorization", "Bearer "+cs.accessToken)
	req.Header.Set("Accept", "application/json")
	resp, err := httpClient.Do(req)
	if err != nil {
		return formats.UsageReport{}, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode/100 != 2 {
		// Truncate body for the error message to avoid logging anything that
		// might contain partial token echoes (Anthropic does not echo tokens
		// here, but defensive bounds are cheap).
		preview := body
		if len(preview) > 200 {
			preview = preview[:200]
		}
		return formats.UsageReport{}, fmt.Errorf("claudecreds.Probe: HTTP %d: %s", resp.StatusCode, preview)
	}
	var out oauthProfileResponse
	if err := json.Unmarshal(body, &out); err != nil {
		return formats.UsageReport{}, fmt.Errorf("claudecreds.Probe: decode: %w", err)
	}

	rep := formats.UsageReport{
		PlanTier: out.Organization.OrganizationType,
	}
	if out.Organization.Name != "" {
		rep.Notes = append(rep.Notes, "org: "+out.Organization.Name)
	}
	if out.Organization.RateLimitTier != "" {
		rep.Notes = append(rep.Notes, "rate-limit-tier: "+out.Organization.RateLimitTier)
	}
	if out.Account.EmailAddress != "" {
		rep.Notes = append(rep.Notes, "email: "+out.Account.EmailAddress)
	}
	return rep, nil
}
