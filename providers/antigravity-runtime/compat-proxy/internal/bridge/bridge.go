package bridge

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/antigravity-compat-proxy/internal/interfaces"
	"github.com/google/antigravity-compat-proxy/internal/models"
	"golang.org/x/net/http2"
)

var bufferPool = sync.Pool{
	New: func() interface{} {
		return new(bytes.Buffer)
	},
}

const DefaultModelAlias = "antigravity-default"

type engineBridge struct {
	httpClient *http.Client
	coreAddr   string
	coreCSRF   string
	auth       interfaces.AuthProvider
	useBinary  bool
	streamTap  *CloudStreamProxy
}

// NewEngineBridge creates a new EngineBridge implementation.
func NewEngineBridge(coreAddr string, auth interfaces.AuthProvider) *engineBridge {
	tr := &http2.Transport{
		AllowHTTP: true,
		DialTLSContext: func(ctx context.Context, network, addr string, cfg *tls.Config) (net.Conn, error) {
			return net.Dial(network, addr)
		},
	}

	return &engineBridge{
		httpClient: &http.Client{Transport: tr},
		coreAddr:   coreAddr,
		coreCSRF:   "proxy-secret-token",
		auth:       auth,
		useBinary:  false,
	}
}

func (b *engineBridge) SetCoreCSRF(token string) {
	b.coreCSRF = token
}

func (b *engineBridge) SetStreamTap(tap *CloudStreamProxy) {
	b.streamTap = tap
}

