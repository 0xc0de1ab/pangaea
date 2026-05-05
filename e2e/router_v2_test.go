package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/0xc0de1ab/pangaea/internal/apiprovider"
	"github.com/0xc0de1ab/pangaea/internal/compat"
	"github.com/0xc0de1ab/pangaea/internal/control"
	"github.com/0xc0de1ab/pangaea/internal/nodeagent"
	"github.com/0xc0de1ab/pangaea/internal/provider"
	"github.com/0xc0de1ab/pangaea/internal/providershim"
	"github.com/0xc0de1ab/pangaea/internal/providersim"
	"github.com/0xc0de1ab/pangaea/internal/quota"
	v2router "github.com/0xc0de1ab/pangaea/internal/router"
)

func TestE2E_V2RouterShimDataPlanePublicDialects(t *testing.T) {
	tokenKey := []byte("test-v2-stream-token-key")
	policy, err := v2router.ParseRoutingPolicyYAML([]byte(routerV2E2EPolicy))
	if err != nil {
		t.Fatalf("parse policy: %v", err)
	}
	engine, err := v2router.NewEngine(policy, provider.NewRegistry(), quota.NewLedger())
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	dataBroker, err := v2router.NewDataBroker(tokenKey)
	if err != nil {
		t.Fatalf("new data broker: %v", err)
	}
	engine.SetInvoker(dataBroker)

	server := httptest.NewServer(v2router.NewHTTPHandler(v2router.HTTPOptions{
		Engine:     engine,
		DataBroker: dataBroker,
	}))
	defer server.Close()

	registration := routerV2E2ERegistration(time.Now())
	sim, err := providersim.New(providersim.Options{Mode: providersim.ModeAPICompatible, Registration: registration})
	if err != nil {
		t.Fatalf("new simulator: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	shimDone := make(chan error, 1)
	go func() {
		shimDone <- providershim.RunSimulatorShim(ctx, providershim.SimulatorShimOptions{
			ControlURL:        httpURLToWS(server.URL) + "/router/v1/control/ws",
			HeartbeatInterval: 20 * time.Millisecond,
			TokenKey:          tokenKey,
			Simulator:         sim,
		})
	}()
	defer func() {
		cancel()
		select {
		case err := <-shimDone:
			if err != nil {
				t.Fatalf("shim exited with error: %v", err)
			}
		case <-time.After(3 * time.Second):
			t.Fatalf("shim did not stop")
		}
	}()

	client := &http.Client{Timeout: 2 * time.Second}
	waitForV2Provider(t, client, server.URL, registration.Identity.ProviderInstanceID)
	node := waitForV2Node(t, client, server.URL, registration.Identity.NodeID)
	if node.HostName != "providersim-host" {
		t.Fatalf("unexpected node snapshot: %#v", node)
	}
	sim.SetAuthStatus(provider.AuthRefreshSoon)
	waitForV2ProviderAuth(t, client, server.URL, registration.Identity.ProviderInstanceID, provider.AuthRefreshSoon)
	refresh := requestV2AuthRefresh(t, client, server.URL, registration.Identity.ProviderInstanceID)
	if !refresh.OK || refresh.Auth.Status != provider.AuthHealthy {
		t.Fatalf("unexpected auth refresh result: %#v", refresh)
	}
	waitForV2ProviderAuth(t, client, server.URL, registration.Identity.ProviderInstanceID, provider.AuthHealthy)
	setV2QuotaLimit(t, client, server.URL, quota.Scope{APIKeyID: "req_e2e_v2_data", Model: "providersim-default"}, quota.Limit{MaxTokens: 1000, MaxRequests: 10})
	response := waitForV2OpenAIChat(t, client, server.URL)
	if len(response.Choices) != 1 || response.Choices[0].Message.Content != "providersim: e2e hello" {
		t.Fatalf("unexpected chat response: %#v", response)
	}
	if response.Model != "gpt-5-sim" {
		t.Fatalf("expected canonical model gpt-5-sim, got %q", response.Model)
	}
	if response.Usage == nil || response.Usage.TotalTokens == 0 {
		t.Fatalf("expected usage in response, got %#v", response.Usage)
	}
	trace := waitForV2Trace(t, client, server.URL, "req_e2e_v2_data")
	if trace.Status != "completed" || trace.Provider == nil || trace.Provider.HostName != "providersim-host" {
		t.Fatalf("unexpected request trace: %#v", trace)
	}
	snapshot := getV2QuotaSnapshot(t, client, server.URL, quota.Scope{APIKeyID: "req_e2e_v2_data", Model: "providersim-default"})
	if snapshot.Committed.Tokens == 0 || snapshot.Limit.MaxTokens != 1000 {
		t.Fatalf("unexpected quota snapshot: %#v", snapshot)
	}
	streamBody := waitForV2OpenAIChatStream(t, client, server.URL)
	if !strings.Contains(streamBody, "providersim: e2e stream") || !strings.Contains(streamBody, "data: [DONE]") {
		t.Fatalf("unexpected OpenAI stream body: %s", streamBody)
	}
	anthropic := waitForV2AnthropicMessages(t, client, server.URL)
	if len(anthropic.Content) != 1 || anthropic.Content[0].Text != "providersim: e2e anthropic" {
		t.Fatalf("unexpected Anthropic response: %#v", anthropic)
	}
	gemini := waitForV2GeminiGenerateContent(t, client, server.URL)
	if len(gemini.Candidates) != 1 || gemini.Candidates[0].Content.Parts[0].Text != "providersim: e2e gemini" {
		t.Fatalf("unexpected Gemini response: %#v", gemini)
	}
	usage := waitForV2ProviderUsage(t, client, server.URL, registration.Identity.ProviderInstanceID)
	if usage.HostName != "providersim-host" || usage.Account.Display != "providersim@example.test" {
		t.Fatalf("usage lost provider host/account dimensions: %#v", usage)
	}
}

func TestE2E_V2RouterDataPlaneFallbackWhenSelectedSessionMissing(t *testing.T) {
	tokenKey := []byte("test-v2-stream-token-key")
	policy, err := v2router.ParseRoutingPolicyYAML([]byte(routerV2FallbackE2EPolicy))
	if err != nil {
		t.Fatalf("parse policy: %v", err)
	}
	registry := provider.NewRegistry()
	missing := routerV2E2ERegistration(time.Now())
	missing.Identity.ProviderID = "providersim-missing"
	missing.Identity.ProviderInstanceID = "providersim-missing-0001"
	missing.Identity.HostName = "missing-host"
	missing.Identity.Account = provider.Account{ID: "acct-missing", Display: "missing@example.test"}
	missing.Auth.Account = missing.Identity.Account
	if err := registry.Upsert(missing); err != nil {
		t.Fatalf("upsert missing provider: %v", err)
	}
	engine, err := v2router.NewEngine(policy, registry, quota.NewLedger())
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	dataBroker, err := v2router.NewDataBroker(tokenKey)
	if err != nil {
		t.Fatalf("new data broker: %v", err)
	}
	engine.SetInvoker(dataBroker)

	server := httptest.NewServer(v2router.NewHTTPHandler(v2router.HTTPOptions{
		Engine:     engine,
		DataBroker: dataBroker,
	}))
	defer server.Close()

	available := routerV2E2ERegistration(time.Now())
	sim, err := providersim.New(providersim.Options{Mode: providersim.ModeAPICompatible, Registration: available})
	if err != nil {
		t.Fatalf("new simulator: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	shimDone := make(chan error, 1)
	go func() {
		shimDone <- providershim.RunSimulatorShim(ctx, providershim.SimulatorShimOptions{
			ControlURL:        httpURLToWS(server.URL) + "/router/v1/control/ws",
			HeartbeatInterval: 20 * time.Millisecond,
			TokenKey:          tokenKey,
			Simulator:         sim,
		})
	}()
	defer func() {
		cancel()
		select {
		case err := <-shimDone:
			if err != nil {
				t.Fatalf("shim exited with error: %v", err)
			}
		case <-time.After(3 * time.Second):
			t.Fatalf("shim did not stop")
		}
	}()

	client := &http.Client{Timeout: 2 * time.Second}
	waitForV2Provider(t, client, server.URL, available.Identity.ProviderInstanceID)
	response := waitForV2OpenAIChat(t, client, server.URL)
	if len(response.Choices) != 1 || response.Choices[0].Message.Content != "providersim: e2e hello" {
		t.Fatalf("unexpected fallback response: %#v", response)
	}
	trace := waitForV2Trace(t, client, server.URL, "req_e2e_v2_data")
	if trace.Provider == nil || trace.Provider.ProviderInstanceID != available.Identity.ProviderInstanceID {
		t.Fatalf("trace did not record fallback provider: %#v", trace)
	}
	if len(trace.Decision.Rejections) == 0 || !strings.Contains(trace.Decision.Rejections[len(trace.Decision.Rejections)-1].Reason, "router data session not found") {
		t.Fatalf("trace did not record missing data session rejection: %#v", trace.Decision.Rejections)
	}
}

func TestE2E_V2NodeAgentProviderInventory(t *testing.T) {
	policy, err := v2router.ParseRoutingPolicyYAML([]byte(routerV2E2EPolicy))
	if err != nil {
		t.Fatalf("parse policy: %v", err)
	}
	engine, err := v2router.NewEngine(policy, provider.NewRegistry(), quota.NewLedger())
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	server := httptest.NewServer(v2router.NewHTTPHandler(v2router.HTTPOptions{Engine: engine}))
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- nodeagent.RunControlClient(ctx, nodeagent.ControlClientOptions{
			ControlURL:        httpURLToWS(server.URL) + "/router/v1/control/ws",
			NodeID:            "node-a1",
			HostName:          "snowbox",
			HeartbeatInterval: 20 * time.Millisecond,
			Runtime:           control.RuntimeInfo{Kind: "docker", Version: "26.1.0"},
			ProviderSpecs: []nodeagent.ProviderSpec{{
				ID:          "codex-samtest",
				InstanceID:  "codex-samtest-0001",
				Kind:        provider.KindCLIContainer,
				Image:       "pangaea/provider-codex:test",
				AccountHint: "samtest4u@gmail.com",
				Service:     provider.ServiceCodex,
				Shim:        nodeagent.ShimSpec{Capabilities: []provider.Capability{provider.CapabilityOpenAIChat, provider.CapabilityAuthRefreshOneshot}},
			}, {
				ID:          "gemini-nullcode",
				InstanceID:  "gemini-nullcode-0001",
				Kind:        provider.KindCLIContainer,
				Image:       "pangaea/provider-gemini:test",
				AccountHint: "nullcode@gmail.com",
				Service:     provider.ServiceGemini,
				Shim:        nodeagent.ShimSpec{Capabilities: []provider.Capability{provider.CapabilityGeminiGenerateContent, provider.CapabilityAuthRefreshOneshot}},
			}},
		})
	}()
	defer func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("node agent exited with error: %v", err)
			}
		case <-time.After(3 * time.Second):
			t.Fatalf("node agent did not stop")
		}
	}()

	client := &http.Client{Timeout: 2 * time.Second}
	waitForV2Provider(t, client, server.URL, "codex-samtest-0001")
	waitForV2Provider(t, client, server.URL, "gemini-nullcode-0001")
	node := waitForV2Node(t, client, server.URL, "node-a1")
	if node.HostName != "snowbox" || node.Runtime.Kind != "docker" {
		t.Fatalf("unexpected node inventory: %#v", node)
	}
	containers := waitForV2Containers(t, client, server.URL, 2)
	seen := map[string]bool{}
	for _, container := range containers {
		if container.HostName != "snowbox" {
			t.Fatalf("container inventory lost host name: %#v", containers)
		}
		seen[container.ProviderInstanceID] = true
	}
	if !seen["codex-samtest-0001"] || !seen["gemini-nullcode-0001"] {
		t.Fatalf("expected both provider containers, got %#v", containers)
	}
}

