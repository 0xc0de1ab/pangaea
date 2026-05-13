package githubcopilotapps

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/0xc0de1ab/pangaea/pkg/formats"
)

var _ formats.UsageProbe = Format{}

func githubCopilotAPIBase(host string) string {
	if v := strings.TrimSpace(os.Getenv("GITHUB_COPILOT_API_BASE_URL")); v != "" {
		return strings.TrimRight(v, "/")
	}
	host = strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(host, "https://"), "http://"))
	host = strings.TrimSuffix(host, "/")
	if host == "" || strings.EqualFold(host, "github.com") || strings.EqualFold(host, "api.github.com") {
		return "https://api.github.com"
	}
	return "https://" + host + "/api/v3"
}

func githubCopilotTokenPath() string {
	if v := strings.TrimSpace(os.Getenv("GITHUB_COPILOT_TOKEN_PATH")); v != "" {
		if strings.HasPrefix(v, "/") {
			return v
		}
		return "/" + v
	}
	return "/copilot_internal/v2/token"
}

func (Format) Probe(ctx context.Context, snap formats.Snapshot, _ string, httpClient *http.Client) (formats.UsageReport, error) {
	cs, ok := snap.(*snapshot)
	if !ok || cs == nil {
		return formats.UsageReport{}, fmt.Errorf("githubcopilotapps.Probe: invalid snapshot")
	}
	token := strings.TrimSpace(cs.primary.Token)
	if token == "" {
		return formats.UsageReport{}, fmt.Errorf("githubcopilotapps.Probe: empty oauth token")
	}
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 10 * time.Second}
	}
	endpoint := githubCopilotAPIBase(cs.primary.Host) + githubCopilotTokenPath()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return formats.UsageReport{}, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("User-Agent", "pangaea-github-copilot-probe")
	resp, err := httpClient.Do(req)
	if err != nil {
		return formats.UsageReport{}, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return formats.UsageReport{}, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		preview := body
		if len(preview) > 200 {
			preview = preview[:200]
		}
		return formats.UsageReport{}, fmt.Errorf("githubcopilotapps.Probe: GET %s: HTTP %d: %s", redactEndpoint(endpoint), resp.StatusCode, preview)
	}
	var doc map[string]any
	if err := json.Unmarshal(body, &doc); err != nil {
		return formats.UsageReport{}, fmt.Errorf("githubcopilotapps.Probe: decode token response: %w", err)
	}
	rep := formats.UsageReport{PlanTier: copilotPlanTier(doc)}
	rep.Notes = copilotProbeNotes(cs.primary, doc)
	return rep, nil
}

func copilotPlanTier(doc map[string]any) string {
	return firstNonBlank(
		stringField(doc, "sku"),
		stringField(doc, "copilot_plan"),
		stringField(doc, "copilotPlan"),
		stringField(doc, "plan"),
		stringField(doc, "plan_name"),
		stringField(doc, "planName"),
		stringField(doc, "subscription"),
		stringField(doc, "subscription_type"),
		stringField(doc, "subscriptionType"),
		stringField(doc, "subscription_tier"),
		stringField(doc, "subscriptionTier"),
		stringField(doc, "billing_plan"),
		stringField(doc, "billingPlan"),
	)
}

func copilotProbeNotes(primary appEntry, doc map[string]any) []string {
	notes := []string{}
	if primary.User != "" {
		notes = append(notes, "user:"+primary.User)
	}
	if primary.Host != "" {
		notes = append(notes, "host:"+primary.Host)
	}
	if status := firstNonBlank(stringField(doc, "status"), stringField(doc, "plan_status"), stringField(doc, "planStatus")); status != "" {
		notes = append(notes, "status:"+status)
	}
	if paid := firstNonBlank(stringField(doc, "paid_tier"), stringField(doc, "paidTier")); paid != "" {
		notes = append(notes, "paid-tier:"+paid)
	}
	if rate := firstNonBlank(stringField(doc, "rate_limit_tier"), stringField(doc, "rateLimitTier")); rate != "" {
		notes = append(notes, "rate-limit-tier:"+rate)
	}
	if tier := firstNonBlank(stringField(doc, "plan_name"), stringField(doc, "planName")); tier != "" {
		notes = append(notes, "tier:"+tier)
	}
	return notes
}

func stringField(doc map[string]any, key string) string {
	if len(doc) == 0 {
		return ""
	}
	value, ok := doc[key]
	if !ok {
		return ""
	}
	switch v := value.(type) {
	case string:
		return strings.TrimSpace(v)
	case fmt.Stringer:
		return strings.TrimSpace(v.String())
	default:
		return ""
	}
}

func redactEndpoint(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	u.RawQuery = ""
	u.User = nil
	return u.String()
}

// lookupGitHubLogin resolves the GitHub login when apps.json omits `user`.
// Uses GET {api}/user with the OAuth token. Override base URL for tests via GITHUB_USER_API_LOOKUP_BASE.
func lookupGitHubLogin(ctx context.Context, host, token string) (string, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return "", fmt.Errorf("githubcopilotapps: empty token")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	base := githubCopilotAPIBase(host)
	if v := strings.TrimSpace(os.Getenv("GITHUB_USER_API_LOOKUP_BASE")); v != "" {
		base = strings.TrimRight(v, "/")
	}
	client := &http.Client{Timeout: 8 * time.Second}
	endpoint := base + "/user"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("User-Agent", "pangaea-github-copilot-account")
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		preview := body
		if len(preview) > 120 {
			preview = preview[:120]
		}
		return "", fmt.Errorf("githubcopilotapps: GET /user: HTTP %d: %s", resp.StatusCode, preview)
	}
	var parsed struct {
		Login string `json:"login"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", fmt.Errorf("githubcopilotapps: decode /user: %w", err)
	}
	return strings.TrimSpace(parsed.Login), nil
}
