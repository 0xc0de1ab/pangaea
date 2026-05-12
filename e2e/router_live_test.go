package e2e

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/0xc0de1ab/pangaea/internal/provider"
)

func TestE2E_LiveRouterProviderCompatDialects(t *testing.T) {
	if os.Getenv("PANGAEA_LIVE_ROUTER_TEST") != "1" {
		t.Skip("set PANGAEA_LIVE_ROUTER_TEST=1 to exercise a live router")
	}

	baseURL := strings.TrimRight(firstEnv("PANGAEA_LIVE_ROUTER_BASE_URL", "PANGAEA_ROUTER_BASE_URL"), "/")
	if baseURL == "" {
		baseURL = "http://127.0.0.1:18080"
	}
	apiKey := firstEnv("PANGAEA_LIVE_ROUTER_API_KEY", "PANGAEA_ROUTER_API_KEY")
	adminToken := firstEnv("PANGAEA_LIVE_ROUTER_ADMIN_TOKEN", "PANGAEA_ROUTER_ADMIN_TOKEN")
	if adminToken == "" {
		adminToken = apiKey
	}
	timeout := liveRouterDurationEnv("PANGAEA_LIVE_ROUTER_REQUEST_TIMEOUT", 2*time.Minute)
	client := &http.Client{Timeout: timeout}

	providers, err := liveRouterProviders(client, baseURL, adminToken)
	if err != nil {
		t.Fatalf("list live router providers: %v", err)
	}
	candidates, skipped := liveRouterProviderCandidates(providers)
	for _, line := range skipped {
		t.Log(line)
	}
	if len(candidates) == 0 {
		t.Fatalf("no live router providers are ready/authenticated; total providers=%d", len(providers))
	}

	requireAllDialects := !liveRouterBoolEnv("PANGAEA_LIVE_ROUTER_SKIP_UNSUPPORTED_DIALECTS")
	for _, registration := range candidates {
		registration := registration
		t.Run(liveRouterSubtestName(registration), func(t *testing.T) {
			for _, dialect := range []liveRouterDialect{liveRouterDialectOpenAI, liveRouterDialectAnthropic, liveRouterDialectGemini} {
				dialect := dialect
				t.Run(string(dialect), func(t *testing.T) {
					models := liveRouterSelectModels(registration, dialect.capability())
					if len(models) == 0 {
						msg := fmt.Sprintf("%s does not report a model with %s", registration.Identity.ProviderInstanceID, dialect.capability())
						if requireAllDialects {
							t.Fatal(msg)
						}
						t.Skip(msg)
					}
					maxAttempts := liveRouterIntEnv("PANGAEA_LIVE_ROUTER_MODEL_ATTEMPTS", 4)
					if maxAttempts > 0 && len(models) > maxAttempts {
						models = models[:maxAttempts]
					}
					var failures []string
					for _, model := range models {
						text, err := liveRouterInvokeDialect(client, baseURL, apiKey, registration.Identity.ProviderInstanceID, dialect, model)
						if err != nil {
							failures = append(failures, fmt.Sprintf("%s: %v", model, err))
							t.Logf("%s %s model=%s failed: %v", registration.Identity.ProviderInstanceID, dialect, model, err)
							continue
						}
						if strings.TrimSpace(text) == "" {
							failures = append(failures, fmt.Sprintf("%s: empty response", model))
							t.Logf("%s %s model=%s returned empty response", registration.Identity.ProviderInstanceID, dialect, model)
							continue
						}
						t.Logf("%s %s model=%s response=%q", registration.Identity.ProviderInstanceID, dialect, model, liveRouterPreview(text, 120))
						return
					}
					t.Fatalf("%s %s failed for %d attempted model(s): %s", registration.Identity.ProviderInstanceID, dialect, len(models), strings.Join(failures, " | "))
				})
			}
		})
	}
}

type liveRouterDialect string

const (
	liveRouterDialectOpenAI    liveRouterDialect = "openai"
	liveRouterDialectAnthropic liveRouterDialect = "anthropic"
	liveRouterDialectGemini    liveRouterDialect = "gemini"

	liveRouterSmokeMaxTokens = 512
)