func TestE2E_V2APICompatibleProviderShimOpenAI(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Fatalf("unexpected upstream path: %s", r.URL.Path)
		}
		var request compat.OpenAIChatRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatalf("decode upstream request: %v", err)
		}
		if request.Model != "deepseek-chat" || len(request.Messages) != 1 || request.Messages[0].Content != "api provider hello" {
			t.Fatalf("unexpected upstream request: %#v", request)
		}
		_ = json.NewEncoder(w).Encode(compat.OpenAIChatResponse{
			ID:     "chatcmpl-api-provider",
			Object: "chat.completion",
			Model:  "deepseek-chat",
			Choices: []compat.OpenAIChatChoice{{
				Index:        0,
				Message:      compat.OpenAIChatMessage{Role: "assistant", Content: "api-compatible: ok"},
				FinishReason: "stop",
			}},
			Usage: &compat.OpenAIUsage{PromptTokens: 11, CompletionTokens: 7, TotalTokens: 18},
		})
	}))
	defer upstream.Close()

	tokenKey := []byte("test-v2-api-compatible-token-key")
	policy, err := v2router.ParseRoutingPolicyYAML([]byte(routerV2APICompatiblePolicy))
	if err != nil {
		t.Fatalf("parse policy: %v", err)
	}
	engine, err := v2router.NewEngine(policy, provider.NewRegistry(), quota.NewLedger())
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	dataBroker, err := v2router.NewDataBroker(tokenKey)
	if err != nil {
		t.Fatalf("new data broker: %v", err)
	}
	engine.SetInvoker(dataBroker)
	server := httptest.NewServer(v2router.NewHTTPHandler(v2router.HTTPOptions{Engine: engine, DataBroker: dataBroker}))
	defer server.Close()

	apiProvider, err := apiprovider.New(apiprovider.Options{
		Registration: apiCompatibleE2ERegistration(time.Now()),
		BaseURL:      upstream.URL,
		Dialect:      compat.APIDialectOpenAI,
		APIKey:       "sk_test_e2e",
	})
	if err != nil {
		t.Fatalf("new api provider: %v", err)
	}
	registration, err := apiProvider.Registration()
	if err != nil {
		t.Fatalf("api provider registration: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- providershim.RunAPICompatibleShim(ctx, providershim.APICompatibleShimOptions{
			ControlURL:        httpURLToWS(server.URL) + "/router/v1/control/ws",
			HeartbeatInterval: 20 * time.Millisecond,
			TokenKey:          tokenKey,
			Provider:          apiProvider,
		})
	}()
	defer func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("api-compatible shim exited with error: %v", err)
			}
		case <-time.After(3 * time.Second):
			t.Fatalf("api-compatible shim did not stop")
		}
	}()

	client := &http.Client{Timeout: 2 * time.Second}
	waitForV2Provider(t, client, server.URL, registration.Identity.ProviderInstanceID)
	response := waitForV2OpenAIChatModel(t, client, server.URL, "deepseek-default", "api provider hello", "req_e2e_v2_api_provider")
	if response.Choices[0].Message.Content != "api-compatible: ok" || response.Usage == nil || response.Usage.TotalTokens != 18 {
		t.Fatalf("unexpected api-compatible response: %#v", response)
	}
	trace := waitForV2Trace(t, client, server.URL, "req_e2e_v2_api_provider")
	if trace.Provider == nil || trace.Provider.ProviderInstanceID != registration.Identity.ProviderInstanceID {
		t.Fatalf("unexpected api-compatible trace: %#v", trace)
	}
}

