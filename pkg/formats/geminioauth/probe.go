package geminioauth

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/0xc0de1ab/pangaea/pkg/formats"
)

// LoadCodeAssistEndpoint is the gemini-cli code-assist enrolment URL. We
// call it with `mode: "HEALTH_CHECK"` which doubles as a lightweight
// validity probe (gemini-cli itself does this on token refresh — see
// packages/core/src/code_assist/server.ts:282-299).
//
// Overridable for tests via the package-level variable or the
// GEMINI_LOADCODEASSIST_URL env var (runtime).
var LoadCodeAssistEndpoint = func() string {
	if v := os.Getenv("GEMINI_LOADCODEASSIST_URL"); v != "" {
		return v
	}
	return "https://cloudcode-pa.googleapis.com/v1internal:loadCodeAssist"
}()

type loadCodeAssistRequest struct {
	CloudaiCompanionProject string                 `json:"cloudaicompanionProject,omitempty"`
	Metadata                loadCodeAssistMetadata `json:"metadata"`
	Mode                    string                 `json:"mode,omitempty"`
}

type loadCodeAssistMetadata struct {
	IDEType    string `json:"ideType"`
	Platform   string `json:"platform"`
	PluginType string `json:"pluginType"`
}

type tier struct {
	ID                     string `json:"id"`
	Name                   string `json:"name"`
	HasOnboardedPreviously bool   `json:"hasOnboardedPreviously"`
}

type loadCodeAssistResponse struct {
	CurrentTier             *tier  `json:"currentTier"`
	PaidTier                *tier  `json:"paidTier"`
	CloudaiCompanionProject string `json:"cloudaicompanionProject"`
}

type retrieveUserQuotaRequest struct {
	Project string `json:"project,omitempty"`
}

type quotaBucket struct {
	ModelID           string  `json:"modelId"`
	RemainingFraction float64 `json:"remainingFraction"`
	RemainingAmount   string  `json:"remainingAmount"`
	ResetTime         string  `json:"resetTime"`
}

type retrieveUserQuotaResponse struct {
	Buckets []quotaBucket `json:"buckets"`
}