func (d liveRouterDialect) capability() provider.Capability {
	switch d {
	case liveRouterDialectOpenAI:
		return provider.CapabilityOpenAIChat
	case liveRouterDialectAnthropic:
		return provider.CapabilityAnthropicMessages
	case liveRouterDialectGemini:
		return provider.CapabilityGeminiGenerateContent
	default:
		return ""
	}
}

func liveRouterProviders(client *http.Client, baseURL string, adminToken string) ([]provider.Registration, error) {
	req, err := http.NewRequest(http.MethodGet, baseURL+"/router/v1/providers", nil)
	if err != nil {
		return nil, err
	}
	liveRouterSetBearer(req, adminToken)
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET /router/v1/providers: status=%d body=%s", resp.StatusCode, liveRouterPreview(string(body), 500))
	}
	var out struct {
		Providers []provider.Registration `json:"providers"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("decode providers: %w body=%s", err, liveRouterPreview(string(body), 500))
	}
	sort.SliceStable(out.Providers, func(i, j int) bool {
		return out.Providers[i].Identity.ProviderInstanceID < out.Providers[j].Identity.ProviderInstanceID
	})
	return out.Providers, nil
}

func liveRouterProviderCandidates(providers []provider.Registration) ([]provider.Registration, []string) {
	includeUnready := liveRouterBoolEnv("PANGAEA_LIVE_ROUTER_INCLUDE_UNREADY")
	var candidates []provider.Registration
	var skipped []string
	for _, registration := range providers {
		if !includeUnready {
			if registration.Health.Status != provider.HealthReady {
				skipped = append(skipped, fmt.Sprintf("skip %s: health=%s reason=%s", registration.Identity.ProviderInstanceID, registration.Health.Status, registration.Health.Reason))
				continue
			}
			if registration.Auth.Status != provider.AuthHealthy && registration.Auth.Status != provider.AuthRefreshSoon {
				skipped = append(skipped, fmt.Sprintf("skip %s: auth=%s reason=%s", registration.Identity.ProviderInstanceID, registration.Auth.Status, registration.Auth.LastRefreshErr))
				continue
			}
		}
		candidates = append(candidates, registration)
	}
	return candidates, skipped
}

func liveRouterSelectModels(registration provider.Registration, capability provider.Capability) []string {
	if len(registration.Models) == 0 {
		return nil
	}
	now := time.Now()
	type candidate struct {
		model provider.Model
		score int
	}
	candidates := make([]candidate, 0, len(registration.Models))
	for _, model := range registration.Models {
		if model.ID == "" || !liveRouterModelHasCapability(model, capability) || liveRouterModelQuotaExhausted(model, now) {
			continue
		}
		score := 0
		id := strings.ToLower(model.ID)
		service := strings.ToLower(string(registration.Identity.Service))
		switch {
		case id == service+"-default", id == "auto", strings.HasPrefix(id, "auto-"), strings.HasSuffix(id, "-default"):
			score += 100
		case model.Kind == "group" || len(model.GroupMembers) > 0:
			score += 60
		}
		if strings.Contains(id, "lite") || strings.Contains(id, "mini") {
			score += 20
		} else if strings.Contains(id, "flash") {
			score += 10
		}
		for _, alias := range model.Aliases {
			alias = strings.ToLower(alias)
			if alias == service+"-default" || strings.Contains(alias, "auto") || strings.Contains(alias, "default") {
				score += 30
				break
			}
		}
		if model.MaxOutputTokens > 0 {
			score += 5
		}
		candidates = append(candidates, candidate{model: model, score: score})
	}
	if len(candidates) == 0 {
		return nil
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].score != candidates[j].score {
			return candidates[i].score > candidates[j].score
		}
		return candidates[i].model.ID < candidates[j].model.ID
	})
	out := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		out = append(out, candidate.model.ID)
	}
	return out
}

func liveRouterModelHasCapability(model provider.Model, capability provider.Capability) bool {
	if capability == "" || len(model.Capabilities) == 0 {
		return true
	}
	for _, item := range model.Capabilities {
		if item == capability {
			return true
		}
	}
	return false
}

func liveRouterModelQuotaExhausted(model provider.Model, now time.Time) bool {
	if model.Quota == nil {
		return false
	}
	if model.Quota.RemainingPct > 0.1 {
		return false
	}
	return model.Quota.ResetAt.IsZero() || model.Quota.ResetAt.After(now)
}

func liveRouterInvokeDialect(client *http.Client, baseURL string, apiKey string, providerInstanceID string, dialect liveRouterDialect, model string) (string, error) {
	switch dialect {
	case liveRouterDialectOpenAI:
		return liveRouterInvokeOpenAI(client, baseURL, apiKey, providerInstanceID, model)
	case liveRouterDialectAnthropic:
		return liveRouterInvokeAnthropic(client, baseURL, apiKey, providerInstanceID, model)
	case liveRouterDialectGemini:
		return liveRouterInvokeGemini(client, baseURL, apiKey, providerInstanceID, model)
	default:
		return "", fmt.Errorf("unsupported dialect %q", dialect)
	}
}

func liveRouterInvokeOpenAI(client *http.Client, baseURL string, apiKey string, providerInstanceID string, model string) (string, error) {
	body := map[string]any{
		"model":      model,
		"max_tokens": liveRouterSmokeMaxTokens,
		"messages": []map[string]string{{
			"role":    "user",
			"content": "Reply with one short sentence containing the word pong.",
		}},
	}
	req, err := liveRouterJSONRequest(http.MethodPost, baseURL+"/v1/chat/completions", body)
	if err != nil {
		return "", err
	}
	liveRouterPrepareCompatRequest(req, apiKey, providerInstanceID, liveRouterRequestID(providerInstanceID, "openai"))
	data, status, err := liveRouterDo(client, req)
	if err != nil {
		return "", err
	}
	if status != http.StatusOK {
		return "", fmt.Errorf("status=%d body=%s", status, liveRouterPreview(string(data), 500))
	}
	var out struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return "", fmt.Errorf("decode OpenAI response: %w body=%s", err, liveRouterPreview(string(data), 500))
	}
	if len(out.Choices) == 0 {
		return "", fmt.Errorf("OpenAI response has no choices: %s", liveRouterPreview(string(data), 500))
	}
	return out.Choices[0].Message.Content, nil
}

func liveRouterInvokeAnthropic(client *http.Client, baseURL string, apiKey string, providerInstanceID string, model string) (string, error) {
	body := map[string]any{
		"model":      model,
		"max_tokens": liveRouterSmokeMaxTokens,
		"messages": []map[string]string{{
			"role":    "user",
			"content": "Reply with one short sentence containing the word pong.",
		}},
	}
	req, err := liveRouterJSONRequest(http.MethodPost, baseURL+"/v1/messages", body)
	if err != nil {
		return "", err
	}
	req.Header.Set("anthropic-version", "2023-06-01")
	liveRouterPrepareCompatRequest(req, apiKey, providerInstanceID, liveRouterRequestID(providerInstanceID, "anthropic"))
	data, status, err := liveRouterDo(client, req)
	if err != nil {
		return "", err
	}
	if status != http.StatusOK {
		return "", fmt.Errorf("status=%d body=%s", status, liveRouterPreview(string(data), 500))
	}
	var out struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return "", fmt.Errorf("decode Anthropic response: %w body=%s", err, liveRouterPreview(string(data), 500))
	}
	var parts []string
	for _, item := range out.Content {
		if item.Text != "" {
			parts = append(parts, item.Text)
		}
	}
	if len(parts) == 0 {
		return "", fmt.Errorf("Anthropic response has no text content: %s", liveRouterPreview(string(data), 500))
	}
	return strings.Join(parts, "\n"), nil
}

func liveRouterInvokeGemini(client *http.Client, baseURL string, apiKey string, providerInstanceID string, model string) (string, error) {
	body := map[string]any{
		"contents": []map[string]any{{
			"role": "user",
			"parts": []map[string]string{{
				"text": "Reply with one short sentence containing the word pong.",
			}},
		}},
		"generationConfig": map[string]any{"maxOutputTokens": liveRouterSmokeMaxTokens},
	}
	endpoint := baseURL + "/v1beta/models/" + url.PathEscape(model) + ":generateContent"
	req, err := liveRouterJSONRequest(http.MethodPost, endpoint, body)
	if err != nil {
		return "", err
	}
	liveRouterPrepareCompatRequest(req, apiKey, providerInstanceID, liveRouterRequestID(providerInstanceID, "gemini"))
	data, status, err := liveRouterDo(client, req)
	if err != nil {
		return "", err
	}
	if status != http.StatusOK {
		return "", fmt.Errorf("status=%d body=%s", status, liveRouterPreview(string(data), 500))
	}
	var out struct {
		Candidates []struct {
			Content struct {
				Parts []struct {
					Text string `json:"text"`
				} `json:"parts"`
			} `json:"content"`
		} `json:"candidates"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return "", fmt.Errorf("decode Gemini response: %w body=%s", err, liveRouterPreview(string(data), 500))
	}
	var parts []string
	for _, candidate := range out.Candidates {
		for _, part := range candidate.Content.Parts {
			if part.Text != "" {
				parts = append(parts, part.Text)
			}
		}
	}
	if len(parts) == 0 {
		return "", fmt.Errorf("Gemini response has no text content: %s", liveRouterPreview(string(data), 500))
	}
	return strings.Join(parts, "\n"), nil
}

