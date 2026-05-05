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

	"github.com/0xc0de1ab/pangaea/internal/compat"
	"github.com/0xc0de1ab/pangaea/internal/provider"
	"github.com/0xc0de1ab/pangaea/internal/providershim"
	"github.com/0xc0de1ab/pangaea/internal/providersim"
	"github.com/0xc0de1ab/pangaea/internal/quota"
	v2router "github.com/0xc0de1ab/pangaea/internal/router"
)

func TestE2E_V2RouterShimDataPlaneOpenAIChat(t *testing.T) {
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

	sim, err := providersim.New(providersim.Options{Mode: providersim.ModeAPICompatible})
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
	waitForV2Provider(t, client, server.URL, "providersim-openai-0001")
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
	usage := waitForV2ProviderUsage(t, client, server.URL, "providersim-openai-0001")
	if usage.HostName != "providersim-host" || usage.Account.Display != "providersim@example.test" {
		t.Fatalf("usage lost provider host/account dimensions: %#v", usage)
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

func waitForV2OpenAIChat(t *testing.T, client *http.Client, baseURL string) compat.OpenAIChatResponse {
	t.Helper()
	body := []byte(`{"model":"providersim-default","messages":[{"role":"user","content":"e2e hello"}]}`)
	deadline := time.Now().Add(5 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		req, err := http.NewRequest(http.MethodPost, baseURL+"/v1/chat/completions", bytes.NewReader(body))
		if err != nil {
			t.Fatalf("new request: %v", err)
		}
		req.Header.Set("content-type", "application/json")
		req.Header.Set("x-request-id", "req_e2e_v2_data")
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
routes:
  - id: providersim-openai
    match:
      models: [providersim-default]
      api_dialects: [openai]
    candidates:
      - provider: providersim-openai
        account: providersim@example.test
        host_name: providersim-host
        weight: 100
    constraints:
      auth_status: [healthy, refresh_soon]
      health_state: [ready]
      max_queue_depth: 4
`