// Probe issues a lightweight POST to v1internal:loadCodeAssist followed by
// retrieveUserQuota for the assigned project. This mirrors Gemini CLI's own
// quota refresh path used by the `/model` UI.
func (Format) Probe(ctx context.Context, snap formats.Snapshot, _ string, httpClient *http.Client) (formats.UsageReport, error) {
	cs, ok := snap.(*snapshot)
	if !ok {
		return formats.UsageReport{}, fmt.Errorf("geminioauth.Probe: foreign snapshot")
	}
	if httpClient == nil {
		httpClient = http.DefaultClient
	}

	body, err := json.Marshal(loadCodeAssistRequest{
		Metadata: loadCodeAssistMetadata{
			IDEType:    "IDE_UNSPECIFIED",
			Platform:   "PLATFORM_UNSPECIFIED",
			PluginType: "GEMINI",
		},
		Mode: "HEALTH_CHECK",
	})
	if err != nil {
		return formats.UsageReport{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, LoadCodeAssistEndpoint, bytes.NewReader(body))
	if err != nil {
		return formats.UsageReport{}, err
	}
	req.Header.Set("Authorization", "Bearer "+cs.accessToken)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err := httpClient.Do(req)
	if err != nil {
		return formats.UsageReport{}, err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode/100 != 2 {
		preview := respBody
		if len(preview) > 200 {
			preview = preview[:200]
		}
		return formats.UsageReport{}, fmt.Errorf("geminioauth.Probe: HTTP %d: %s", resp.StatusCode, preview)
	}
	var out loadCodeAssistResponse
	if err := json.Unmarshal(respBody, &out); err != nil {
		return formats.UsageReport{}, fmt.Errorf("geminioauth.Probe: decode: %w", err)
	}
	rep := formats.UsageReport{}
	if out.CurrentTier != nil {
		rep.PlanTier = out.CurrentTier.ID
		if out.CurrentTier.Name != "" {
			rep.Notes = append(rep.Notes, "tier: "+out.CurrentTier.Name)
		}
	}
	if out.PaidTier != nil && out.PaidTier.ID != "" && (out.CurrentTier == nil || out.PaidTier.ID != out.CurrentTier.ID) {
		rep.Notes = append(rep.Notes, "paid-tier: "+out.PaidTier.ID)
	}
	if out.CloudaiCompanionProject != "" {
		rep.Notes = append(rep.Notes, "project: "+out.CloudaiCompanionProject)
	}
	if out.CloudaiCompanionProject == "" {
		return rep, nil
	}

	quotaReqBody, err := json.Marshal(retrieveUserQuotaRequest{Project: out.CloudaiCompanionProject})
	if err != nil {
		return formats.UsageReport{}, err
	}
	quotaReq, err := http.NewRequestWithContext(ctx, http.MethodPost, retrieveUserQuotaEndpoint(LoadCodeAssistEndpoint), bytes.NewReader(quotaReqBody))
	if err != nil {
		return formats.UsageReport{}, err
	}
	quotaReq.Header.Set("Authorization", "Bearer "+cs.accessToken)
	quotaReq.Header.Set("Content-Type", "application/json")
	quotaReq.Header.Set("Accept", "application/json")
	quotaResp, err := httpClient.Do(quotaReq)
	if err != nil {
		return formats.UsageReport{}, err
	}
	defer quotaResp.Body.Close()
	quotaBody, _ := io.ReadAll(quotaResp.Body)
	if quotaResp.StatusCode/100 != 2 {
		preview := quotaBody
		if len(preview) > 200 {
			preview = preview[:200]
		}
		return formats.UsageReport{}, fmt.Errorf("geminioauth.Probe quota: HTTP %d: %s", quotaResp.StatusCode, preview)
	}
	var quota retrieveUserQuotaResponse
	if err := json.Unmarshal(quotaBody, &quota); err != nil {
		return formats.UsageReport{}, fmt.Errorf("geminioauth.Probe quota: decode: %w", err)
	}
	rep.Windows = buildQuotaWindows(quota.Buckets)
	if len(rep.Windows) > 0 {
		rep.RemainingPct = rep.Windows[0].RemainingPct
		rep.ResetAt = rep.Windows[0].ResetAt
	}
	return rep, nil
}

func retrieveUserQuotaEndpoint(loadEndpoint string) string {
	return strings.Replace(loadEndpoint, ":loadCodeAssist", ":retrieveUserQuota", 1)
}

func buildQuotaWindows(buckets []quotaBucket) []formats.UsageWindow {
	type agg struct {
		label        string
		remainingPct float64
		resetAt      string
	}
	byLabel := map[string]agg{}
	for _, bucket := range buckets {
		label := geminiQuotaLabel(bucket.ModelID)
		if label == "" || (bucket.RemainingFraction < 0 && bucket.RemainingAmount == "") {
			continue
		}
		current, ok := byLabel[label]
		if !ok || bucket.RemainingFraction < current.remainingPct/100 {
			byLabel[label] = agg{
				label:        label,
				remainingPct: bucket.RemainingFraction * 100,
				resetAt:      bucket.ResetTime,
			}
		}
	}
	order := []string{"Flash", "Flash Lite", "Pro"}
	out := make([]formats.UsageWindow, 0, len(byLabel))
	for _, label := range order {
		current, ok := byLabel[label]
		if !ok {
			continue
		}
		out = append(out, formats.UsageWindow{
			Label:        current.label,
			RemainingPct: current.remainingPct,
			ResetAt:      parseQuotaReset(current.resetAt),
		})
		delete(byLabel, label)
	}
	if len(byLabel) > 0 {
		rest := make([]string, 0, len(byLabel))
		for label := range byLabel {
			rest = append(rest, label)
		}
		slices.Sort(rest)
		for _, label := range rest {
			current := byLabel[label]
			out = append(out, formats.UsageWindow{
				Label:        current.label,
				RemainingPct: current.remainingPct,
				ResetAt:      parseQuotaReset(current.resetAt),
			})
		}
	}
	return out
}

func geminiQuotaLabel(modelID string) string {
	modelID = strings.ToLower(strings.TrimSpace(modelID))
	switch {
	case strings.Contains(modelID, "flash-lite"):
		return "Flash Lite"
	case strings.Contains(modelID, "flash"):
		return "Flash"
	case strings.Contains(modelID, "pro"):
		return "Pro"
	default:
		return ""
	}
}

func parseQuotaReset(raw string) time.Time {
	if raw == "" {
		return time.Time{}
	}
	layouts := []string{time.RFC3339Nano, time.RFC3339}
	for _, layout := range layouts {
		if ts, err := time.Parse(layout, raw); err == nil {
			return ts
		}
	}
	return time.Time{}
}