func liveRouterJSONRequest(method string, endpoint string, payload any) (*http.Request, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequest(method, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("content-type", "application/json")
	return req, nil
}

func liveRouterPrepareCompatRequest(req *http.Request, apiKey string, providerInstanceID string, requestID string) {
	liveRouterSetBearer(req, apiKey)
	req.Header.Set("x-pangaea-provider-instance-id", providerInstanceID)
	req.Header.Set("x-request-id", requestID)
}

func liveRouterSetBearer(req *http.Request, token string) {
	if strings.TrimSpace(token) != "" {
		req.Header.Set("authorization", "Bearer "+strings.TrimSpace(token))
	}
}

func liveRouterDo(client *http.Client, req *http.Request) ([]byte, int, error) {
	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, resp.StatusCode, err
	}
	return body, resp.StatusCode, nil
}

func liveRouterRequestID(providerInstanceID string, dialect string) string {
	return "live_" + liveRouterSanitize(providerInstanceID) + "_" + dialect + "_" + strconv.FormatInt(time.Now().UnixNano(), 36)
}

func liveRouterSubtestName(registration provider.Registration) string {
	parts := []string{
		string(registration.Identity.Service),
		registration.Identity.ProviderInstanceID,
		registration.Identity.HostName,
		registration.Identity.NodeID,
	}
	return liveRouterSanitize(strings.Join(parts, "_"))
}

func liveRouterSanitize(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var b strings.Builder
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			continue
		}
		b.WriteByte('_')
	}
	out := strings.Trim(b.String(), "_")
	if out == "" {
		return "unknown"
	}
	return out
}

func liveRouterPreview(value string, max int) string {
	value = strings.TrimSpace(value)
	if max <= 0 || len(value) <= max {
		return value
	}
	return value[:max] + "..."
}

func liveRouterDurationEnv(name string, fallback time.Duration) time.Duration {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}
	parsed, err := time.ParseDuration(value)
	if err != nil || parsed <= 0 {
		return fallback
	}
	return parsed
}

func liveRouterBoolEnv(name string) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(name))) {
	case "1", "t", "true", "y", "yes", "on":
		return true
	default:
		return false
	}
}

func liveRouterIntEnv(name string, fallback int) int {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func firstEnv(names ...string) string {
	for _, name := range names {
		if value := strings.TrimSpace(os.Getenv(name)); value != "" {
			return value
		}
	}
	return ""
}