func waitForV2Provider(t *testing.T, client *http.Client, baseURL string, providerInstanceID string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := client.Get(baseURL + "/router/v1/providers")
		if err == nil {
			var out struct {
				Providers []provider.Registration `json:"providers"`
			}
			if decodeErr := json.NewDecoder(resp.Body).Decode(&out); decodeErr == nil {
				_ = resp.Body.Close()
				for _, registration := range out.Providers {
					if registration.Identity.ProviderInstanceID == providerInstanceID {
						return
					}
				}
			} else {
				_ = resp.Body.Close()
			}
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("provider %q did not register", providerInstanceID)
}

func waitForV2Containers(t *testing.T, client *http.Client, baseURL string, minCount int) []v2router.ContainerSnapshot {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		resp, err := client.Get(baseURL + "/router/v1/containers")
		if err != nil {
			lastErr = err
			time.Sleep(25 * time.Millisecond)
			continue
		}
		data, readErr := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if readErr != nil {
			lastErr = readErr
			time.Sleep(25 * time.Millisecond)
			continue
		}
		if resp.StatusCode != http.StatusOK {
			lastErr = fmt.Errorf("status=%d body=%s", resp.StatusCode, string(data))
			time.Sleep(25 * time.Millisecond)
			continue
		}
		var out struct {
			Containers []v2router.ContainerSnapshot `json:"containers"`
		}
		if err := json.Unmarshal(data, &out); err != nil {
			t.Fatalf("decode containers response: %v body=%s", err, string(data))
		}
		if len(out.Containers) >= minCount {
			return out.Containers
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("container inventory did not reach %d entries: %v", minCount, lastErr)
	return nil
}

func waitForV2ProviderAuth(t *testing.T, client *http.Client, baseURL string, providerInstanceID string, status provider.AuthStatus) provider.Registration {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		resp, err := client.Get(baseURL + "/router/v1/providers")
		if err != nil {
			lastErr = err
			time.Sleep(25 * time.Millisecond)
			continue
		}
		data, readErr := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if readErr != nil {
			lastErr = readErr
			time.Sleep(25 * time.Millisecond)
			continue
		}
		if resp.StatusCode != http.StatusOK {
			lastErr = fmt.Errorf("status=%d body=%s", resp.StatusCode, string(data))
			time.Sleep(25 * time.Millisecond)
			continue
		}
		var out struct {
			Providers []provider.Registration `json:"providers"`
		}
		if err := json.Unmarshal(data, &out); err != nil {
			t.Fatalf("decode providers response: %v body=%s", err, string(data))
		}
		for _, registration := range out.Providers {
			if registration.Identity.ProviderInstanceID == providerInstanceID && registration.Auth.Status == status {
				return registration
			}
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("provider %q auth status did not become %q: %v", providerInstanceID, status, lastErr)
	return provider.Registration{}
}

func requestV2AuthRefresh(t *testing.T, client *http.Client, baseURL string, providerInstanceID string) control.AuthRefreshResult {
	t.Helper()
	body := bytes.NewBufferString(`{"refresh_id":"refresh_e2e","reason":"e2e","timeout_seconds":2}`)
	req, err := http.NewRequest(http.MethodPost, baseURL+"/router/v1/providers/"+providerInstanceID+"/auth/refresh", body)
	if err != nil {
		t.Fatalf("new auth refresh request: %v", err)
	}
	req.Header.Set("content-type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("auth refresh request: %v", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read auth refresh response: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("auth refresh status=%d body=%s", resp.StatusCode, string(data))
	}
	var result control.AuthRefreshResult
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatalf("decode auth refresh response: %v body=%s", err, string(data))
	}
	return result
}

func waitForV2Node(t *testing.T, client *http.Client, baseURL string, nodeID string) v2router.NodeSnapshot {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		resp, err := client.Get(baseURL + "/router/v1/nodes")
		if err != nil {
			lastErr = err
			time.Sleep(25 * time.Millisecond)
			continue
		}
		data, readErr := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if readErr != nil {
			lastErr = readErr
			time.Sleep(25 * time.Millisecond)
			continue
		}
		if resp.StatusCode != http.StatusOK {
			lastErr = fmt.Errorf("status=%d body=%s", resp.StatusCode, string(data))
			time.Sleep(25 * time.Millisecond)
			continue
		}
		var out struct {
			Nodes []v2router.NodeSnapshot `json:"nodes"`
		}
		if err := json.Unmarshal(data, &out); err != nil {
			t.Fatalf("decode nodes response: %v body=%s", err, string(data))
		}
		for _, node := range out.Nodes {
			if node.NodeID == nodeID && !node.LastInventoryAt.IsZero() {
				return node
			}
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("node %q did not report inventory: %v", nodeID, lastErr)
	return v2router.NodeSnapshot{}
}

func setV2QuotaLimit(t *testing.T, client *http.Client, baseURL string, scope quota.Scope, limit quota.Limit) {
	t.Helper()
	body, err := json.Marshal(struct {
		Scope quota.Scope `json:"scope"`
		Limit quota.Limit `json:"limit"`
	}{Scope: scope, Limit: limit})
	if err != nil {
		t.Fatalf("marshal quota limit: %v", err)
	}
	req, err := http.NewRequest(http.MethodPut, baseURL+"/router/v1/quotas/limits", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("new quota request: %v", err)
	}
	req.Header.Set("content-type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("set quota limit: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(resp.Body)
		t.Fatalf("set quota limit status=%d body=%s", resp.StatusCode, string(data))
	}
}

func getV2QuotaSnapshot(t *testing.T, client *http.Client, baseURL string, scope quota.Scope) quota.SnapshotRecord {
	t.Helper()
	body, err := json.Marshal(scope)
	if err != nil {
		t.Fatalf("marshal quota scope: %v", err)
	}
	req, err := http.NewRequest(http.MethodPost, baseURL+"/router/v1/quotas/snapshot", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("new quota snapshot request: %v", err)
	}
	req.Header.Set("content-type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("get quota snapshot: %v", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read quota snapshot: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("quota snapshot status=%d body=%s", resp.StatusCode, string(data))
	}
	var snapshot quota.SnapshotRecord
	if err := json.Unmarshal(data, &snapshot); err != nil {
		t.Fatalf("decode quota snapshot: %v body=%s", err, string(data))
	}
	return snapshot
}

func waitForV2OpenAIChat(t *testing.T, client *http.Client, baseURL string) compat.OpenAIChatResponse {
	t.Helper()
	return waitForV2OpenAIChatModel(t, client, baseURL, "providersim-default", "e2e hello", "req_e2e_v2_data")
}

func waitForV2OpenAIChatModel(t *testing.T, client *http.Client, baseURL string, model string, content string, requestID string) compat.OpenAIChatResponse {
	t.Helper()
	body, err := json.Marshal(compat.OpenAIChatRequest{
		Model:    model,
		Messages: []compat.OpenAIChatMessage{{Role: "user", Content: content}},
	})
	if err != nil {
		t.Fatalf("marshal OpenAI chat request: %v", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		req, err := http.NewRequest(http.MethodPost, baseURL+"/v1/chat/completions", bytes.NewReader(body))
		if err != nil {
			t.Fatalf("new request: %v", err)
		}
		req.Header.Set("content-type", "application/json")
		req.Header.Set("x-request-id", requestID)
		resp, err := client.Do(req)
		if err != nil {
			lastErr = err
			time.Sleep(25 * time.Millisecond)
			continue
		}
		data, readErr := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if readErr != nil {
			lastErr = readErr
			time.Sleep(25 * time.Millisecond)
			continue
		}
		if resp.StatusCode == http.StatusOK {
			var response compat.OpenAIChatResponse
			if err := json.Unmarshal(data, &response); err != nil {
				t.Fatalf("decode chat response: %v body=%s", err, string(data))
			}
			return response
		}
		lastErr = fmt.Errorf("status=%d body=%s", resp.StatusCode, string(data))
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("chat completion did not succeed: %v", lastErr)
	return compat.OpenAIChatResponse{}
}

func waitForV2OpenAIChatStream(t *testing.T, client *http.Client, baseURL string) string {
	t.Helper()
	body := []byte(`{"model":"providersim-default","stream":true,"messages":[{"role":"user","content":"e2e stream"}]}`)
	deadline := time.Now().Add(5 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		req, err := http.NewRequest(http.MethodPost, baseURL+"/v1/chat/completions", bytes.NewReader(body))
		if err != nil {
			t.Fatalf("new request: %v", err)
		}
		req.Header.Set("content-type", "application/json")
		req.Header.Set("x-request-id", "req_e2e_v2_stream")
		resp, err := client.Do(req)
		if err != nil {
			lastErr = err
			time.Sleep(25 * time.Millisecond)
			continue
		}
		data, readErr := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if readErr != nil {
			lastErr = readErr
			time.Sleep(25 * time.Millisecond)
			continue
		}
		if resp.StatusCode == http.StatusOK {
			return string(data)
		}
		lastErr = fmt.Errorf("status=%d body=%s", resp.StatusCode, string(data))
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("OpenAI stream did not succeed: %v", lastErr)
	return ""
}

func waitForV2AnthropicMessages(t *testing.T, client *http.Client, baseURL string) compat.AnthropicMessagesResponse {
	t.Helper()
	body := []byte(`{"model":"claude-default","max_tokens":64,"messages":[{"role":"user","content":"e2e anthropic"}]}`)
	deadline := time.Now().Add(5 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		req, err := http.NewRequest(http.MethodPost, baseURL+"/v1/messages", bytes.NewReader(body))
		if err != nil {
			t.Fatalf("new request: %v", err)
		}
		req.Header.Set("content-type", "application/json")
		req.Header.Set("x-request-id", "req_e2e_v2_anthropic")
		resp, err := client.Do(req)
		if err != nil {
			lastErr = err
			time.Sleep(25 * time.Millisecond)
			continue
		}
		data, readErr := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if readErr != nil {
			lastErr = readErr
			time.Sleep(25 * time.Millisecond)
			continue
		}
		if resp.StatusCode == http.StatusOK {
			var response compat.AnthropicMessagesResponse
			if err := json.Unmarshal(data, &response); err != nil {
				t.Fatalf("decode Anthropic response: %v body=%s", err, string(data))
			}
			return response
		}
		lastErr = fmt.Errorf("status=%d body=%s", resp.StatusCode, string(data))
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("Anthropic message did not succeed: %v", lastErr)
	return compat.AnthropicMessagesResponse{}
}

func waitForV2GeminiGenerateContent(t *testing.T, client *http.Client, baseURL string) compat.GeminiGenerateContentResponse {
	t.Helper()
	body := []byte(`{"contents":[{"role":"user","parts":[{"text":"e2e gemini"}]}]}`)
	deadline := time.Now().Add(5 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		req, err := http.NewRequest(http.MethodPost, baseURL+"/v1beta/models/gemini-default:generateContent", bytes.NewReader(body))
		if err != nil {
			t.Fatalf("new request: %v", err)
		}
		req.Header.Set("content-type", "application/json")
		req.Header.Set("x-request-id", "req_e2e_v2_gemini")
		resp, err := client.Do(req)
		if err != nil {
			lastErr = err
			time.Sleep(25 * time.Millisecond)
			continue
		}
		data, readErr := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if readErr != nil {
			lastErr = readErr
			time.Sleep(25 * time.Millisecond)
			continue
		}
		if resp.StatusCode == http.StatusOK {
			var response compat.GeminiGenerateContentResponse
			if err := json.Unmarshal(data, &response); err != nil {
				t.Fatalf("decode Gemini response: %v body=%s", err, string(data))
			}
			return response
		}
		lastErr = fmt.Errorf("status=%d body=%s", resp.StatusCode, string(data))
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("Gemini generateContent did not succeed: %v", lastErr)
	return compat.GeminiGenerateContentResponse{}
}

func waitForV2ProviderUsage(t *testing.T, client *http.Client, baseURL string, providerInstanceID string) v2router.ProviderUsageSnapshot {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		resp, err := client.Get(baseURL + "/router/v1/usage/providers")
		if err != nil {
			lastErr = err
			time.Sleep(25 * time.Millisecond)
			continue
		}
		data, readErr := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if readErr != nil {
			lastErr = readErr
			time.Sleep(25 * time.Millisecond)
			continue
		}
		if resp.StatusCode != http.StatusOK {
			lastErr = fmt.Errorf("status=%d body=%s", resp.StatusCode, string(data))
			time.Sleep(25 * time.Millisecond)
			continue
		}
		var out struct {
			Usage []v2router.ProviderUsageSnapshot `json:"usage"`
		}
		if err := json.Unmarshal(data, &out); err != nil {
			t.Fatalf("decode usage response: %v body=%s", err, string(data))
		}
		for _, usage := range out.Usage {
			if usage.ProviderInstanceID == providerInstanceID && usage.Usage.TotalTokens > 0 {
				return usage
			}
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("provider usage did not update: %v", lastErr)
	return v2router.ProviderUsageSnapshot{}
}

func waitForV2Trace(t *testing.T, client *http.Client, baseURL string, requestID string) v2router.RequestTrace {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		resp, err := client.Get(baseURL + "/router/v1/traces/" + requestID)
		if err != nil {
			lastErr = err
			time.Sleep(25 * time.Millisecond)
			continue
		}
		data, readErr := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if readErr != nil {
			lastErr = readErr
			time.Sleep(25 * time.Millisecond)
			continue
		}
		if resp.StatusCode == http.StatusOK {
			var trace v2router.RequestTrace
			if err := json.Unmarshal(data, &trace); err != nil {
				t.Fatalf("decode trace response: %v body=%s", err, string(data))
			}
			return trace
		}
		lastErr = fmt.Errorf("status=%d body=%s", resp.StatusCode, string(data))
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("request trace did not update: %v", lastErr)
	return v2router.RequestTrace{}
}

func routerV2E2ERegistration(now time.Time) provider.Registration {
	account := provider.Account{ID: "acct-providersim", Display: "providersim@example.test"}
	return provider.Registration{
		Identity: provider.ProviderIdentity{
			ProviderID:         "providersim-multi",
			ProviderInstanceID: "providersim-multi-0001",
			NodeID:             "providersim-node",
			HostName:           "providersim-host",
			Service:            provider.ServiceOpenAI,
			Kind:               provider.KindAPICompatible,
			Account:            account,
		},
		Capabilities: []provider.Capability{
			provider.CapabilityOpenAIChat,
			provider.CapabilityAnthropicMessages,
			provider.CapabilityGeminiGenerateContent,
			provider.CapabilityStreamSSE,
			provider.CapabilityUsageRead,
			provider.CapabilityAuthRefreshOneshot,
		},
		Models: []provider.Model{
			{ID: "gpt-5-sim", Aliases: []string{"providersim-default"}, Capabilities: []provider.Capability{provider.CapabilityOpenAIChat}},
			{ID: "claude-native-sim", Aliases: []string{"claude-default"}, Capabilities: []provider.Capability{provider.CapabilityAnthropicMessages}},
			{ID: "gemini-native-sim", Aliases: []string{"gemini-default"}, Capabilities: []provider.Capability{provider.CapabilityGeminiGenerateContent}},
		},
		Health: provider.Health{
			Status:    provider.HealthReady,
			CheckedAt: now,
		},
		Auth: provider.AuthState{
			Status:        provider.AuthHealthy,
			Account:       account,
			ExpiresAt:     now.Add(time.Hour),
			Refreshable:   true,
			LastRefreshAt: now,
		},
		Limits: provider.LimitState{
			MaxConcurrency: 8,
			QueueDepth:     0,
		},
		RegisteredAt: now,
	}
}

func apiCompatibleE2ERegistration(now time.Time) provider.Registration {
	account := provider.Account{ID: "acct-deepseek", Display: "deepseek-api@example.test"}
	return provider.Registration{
		Identity: provider.ProviderIdentity{
			ProviderID:         "deepseek-api",
			ProviderInstanceID: "deepseek-api-0001",
			NodeID:             "api-node",
			HostName:           "api-host",
			Service:            provider.ServiceDeepSeek,
			Kind:               provider.KindAPICompatible,
			Account:            account,
		},
		Capabilities: []provider.Capability{
			provider.CapabilityOpenAIChat,
			provider.CapabilityUsageRead,
		},
		Models: []provider.Model{{
			ID:           "deepseek-chat",
			Aliases:      []string{"deepseek-default"},
			Capabilities: []provider.Capability{provider.CapabilityOpenAIChat},
		}},
		Health:       provider.Health{Status: provider.HealthReady, CheckedAt: now},
		Auth:         provider.AuthState{Status: provider.AuthHealthy, Account: account},
		RegisteredAt: now,
	}
}

func httpURLToWS(raw string) string {
	if strings.HasPrefix(raw, "https://") {
		return "wss://" + strings.TrimPrefix(raw, "https://")
	}
	return "ws://" + strings.TrimPrefix(raw, "http://")
}

const routerV2E2EPolicy = `
version: routing-policy/v1
model_aliases:
  providersim-default:
    canonical_model: gpt-5-sim
    required_capabilities:
      - api.openai.chat
  claude-default:
    canonical_model: claude-native-sim
    required_capabilities:
      - api.anthropic.messages
  gemini-default:
    canonical_model: gemini-native-sim
    required_capabilities:
      - api.gemini.generateContent
routes:
  - id: providersim-openai
    match:
      models: [providersim-default]
      api_dialects: [openai]
    candidates:
      - provider: providersim-multi
        account: providersim@example.test
        host_name: providersim-host
        weight: 100
    constraints:
      auth_status: [healthy, refresh_soon]
      health_state: [ready]
      max_queue_depth: 4
  - id: providersim-anthropic
    match:
      models: [claude-default]
      api_dialects: [anthropic]
    candidates:
      - provider: providersim-multi
        account: providersim@example.test
        host_name: providersim-host
        weight: 100
    constraints:
      auth_status: [healthy, refresh_soon]
      health_state: [ready]
      max_queue_depth: 4
  - id: providersim-gemini
    match:
      models: [gemini-default]
      api_dialects: [gemini]
    candidates:
      - provider: providersim-multi
        account: providersim@example.test
        host_name: providersim-host
        weight: 100
    constraints:
      auth_status: [healthy, refresh_soon]
      health_state: [ready]
      max_queue_depth: 4
`

const routerV2FallbackE2EPolicy = `
version: routing-policy/v1
model_aliases:
  providersim-default:
    canonical_model: gpt-5-sim
    required_capabilities:
      - api.openai.chat
routes:
  - id: providersim-openai-fallback
    match:
      models: [providersim-default]
      api_dialects: [openai]
    candidates:
      - provider: providersim-missing
        account: missing@example.test
        host_name: missing-host
        weight: 100
      - provider: providersim-multi
        account: providersim@example.test
        host_name: providersim-host
        weight: 10
    constraints:
      auth_status: [healthy, refresh_soon]
      health_state: [ready]
      max_queue_depth: 4
`

const routerV2APICompatiblePolicy = `
version: routing-policy/v1
model_aliases:
  deepseek-default:
    canonical_model: deepseek-chat
    required_capabilities:
      - api.openai.chat
routes:
  - id: deepseek-openai
    match:
      models: [deepseek-default]
      api_dialects: [openai]
    candidates:
      - provider: deepseek-api
        account: deepseek-api@example.test
        host_name: api-host
        weight: 100
    constraints:
      auth_status: [healthy]
      health_state: [ready]
`
