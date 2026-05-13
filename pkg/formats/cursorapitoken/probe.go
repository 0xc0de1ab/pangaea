package cursorapitoken

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

// Cursor documents GET /v1/me (Basic auth: API key as username, empty password).
// See https://cursor.com/docs/cloud-agent/api/endpoints

var _ formats.UsageProbe = Format{}

func cursorAPIBase() string {
	if v := strings.TrimSpace(os.Getenv("CURSOR_API_BASE_URL")); v != "" {
		return strings.TrimRight(v, "/")
	}
	return "https://api.cursor.com"
}

func probeHTTPClient(c *http.Client) *http.Client {
	if c != nil {
		return c
	}
	return &http.Client{Timeout: 10 * time.Second}
}

type cursorMeDoc struct {
	ApiKeyName string `json:"apiKeyName"`
	CreatedAt  string `json:"createdAt"`
	UserEmail  string `json:"userEmail"`
	// Undocumented today; kept so Pangaea can surface plan info if Cursor adds fields.
	TeamName         string `json:"teamName,omitempty"`
	Plan             string `json:"plan,omitempty"`
	PlanName         string `json:"planName,omitempty"`
	Subscription     string `json:"subscription,omitempty"`
	SubscriptionTier string `json:"subscriptionTier,omitempty"`
	UserTier         string `json:"userTier,omitempty"`
	BillingPlan      string `json:"billingPlan,omitempty"`
	OrganizationName string `json:"organizationName,omitempty"`
}

func fetchCursorMe(ctx context.Context, token string, httpClient *http.Client) (cursorMeDoc, error) {
	var zero cursorMeDoc
	token = strings.TrimSpace(token)
	if token == "" {
		return zero, fmt.Errorf("cursorapitoken: empty api token")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, cursorAPIBase()+"/v1/me", nil)
	if err != nil {
		return zero, err
	}
	req.Header.Set("Accept", "application/json")
	req.SetBasicAuth(token, "")
	resp, err := httpClient.Do(req)
	if err != nil {
		return zero, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return zero, err
	}
	if resp.StatusCode/100 != 2 {
		preview := body
		if len(preview) > 200 {
			preview = preview[:200]
		}
		return zero, fmt.Errorf("cursorapitoken: GET /v1/me: HTTP %d: %s", resp.StatusCode, preview)
	}
	var doc cursorMeDoc
	if err := json.Unmarshal(body, &doc); err != nil {
		return zero, fmt.Errorf("cursorapitoken: decode /v1/me: %w", err)
	}
	return doc, nil
}

func cursorPlanLabel(doc cursorMeDoc) string {
	parts := []string{
		strings.TrimSpace(doc.SubscriptionTier),
		strings.TrimSpace(doc.Plan),
		strings.TrimSpace(doc.PlanName),
		strings.TrimSpace(doc.Subscription),
		strings.TrimSpace(doc.UserTier),
		strings.TrimSpace(doc.BillingPlan),
	}
	for _, p := range parts {
		if p != "" {
			return p
		}
	}
	return ""
}

func (Format) Probe(ctx context.Context, snap formats.Snapshot, _ string, httpClient *http.Client) (formats.UsageReport, error) {
	if snap == nil {
		return formats.UsageReport{}, fmt.Errorf("cursorapitoken.Probe: nil snapshot")
	}
	token := strings.TrimSpace(string(snap.Raw()))
	if token == "" {
		return formats.UsageReport{}, fmt.Errorf("cursorapitoken.Probe: empty token")
	}
	doc, err := fetchCursorMe(ctx, token, probeHTTPClient(httpClient))
	if err != nil {
		return formats.UsageReport{}, err
	}
	rep := formats.UsageReport{}
	if tier := cursorPlanLabel(doc); tier != "" {
		rep.PlanTier = tier
	}
	if email := strings.TrimSpace(doc.UserEmail); email != "" {
		rep.Notes = append(rep.Notes, "email:"+email)
	}
	if name := strings.TrimSpace(doc.ApiKeyName); name != "" {
		rep.Notes = append(rep.Notes, "api-key-name:"+name)
	}
	if team := strings.TrimSpace(doc.TeamName); team != "" {
		rep.Notes = append(rep.Notes, "team:"+team)
	}
	if org := strings.TrimSpace(doc.OrganizationName); org != "" {
		rep.Notes = append(rep.Notes, "organization:"+org)
	}
	if rep.PlanTier == "" {
		// Documented /v1/me schema has no plan field (as of Cloud Agents OpenAPI v1).
		rep.Notes = append(rep.Notes, "status:Cursor Cloud API plan not reported by GET /v1/me")
	}
	return rep, nil
}
