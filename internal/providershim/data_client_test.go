package providershim

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/0xc0de1ab/pangaea/internal/compat"
	"github.com/0xc0de1ab/pangaea/internal/provider"
	"github.com/0xc0de1ab/pangaea/internal/router"
)

func TestRunSimulatorDataClientCancelsInflightRequestOnCancelFrame(t *testing.T) {
	tokenKey := []byte("test-data-client-cancel-key")
	broker, err := router.NewDataBroker(tokenKey)
	if err != nil {
		t.Fatalf("new data broker: %v", err)
	}
	server := httptest.NewServer(router.NewHTTPHandler(router.HTTPOptions{DataBroker: broker}))
	defer server.Close()

	registration := testDataClientRegistration("provider-a1")
	blocking := &blockingDataProvider{
		registration: registration,
		invoked:      make(chan struct{}, 1),
		canceled:     make(chan struct{}, 1),
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- RunSimulatorDataClient(ctx, DataClientOptions{
			DataURL:  dataURL(server.URL, registration.Identity.ProviderInstanceID),
			TokenKey: tokenKey,
			Provider: blocking,
		})
	}()
	waitForDataSession(t, broker, registration.Identity.ProviderInstanceID)

	invokeCtx, invokeCancel := context.WithCancel(context.Background())
	invokeDone := make(chan error, 1)
	go func() {
		_, err := broker.Invoke(invokeCtx, registration, testDataClientRequest("req_cancel_shim"))
		invokeDone <- err
	}()
	select {
	case <-blocking.invoked:
	case <-time.After(time.Second):
		t.Fatalf("provider invoke did not start")
	}
	invokeCancel()
	select {
	case <-blocking.canceled:
	case <-time.After(time.Second):
		t.Fatalf("provider invoke context was not canceled")
	}
	select {
	case err := <-invokeDone:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected invoke context canceled, got %v", err)
		}
	case <-time.After(time.Second):
		t.Fatalf("broker invoke did not return")
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("data client returned error: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatalf("data client did not stop")
	}
}

type blockingDataProvider struct {
	registration provider.Registration
	invoked      chan struct{}
	canceled     chan struct{}
}

func (p *blockingDataProvider) Registration() (provider.Registration, error) {
	return p.registration, nil
}

func (p *blockingDataProvider) Invoke(ctx context.Context, _ provider.Registration, _ compat.Request) (compat.Response, error) {
	select {
	case p.invoked <- struct{}{}:
	default:
	}
	<-ctx.Done()
	select {
	case p.canceled <- struct{}{}:
	default:
	}
	return compat.Response{}, ctx.Err()
}

func dataURL(serverURL string, providerInstanceID string) string {
	return "ws" + strings.TrimPrefix(serverURL, "http") + "/router/v1/data/ws?provider_instance_id=" + providerInstanceID
}

func waitForDataSession(t *testing.T, broker *router.DataBroker, providerInstanceID string) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		for _, session := range broker.Sessions() {
			if session.ProviderInstanceID == providerInstanceID {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("data session %s did not connect", providerInstanceID)
}

func testDataClientRegistration(providerInstanceID string) provider.Registration {
	return provider.Registration{
		Identity: provider.ProviderIdentity{
			ProviderID:         "providersim-openai",
			ProviderInstanceID: providerInstanceID,
			NodeID:             "node-a1",
			HostName:           "snowbox",
			Service:            provider.ServiceOpenAI,
			Kind:               provider.KindAPICompatible,
		},
	}
}

func testDataClientRequest(requestID string) compat.Request {
	return compat.Request{
		ID:      requestID,
		Dialect: compat.APIDialectOpenAI,
		Model:   "gpt-5-sim",
		Messages: []compat.Message{{
			Role:    compat.MessageRoleUser,
			Content: []compat.ContentPart{{Type: compat.ContentPartText, Text: "slow"}},
		}},
	}
}
