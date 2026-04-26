package codexauth

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/0xc0de1ab/pangaea/pkg/formats"
)

// ChatGPTUsageEndpoint mirrors codex's BackendClient default base. The call
// is `GET /backend-api/wham/usage` with the OAuth access_token as Bearer
// and `ChatGPT-Account-ID` set to the chatgpt_account_id JWT claim.
//
// Source: codex-rs/backend-client/src/client.rs (`get_rate_limits`),
// codex-rs/login/src/auth/agent_identity.rs (default base URL).
//
// Overridable for tests via the package-level variable or the
// CHATGPT_USAGE_URL env var (runtime).
var ChatGPTUsageEndpoint = func() string {
	if v := os.Getenv("CHATGPT_USAGE_URL"); v != "" {
		return v
	}
	return "https://chatgpt.com/backend-api/wham/usage"
}()

// rateLimitWindow mirrors RateLimitWindowSnapshot from the codex OpenAPI.
type rateLimitWindow struct {
	UsedPercent        float64  `json:"used_percent"`
	LimitWindowSeconds int64    `json:"limit_window_seconds"`
	ResetAfterSeconds  int64    `json:"reset_after_seconds"`
	ResetAt            flexTime `json:"reset_at"`
}

type rateLimitDetails struct {
	Allowed         bool             `json:"allowed"`
	LimitReached    bool             `json:"limit_reached"`
	PrimaryWindow   *rateLimitWindow `json:"primary_window"`
	SecondaryWindow *rateLimitWindow `json:"secondary_window"`
}

// rateLimitStatusPayload mirrors RateLimitStatusPayload. We pick the fields
// most useful for human-facing output and ignore the rest.
type rateLimitStatusPayload struct {
	PlanType             string           `json:"plan_type"`
	RateLimit            rateLimitDetails `json:"rate_limit"`
	AdditionalRateLimits []struct {
		LimitName string           `json:"limit_name"`
		RateLimit rateLimitDetails `json:"rate_limit"`
	} `json:"additional_rate_limits"`
}

type flexTime struct {
	time.Time
}

func (ft *flexTime) UnmarshalJSON(data []byte) error {
	raw := string(data)
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "null" {
		ft.Time = time.Time{}
		return nil
	}
	if raw[0] == '"' {
		var s string
		if err := json.Unmarshal(data, &s); err != nil {
			return err
		}
		if s == "" {
			ft.Time = time.Time{}
			return nil
		}
		if ts, err := time.Parse(time.RFC3339, s); err == nil {
			ft.Time = ts.UTC()
			return nil
		}
		if ts, err := time.Parse(time.RFC3339Nano, s); err == nil {
			ft.Time = ts.UTC()
			return nil
		}
		return fmt.Errorf("invalid RFC3339 time %q", s)
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return fmt.Errorf("invalid unix timestamp %q: %w", raw, err)
	}
	ft.Time = time.Unix(n, 0).UTC()
	return nil
}

// Probe calls /backend-api/wham/usage. The codex auth.json file already
// contains everything needed: tokens.access_token + chatgpt_account_id
// (decoded from id_token at parse time). If chatgpt_account_id is empty
// we fail loudly — without it the backend returns 401 for the wrong
// account, which is worse than not calling at all.
func (Format) Probe(ctx context.Context, snap formats.Snapshot, _ string, httpClient *http.Client) (formats.UsageReport, error) {
	cs, ok := snap.(*snapshot)
	if !ok {
		return formats.UsageReport{}, fmt.Errorf("codexauth.Probe: foreign snapshot")
	}
	accountID := cs.chatgptAccountID
	if accountID == "" {
		// Fall back to top-level tokens.account_id if id_token claim missing.
		accountID = cs.accountID
	}
	if accountID == "" {
		return formats.UsageReport{}, fmt.Errorf("codexauth.Probe: chatgpt_account_id is required but empty")
	}
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, ChatGPTUsageEndpoint, nil)
	if err != nil {
		return formats.UsageReport{}, err
	}
	req.Header.Set("Authorization", "Bearer "+cs.accessToken)
	req.Header.Set("ChatGPT-Account-ID", accountID)
	req.Header.Set("Accept", "application/json")
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
		return formats.UsageReport{}, fmt.Errorf("codexauth.Probe: HTTP %d: %s", resp.StatusCode, preview)
	}
	var out rateLimitStatusPayload
	if err := json.Unmarshal(body, &out); err != nil {
		return formats.UsageReport{}, fmt.Errorf("codexauth.Probe: decode: %w", err)
	}

	rep := formats.UsageReport{PlanTier: out.PlanType}
	if w := out.RateLimit.PrimaryWindow; w != nil {
		rep.RemainingPct = 100 - w.UsedPercent
		rep.Unit = humanWindowUnit(w.LimitWindowSeconds)
		rep.ResetAt = w.ResetAt.Time
		rep.Windows = append(rep.Windows, usageWindow("", w))
	}
	if w := out.RateLimit.SecondaryWindow; w != nil {
		rep.Windows = append(rep.Windows, usageWindow("", w))
		rep.Notes = append(rep.Notes,
			fmt.Sprintf("%s window: %.1f%% used (resets %s)",
				humanWindowUnit(w.LimitWindowSeconds),
				w.UsedPercent,
				w.ResetAt.UTC().Format(time.RFC3339)))
	}
	for _, extra := range out.AdditionalRateLimits {
		if w := extra.RateLimit.PrimaryWindow; w != nil {
			rep.Windows = append(rep.Windows, usageWindow(extra.LimitName, w))
		}
		if w := extra.RateLimit.SecondaryWindow; w != nil {
			rep.Windows = append(rep.Windows, usageWindow(extra.LimitName, w))
		}
		if extra.RateLimit.LimitReached && extra.LimitName != "" {
			rep.Notes = append(rep.Notes, extra.LimitName+": limit reached on at least one window")
		}
	}
	if out.RateLimit.LimitReached {
		rep.Notes = append(rep.Notes, "limit reached on at least one window")
	}
	return rep, nil
}

// humanWindowUnit converts a window size in seconds to a short human label
// suitable for inclusion in operator-facing messages.
func humanWindowUnit(seconds int64) string {
	switch {
	case seconds <= 0:
		return "window"
	case seconds <= 60*60:
		return fmt.Sprintf("%dm window", seconds/60)
	case seconds <= 24*60*60:
		return fmt.Sprintf("%dh window", seconds/3600)
	default:
		return fmt.Sprintf("%dd window", seconds/86400)
	}
}

func usageWindow(prefix string, w *rateLimitWindow) formats.UsageWindow {
	label := humanWindowLabel(w.LimitWindowSeconds)
	if prefix != "" {
		label = prefix + " " + label
	}
	return formats.UsageWindow{
		Label:        label,
		RemainingPct: 100 - w.UsedPercent,
		ResetAt:      w.ResetAt.Time,
		Unit:         humanWindowUnit(w.LimitWindowSeconds),
	}
}

func humanWindowLabel(seconds int64) string {
	switch seconds {
	case 5 * 60 * 60:
		return "5h limit"
	case 7 * 24 * 60 * 60:
		return "Weekly limit"
	}
	switch {
	case seconds <= 0:
		return "limit"
	case seconds < 24*60*60:
		return fmt.Sprintf("%dh limit", seconds/3600)
	default:
		return fmt.Sprintf("%dd limit", seconds/86400)
	}
}