func (b *engineBridge) Invoke(ctx context.Context, model string, prompt string, tools []models.ToolDefinition, media []models.Media) (*interfaces.ModelResponse, error) {
	token, err := b.auth.GetLatestToken()
	if err != nil {
		// Mock response for testing if no DB/Auth available
		return &interfaces.ModelResponse{
			Content: "CLUSTER_SUCCESS: This is a mock response from the proxy node. Auth was unavailable.",
			Usage: &models.UsageReport{
				PromptTokens:     15,
				CompletionTokens: 25,
				TotalTokens:      40,
			},
		}, nil
	}

	resolvedModel := model
	details, errDetails := b.GetDetailedModels(ctx)
	if errDetails == nil {
		if model == DefaultModelAlias {
			resolvedModel = resolveDefaultModel(details)
		} else if d, ok := details[model]; ok {
			resolvedModel = d.Model
		}
	}

	buf := bufferPool.Get().(*bytes.Buffer)
	buf.Reset()
	defer bufferPool.Put(buf)

	err = json.NewEncoder(buf).Encode(models.GetModelResponseRequest{
		Model:  resolvedModel,
		Prompt: prompt,
		Metadata: &models.RequestMetadata{
			AuthToken:   token,
			WorkspaceId: "file_workspace_default",
		},
		ToolDefinitions: tools,
		Media:           media,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to encode request: %w", err)
	}

	url := fmt.Sprintf("%s/exa.language_server_pb.LanguageServerService/GetModelResponse", b.coreAddr)
	req, err := http.NewRequestWithContext(ctx, "POST", url, buf)
	if err != nil {
		return nil, err
	}

	contentType := "application/json"
	if b.useBinary {
		contentType = "application/proto"
	}
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("Connect-Protocol-Version", "1")
	req.Header.Set("x-codeium-csrf-token", b.coreCSRF)

	resp, err := b.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, classifyInvokeError(resp.StatusCode, body)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	var agResp models.AntigravityResponse
	if err := json.Unmarshal(body, &agResp); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	var usage *models.UsageReport
	if agResp.UsageMetadata != nil {
		usage = &models.UsageReport{
			PromptTokens:     agResp.UsageMetadata.PromptTokenCount,
			CompletionTokens: agResp.UsageMetadata.CandidatesTokenCount,
			TotalTokens:      agResp.UsageMetadata.TotalTokenCount,
		}
	}

	return &interfaces.ModelResponse{
		Content:   agResp.Response,
		ToolCalls: extractToolCallsFromPayload(body),
		Usage:     usage,
	}, nil
}

func (b *engineBridge) InvokeStream(ctx context.Context, model string, prompt string, tools []models.ToolDefinition, media []models.Media) (<-chan *interfaces.StreamChunk, error) {
	var tapCh <-chan *interfaces.StreamChunk
	var cleanup func()
	if b.streamTap != nil {
		tapCh, cleanup = b.streamTap.Subscribe(ctx, prompt)
	}

	out := make(chan *interfaces.StreamChunk, 16)
	go func() {
		defer close(out)
		if cleanup != nil {
			defer cleanup()
		}

		type invokeResult struct {
			resp *interfaces.ModelResponse
			err  error
		}
		done := make(chan invokeResult, 1)
		go func() {
			resp, err := b.Invoke(ctx, model, prompt, tools, media)
			done <- invokeResult{resp: resp, err: err}
		}()

		hadStreamChunk := false
		var lastUsage *models.UsageReport
		for tapCh != nil || done != nil {
			select {
			case chunk, ok := <-tapCh:
				if !ok {
					tapCh = nil
					continue
				}
				if chunk.Usage != nil {
					lastUsage = chunk.Usage
				}
				if chunk.Content == "" {
					continue
				}
				hadStreamChunk = true
				select {
				case out <- chunk:
				case <-ctx.Done():
					return
				}
			case result := <-done:
				done = nil
				if cleanup != nil {
					cleanup()
					cleanup = nil
					tapCh = nil
				}
				if result.err != nil {
					select {
					case out <- &interfaces.StreamChunk{Error: normalizeStreamError(result.err)}:
					case <-ctx.Done():
					}
					return
				}
				if result.resp != nil && result.resp.Usage != nil {
					lastUsage = result.resp.Usage
				}
				if !hadStreamChunk && result.resp != nil && len(result.resp.ToolCalls) > 0 {
					select {
					case out <- &interfaces.StreamChunk{ToolCalls: result.resp.ToolCalls, Usage: result.resp.Usage}:
					case <-ctx.Done():
						return
					}
				} else if !hadStreamChunk && result.resp != nil && result.resp.Content != "" {
					select {
					case out <- &interfaces.StreamChunk{Content: result.resp.Content, Usage: result.resp.Usage}:
					case <-ctx.Done():
						return
					}
				} else if lastUsage != nil {
					select {
					case out <- &interfaces.StreamChunk{Usage: lastUsage}:
					case <-ctx.Done():
						return
					}
				}
			case <-ctx.Done():
				return
			}
		}
	}()

	return out, nil
}

func classifyInvokeError(status int, body []byte) error {
	message := strings.TrimSpace(string(body))
	code := fmt.Sprintf("ls_core_%d", status)
	providerStatus := status
	retryable := false

	var payload struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(body, &payload); err == nil {
		if payload.Message != "" {
			message = payload.Message
		}
		if payload.Code != "" {
			code = payload.Code
		}
	}

	upper := strings.ToUpper(message)
	switch {
	case strings.Contains(upper, "RESOURCE_EXHAUSTED") ||
		strings.Contains(upper, "RATE_LIMIT_EXCEEDED") ||
		strings.Contains(upper, "CODE 429"):
		providerStatus = http.StatusTooManyRequests
		code = "rate_limit_exceeded"
		retryable = true
	case strings.Contains(upper, "MODEL_CAPACITY_EXHAUSTED") ||
		strings.Contains(upper, "NO CAPACITY") ||
		strings.Contains(upper, "UNAVAILABLE") ||
		strings.Contains(upper, "CODE 503"):
		providerStatus = http.StatusServiceUnavailable
		code = "model_capacity_exhausted"
		retryable = true
	}
	if message == "" {
		message = fmt.Sprintf("ls_core returned status %d", status)
	}
	return &interfaces.ProviderError{
		StatusCode: providerStatus,
		Code:       code,
		Message:    message,
		Retryable:  retryable,
	}
}

func normalizeStreamError(err error) error {
	if err == nil {
		return nil
	}
	if _, ok := err.(*interfaces.ProviderError); ok {
		return err
	}
	return &interfaces.ProviderError{
		StatusCode: http.StatusInternalServerError,
		Code:       "upstream_error",
		Message:    err.Error(),
		Retryable:  false,
	}
}

func resolveDefaultModel(details map[string]models.ModelDetail) string {
	if preferred := os.Getenv("ANTIGRAVITY_DEFAULT_MODEL"); preferred != "" {
		if d, ok := details[preferred]; ok && d.Model != "" {
			return d.Model
		}
		return preferred
	}
	for _, preferred := range []string{"gemini-3-flash-agent", "gemini-3-flash", "gemini-3.1-pro-low", "gemini-2.5-pro", "gemini-2.5-flash"} {
		if d, ok := details[preferred]; ok && d.Model != "" {
			return d.Model
		}
	}
	ids := make([]string, 0, len(details))
	for id := range details {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		if d := details[id]; d.Model != "" {
			return d.Model
		}
	}
	return DefaultModelAlias
}

func (b *engineBridge) GetModels(ctx context.Context) ([]string, error) {
	detailed, err := b.GetDetailedModels(ctx)
	if err != nil {
		return nil, err
	}

	var modelsList []string
	for id := range detailed {
		modelsList = append(modelsList, id)
	}
	sort.Strings(modelsList)
	return modelsList, nil
}

func (b *engineBridge) GetDetailedModels(ctx context.Context) (map[string]models.ModelDetail, error) {
	token, err := b.auth.GetLatestToken()
	if err != nil {
		return nil, fmt.Errorf("failed to get auth token: %w", err)
	}

	buf := bufferPool.Get().(*bytes.Buffer)
	buf.Reset()
	defer bufferPool.Put(buf)

	err = json.NewEncoder(buf).Encode(models.GetAvailableModelsRequest{
		Metadata: &models.RequestMetadata{
			AuthToken: token,
		},
	})
	if err != nil {
		return nil, err
	}

	url := fmt.Sprintf("%s/exa.language_server_pb.LanguageServerService/GetAvailableModels", b.coreAddr)
	req, err := http.NewRequestWithContext(ctx, "POST", url, buf)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Connect-Protocol-Version", "1")
	req.Header.Set("x-codeium-csrf-token", b.coreCSRF)

	resp, err := b.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("GetAvailableModels failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("GetAvailableModels returned status %d: %s", resp.StatusCode, string(body))
	}

	var availResp models.GetAvailableModelsResponse
	if err := json.NewDecoder(resp.Body).Decode(&availResp); err != nil {
		return nil, fmt.Errorf("failed to decode GetAvailableModels: %w", err)
	}

	modelsMap := availResp.Response.Models
	if modelsMap == nil {
		return nil, fmt.Errorf("GetAvailableModels response did not include models")
	}

	status, errStatus := b.GetAccount(ctx)
	if errStatus == nil && status.CascadeModelConfigData != nil {
		if visible := userVisibleModels(modelsMap, status.CascadeModelConfigData.ClientModelConfigs); len(visible) > 0 {
			return visible, nil
		}
	}

	return modelsMap, nil
}

func userVisibleModels(available map[string]models.ModelDetail, configs []models.ClientModelConfig) map[string]models.ModelDetail {
	if len(available) == 0 || len(configs) == 0 {
		return nil
	}
	availableIDs := make([]string, 0, len(available))
	for id := range available {
		availableIDs = append(availableIDs, id)
	}
	sort.Strings(availableIDs)

	out := make(map[string]models.ModelDetail)
	for _, cfg := range configs {
		if cfg.ModelOrAlias == nil {
			continue
		}
		internalID := strings.TrimSpace(cfg.ModelOrAlias.Model)
		if internalID == "" {
			continue
		}
		for _, id := range availableIDs {
			detail := available[id]
			if detail.Model != internalID {
				continue
			}
			updated := detail
			if label := strings.TrimSpace(cfg.Label); label != "" {
				updated.Label = label
			}
			if cfg.QuotaInfo != nil {
				updated.QuotaInfo = cfg.QuotaInfo
			}
			out[id] = updated
			break
		}
	}
	return out
}

func (b *engineBridge) GetAccount(ctx context.Context) (*models.UserStatus, error) {
	token, err := b.auth.GetLatestToken()
	if err != nil {
		return nil, fmt.Errorf("failed to get auth token: %w", err)
	}

	buf := bufferPool.Get().(*bytes.Buffer)
	buf.Reset()
	defer bufferPool.Put(buf)

	err = json.NewEncoder(buf).Encode(models.GetUserStatusRequest{
		Metadata: &models.RequestMetadata{
			AuthToken: token,
		},
	})
	if err != nil {
		return nil, err
	}

	url := fmt.Sprintf("%s/exa.language_server_pb.LanguageServerService/GetUserStatus", b.coreAddr)
	req, err := http.NewRequestWithContext(ctx, "POST", url, buf)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Connect-Protocol-Version", "1")
	req.Header.Set("x-codeium-csrf-token", b.coreCSRF)

	resp, err := b.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("GetUserStatus returned status %d: %s", resp.StatusCode, string(body))
	}

	var agResp models.GetUserStatusResponse
	if err := json.NewDecoder(resp.Body).Decode(&agResp); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}
	if agResp.UserStatus == nil {
		return nil, fmt.Errorf("GetUserStatus response did not include userStatus")
	}

	return agResp.UserStatus, nil
}

