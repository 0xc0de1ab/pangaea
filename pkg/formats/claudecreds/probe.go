package claudecreds

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/0xc0de1ab/pangaea/pkg/formats"
)

// AnthropicUsageEndpoint mirrors Claude Code's `/usage` screen.
//
// Source: claude-code-cli `src/services/api/usage.ts`
// (`GET ${BASE_API_URL}/api/oauth/usage` with Bearer auth and
// `anthropic-beta: oauth-2025-04-20`).
//
// Overridable for tests via the package-level variable or the
// ANTHROPIC_OAUTH_USAGE_URL env var.
var AnthropicUsageEndpoint = func() string {
	if v := os.Getenv("ANTHROPIC_OAUTH_USAGE_URL"); v != "" {
		return v
	}
	return "https://api.anthropic.com/api/oauth/usage"
}()

type oauthUsageRateLimit struct {
	Utilization *float64 `json:"utilization"`
	ResetsAt    string   `json:"resets_at"`
}

type oauthExtraUsage struct {
	IsEnabled    bool     `json:"is_enabled"`
	MonthlyLimit *int64   `json:"monthly_limit"`
	UsedCredits  *int64   `json:"used_credits"`
	Utilization  *float64 `json:"utilization"`
}

type oauthUsageResponse struct {
	FiveHour          *oauthUsageRateLimit `json:"five_hour"`
	SevenDay          *oauthUsageRateLimit `json:"seven_day"`
	SevenDayOAuthApps *oauthUsageRateLimit `json:"seven_day_oauth_apps"`
	SevenDayOpus      *oauthUsageRateLimit `json:"seven_day_opus"`
	SevenDaySonnet    *oauthUsageRateLimit `json:"seven_day_sonnet"`
	ExtraUsage        *oauthExtraUsage     `json:"extra_usage"`
}

// Probe issues GET /api/oauth/usage with the cached access token. The returned
// windows intentionally mirror the Claude Code `/usage` tab:
//   - Current session
//   - Current week (all models)
//   - Current week (Sonnet only) only for max/team/unknown subscription types
//   - Extra usage only for pro/max when the API exposes it
func (Format) Probe(ctx context.Context, snap formats.Snapshot, _ string, httpClient *http.Client) (formats.UsageReport, error) {
	cs, ok := snap.(*snapshot)
	if !ok {
		return formats.UsageReport{}, fmt.Errorf("claudecreds.Probe: foreign snapshot")
	}
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, AnthropicUsageEndpoint, nil)
	if err != nil {
		return formats.UsageReport{}, err
	}
	req.Header.Set("Authorization", "Bearer "+cs.accessToken)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("anthropic-beta", "oauth-2025-04-20")
	req.Header.Set("User-Agent", "pangaeactl/notifier")
	resp, err := httpClient.Do(req)
	if err != nil {
		return formats.UsageReport{}, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode/100 != 2 {
		preview := body
		if len(preview) > 200 {
			preview = preview[:200]
		}
		return formats.UsageReport{}, fmt.Errorf("claudecreds.Probe: HTTP %d: %s", resp.StatusCode, preview)
	}
	var out oauthUsageResponse
	if err := json.Unmarshal(body, &out); err != nil {
		return formats.UsageReport{}, fmt.Errorf("claudecreds.Probe: decode: %w", err)
	}

	rep := formats.UsageReport{
		PlanTier: strings.TrimSpace(cs.subscriptionType),
	}
	if rep.PlanTier == "" {
		rep.PlanTier = "unknown"
	}
	if cs.rateLimitTier != "" {
		rep.Notes = append(rep.Notes, "rate-limit-tier: "+cs.rateLimitTier)
	}

	if w := claudeUsageWindow("Current session", out.FiveHour); w != nil {
		rep.Windows = append(rep.Windows, *w)
		rep.RemainingPct = w.RemainingPct
		rep.ResetAt = w.ResetAt
	}
	if w := claudeUsageWindow("Current week (all models)", out.SevenDay); w != nil {
		rep.Windows = append(rep.Windows, *w)
	}
	if showClaudeSonnetWindow(cs.subscriptionType) {
		if w := claudeUsageWindow("Current week (Sonnet only)", out.SevenDaySonnet); w != nil {
			rep.Windows = append(rep.Windows, *w)
		}
	}
	if showClaudeExtraUsage(cs.subscriptionType) && out.ExtraUsage != nil {
		switch {
		case !out.ExtraUsage.IsEnabled:
			rep.Notes = append(rep.Notes, "extra usage: not enabled")
		case out.ExtraUsage.MonthlyLimit == nil:
			rep.Notes = append(rep.Notes, "extra usage: unlimited")
		case out.ExtraUsage.Utilization != nil:
			w := formats.UsageWindow{
				Label:        "Extra usage",
				RemainingPct: clampPct(100 - *out.ExtraUsage.Utilization),
			}
			rep.Windows = append(rep.Windows, w)
			if out.ExtraUsage.UsedCredits != nil && out.ExtraUsage.MonthlyLimit != nil {
				rep.Notes = append(rep.Notes, fmt.Sprintf(
					"extra usage spend: %s / %s",
					formatCents(*out.ExtraUsage.UsedCredits),
					formatCents(*out.ExtraUsage.MonthlyLimit),
				))
			}
		}
	}
	return rep, nil
}

func claudeUsageWindow(label string, in *oauthUsageRateLimit) *formats.UsageWindow {
	if in == nil || in.Utilization == nil {
		return nil
	}
	var resetAt time.Time
	if strings.TrimSpace(in.ResetsAt) != "" {
		if ts, err := time.Parse(time.RFC3339Nano, in.ResetsAt); err == nil {
			resetAt = ts
		} else if ts, err := time.Parse(time.RFC3339, in.ResetsAt); err == nil {
			resetAt = ts
		}
	}
	return &formats.UsageWindow{
		Label:        label,
		RemainingPct: clampPct(100 - *in.Utilization),
		ResetAt:      resetAt,
	}
}

func showClaudeSonnetWindow(subscriptionType string) bool {
	switch strings.ToLower(strings.TrimSpace(subscriptionType)) {
	case "", "max", "team":
		return true
	default:
		return false
	}
}

func showClaudeExtraUsage(subscriptionType string) bool {
	switch strings.ToLower(strings.TrimSpace(subscriptionType)) {
	case "max", "pro":
		return true
	default:
		return false
	}
}

func clampPct(v float64) float64 {
	switch {
	case v < 0:
		return 0
	case v > 100:
		return 100
	default:
		return v
	}
}

func formatCents(v int64) string {
	sign := ""
	if v < 0 {
		sign = "-"
		v = -v
	}
	return fmt.Sprintf("%s$%d.%02d", sign, v/100, v%100)
}
