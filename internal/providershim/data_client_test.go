package providershim

import (
	"context"
	"errors"
	"net/http"
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
	server := httptest.NewServer(router.NewHTTPHandler(router.HTTPOptions{DataBroker: broker, PeerToken: "peer-secret"}))
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
			DataURL:   dataURL(server.URL, registration.Identity.ProviderInstanceID),
			PeerToken: "peer-secret",
			TokenKey:  tokenKey,
			Provider:  blocking,
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

func TestRunSimulatorDataClientStreamsEventFrames(t *testing.T) {
	tokenKey := []byte("test-data-client-stream-key")
	broker, err := router.NewDataBroker(tokenKey)
	if err != nil {
		t.Fatalf("new data broker: %v", err)
	}
	server := httptest.NewServer(router.NewHTTPHandler(router.HTTPOptions{DataBroker: broker}))
	defer server.Close()

	registration := testDataClientRegistration("provider-stream-a1")
	streaming := &streamingDataProvider{registration: registration}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- RunSimulatorDataClient(ctx, DataClientOptions{
			DataURL:  dataURL(server.URL, registration.Identity.ProviderInstanceID),
			TokenKey: tokenKey,
			Provider: streaming,
		})
	}()
	waitForDataSession(t, broker, registration.Identity.ProviderInstanceID)

	events := make(chan compat.Event, 4)
	response, err := broker.InvokeStream(context.Background(), registration, testDataClientStreamRequest("req_stream_shim"), func(event compat.Event) error {
		events <- event
		return nil
	})
	if err != nil {
		t.Fatalf("invoke stream: %v", err)
	}
	if response.Message.Content[0].Text != "streamed response" || response.Usage.TotalTokens != 3 {
		t.Fatalf("unexpected response: %#v", response)
	}
	select {
	case event := <-events:
		if event.Type != compat.EventContentDelta || event.ContentDelta.Text != "streamed response" {
			t.Fatalf("unexpected event: %#v", event)
		}
	default:
		t.Fatalf("expected streamed event")
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

func TestRunSimulatorDataClientPreservesUpstreamErrorMetadata(t *testing.T) {
	tokenKey := []byte("test-data-client-upstream-error-key")
	broker, err := router.NewDataBroker(tokenKey)
	if err != nil {
		t.Fatalf("new data broker: %v", err)
	}
	server := httptest.NewServer(router.NewHTTPHandler(router.HTTPOptions{DataBroker: broker}))
	defer server.Close()

	registration := testDataClientRegistration("provider-upstream-error-a1")
	failing := &upstreamErrorDataProvider{registration: registration}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- RunSimulatorDataClient(ctx, DataClientOptions{
			DataURL:  dataURL(server.URL, registration.Identity.ProviderInstanceID),
			TokenKey: tokenKey,
			Provider: failing,
		})
	}()
	waitForDataSession(t, broker, registration.Identity.ProviderInstanceID)

	_, err = broker.Invoke(context.Background(), registration, testDataClientRequest("req_upstream_error_shim"))
	var upstream *provider.UpstreamError
	if !errors.As(err, &upstream) {
		t.Fatalf("expected upstream error across data plane, got %T %v", err, err)
	}
	if upstream.StatusCode != http.StatusTooManyRequests || upstream.Code != "rate_limit_exceeded" || upstream.Message != "upstream quota exhausted" || upstream.RetryAfter != "15" {
		t.Fatalf("unexpected upstream error details: %#v", upstream)
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

type streamingDataProvider struct {
	registration provider.Registration
}

func (p *streamingDataProvider) Registration() (provider.Registration, error) {
	return p.registration, nil
}

func (p *streamingDataProvider) Invoke(context.Context, provider.Registration, compat.Request) (compat.Response, error) {
	return compat.Response{}, errors.New("non-stream invoke should not be used")
}

func (p *streamingDataProvider) InvokeStream(_ context.Context, _ provider.Registration, request compat.Request, emit func(compat.Event) error) (compat.Response, error) {
	if !request.Stream {
		return compat.Response{}, errors.New("expected stream request")
	}
	if err := emit(compat.Event{
		Type:         compat.EventContentDelta,
		ContentDelta: &compat.ContentPart{Type: compat.ContentPartText, Text: "streamed response"},
	}); err != nil {
		return compat.Response{}, err
	}
	return compat.Response{
		Dialect: request.Dialect,
		Model:   request.Model,
		Message: compat.Message{
			Role:    compat.MessageRoleAssistant,
			Content: []compat.ContentPart{{Type: compat.ContentPartText, Text: "streamed response"}},
		},
		StopReason: "stop",
		Usage:      compat.Usage{InputTokens: 1, OutputTokens: 2, TotalTokens: 3},
	}, nil
}

type upstreamErrorDataProvider struct {
	registration provider.Registration
}

func (p *upstreamErrorDataProvider) Registration() (provider.Registration, error) {
	return p.registration, nil
}

func (p *upstreamErrorDataProvider) Invoke(context.Context, provider.Registration, compat.Request) (compat.Response, error) {
	return compat.Response{}, &provider.UpstreamError{
		StatusCode: http.StatusTooManyRequests,
		Code:       "rate_limit_exceeded",
		Message:    "upstream quota exhausted",
		RetryAfter: "15",
	}
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

func testDataClientStreamRequest(requestID string) compat.Request {
	request := testDataClientRequest(requestID)
	request.Stream = true
	return request
}