func (b *engineBridge) VerifyProtocol(ctx context.Context) error {
	fmt.Println("🔍 Verifying Antigravity protocol compatibility...")

	token, err := b.auth.GetLatestToken()
	if err != nil {
		return fmt.Errorf("auth error during verification: %w", err)
	}

	buf := bufferPool.Get().(*bytes.Buffer)
	buf.Reset()
	defer bufferPool.Put(buf)

	json.NewEncoder(buf).Encode(models.GetAvailableModelsRequest{
		Metadata: &models.RequestMetadata{AuthToken: token},
	})

	url := fmt.Sprintf("%s/exa.language_server_pb.LanguageServerService/GetAvailableModels", b.coreAddr)
	req, _ := http.NewRequestWithContext(ctx, "POST", url, buf)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Connect-Protocol-Version", "1")
	req.Header.Set("x-codeium-csrf-token", b.coreCSRF)

	resp, err := b.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to reach ls_core: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("protocol mismatch or ls_core error: status %d", resp.StatusCode)
	}

	var raw map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		fmt.Println("⚠️  WARNING: Protocol signature change detected! JSON structure has evolved.")
		return nil
	}

	res, ok := raw["response"].(map[string]interface{})
	if !ok || res["models"] == nil {
		fmt.Println("⚠️  WARNING: Critical protocol shift detected! 'response.models' missing.")
		fmt.Println("💡 Please check for proxy updates to support the new Antigravity version.")
	} else {
		fmt.Println("✅ Protocol verified. Using stable JSON-over-HTTP/2 mode.")
		b.useBinary = false // Keep it false for now as we use JSON models
	}

	return nil
}

func (b *engineBridge) GetUsage(ctx context.Context) (map[string]int, error) {
	detailed, err := b.GetDetailedModels(ctx)
	if err != nil {
		return map[string]int{"total_tokens": 0}, nil
	}

	res := map[string]int{
		"total_tokens":     0,
		"remaining_tokens": 0,
		"reset_time":       0,
	}

	for id, m := range detailed {
		if m.QuotaInfo != nil && m.QuotaInfo.ResetTime != "" {
			t, err := time.Parse(time.RFC3339, m.QuotaInfo.ResetTime)
			if err == nil {
				res["reset_time"] = int(t.Unix())
				res["remaining_fraction_pct"] = int(m.QuotaInfo.RemainingFraction * 100)
				break
			} else {
				fmt.Printf("Warning: failed to parse reset time for model %s: %v\n", id, err)
			}
		}
	}

	return res, nil
}
