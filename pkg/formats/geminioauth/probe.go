package geminioauth

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"

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
	CloudaiCompanionProject string                  `json:"cloudaicompanionProject,omitempty"`
	Metadata                loadCodeAssistMetadata  `json:"metadata"`
	Mode                    string                  `json:"mode,omitempty"`
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

// Probe issues a lightweight POST to v1internal:loadCodeAssist. Successful
// response → token works; we surface the user's currentTier (free /
// standard / legacy), the paid tier id if any, and the assigned project
// id. Gemini does not expose a usage-percent endpoint reachable by Bearer
// alone — retrieveUserQuota needs a project, which we have, but its
// response shape is per-bucket and noisy for a one-line summary; we leave
// it for a future enhancement.
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
	return rep, nil
}
