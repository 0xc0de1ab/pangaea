package router

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/0xc0de1ab/pangaea/internal/compat"
	"github.com/0xc0de1ab/pangaea/internal/provider"
	"github.com/0xc0de1ab/pangaea/internal/tunnel"
	"github.com/gorilla/websocket"
)

func TestHTTPDataSessionsListsConnectedProvider(t *testing.T) {
	broker, err := NewDataBroker([]byte("test-data-session-key"))
	if err != nil {
		t.Fatalf("new data broker: %v", err)
	}
	server := httptest.NewServer(NewHTTPHandler(HTTPOptions{DataBroker: broker}))
	defer server.Close()

	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+"/router/v1/data/ws?provider_instance_id=provider-a1", nil)
	if err != nil {
		t.Fatalf("dial data ws: %v", err)
	}
	defer conn.Close()

	resp, err := http.Get(server.URL + "/router/v1/data/sessions")
	if err != nil {
		t.Fatalf("get data sessions: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var out struct {
		Sessions []DataSessionSnapshot `json:"sessions"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode sessions: %v", err)
	}
	if len(out.Sessions) != 1 || out.Sessions[0].ProviderInstanceID != "provider-a1" {
		t.Fatalf("unexpected data sessions: %#v", out.Sessions)
	}
}

func TestDataWSRequiresPeerTokenWhenConfigured(t *testing.T) {
	broker, err := NewDataBroker([]byte("test-data-peer-auth-key"))
	if err != nil {
		t.Fatalf("new data broker: %v", err)
	}
	server := httptest.NewServer(NewHTTPHandler(HTTPOptions{DataBroker: broker, PeerToken: "peer-secret"}))
	defer server.Close()
	url := "ws" + strings.TrimPrefix(server.URL, "http") + "/router/v1/data/ws?provider_instance_id=provider-a1"

	conn, resp, err := websocket.DefaultDialer.Dial(url, nil)
	if err == nil {
		_ = conn.Close()
		t.Fatalf("expected data ws dial without peer token to fail")
	}
	if resp == nil || resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 without peer token, got response=%#v err=%v", resp, err)
	}

	headers := http.Header{}
	headers.Set("authorization", "Bearer peer-secret")
	conn, _, err = websocket.DefaultDialer.Dial(url, headers)
	if err != nil {
		t.Fatalf("dial data ws with peer token: %v", err)
	}
	defer conn.Close()
}

func TestDataBrokerProviderAvailableTracksDataSession(t *testing.T) {
	broker, err := NewDataBroker([]byte("test-data-availability-key"))
	if err != nil {
		t.Fatalf("new data broker: %v", err)
	}
	server := httptest.NewServer(NewHTTPHandler(HTTPOptions{DataBroker: broker}))
	defer server.Close()

	if broker.ProviderAvailable("provider-a1") {
		t.Fatalf("provider should not be available before data session connects")
	}
	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+"/router/v1/data/ws?provider_instance_id=provider-a1", nil)
	if err != nil {
		t.Fatalf("dial data ws: %v", err)
	}
	if !broker.ProviderAvailable("provider-a1") {
		t.Fatalf("provider should be available after data session connects")
	}
	if err := conn.Close(); err != nil {
		t.Fatalf("close data ws: %v", err)
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if !broker.ProviderAvailable("provider-a1") {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("provider should become unavailable after data session disconnects")
}

func TestDataBrokerProviderQueueDepthTracksPendingRequest(t *testing.T) {
	broker, err := NewDataBroker([]byte("test-data-queue-depth-key"))
	if err != nil {
		t.Fatalf("new data broker: %v", err)
	}
	server := httptest.NewServer(NewHTTPHandler(HTTPOptions{DataBroker: broker}))
	defer server.Close()

	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+"/router/v1/data/ws?provider_instance_id=provider-a1", nil)
	if err != nil {
		t.Fatalf("dial data ws: %v", err)
	}
	defer conn.Close()

	done := make(chan error, 1)
	go func() {
		_, err := broker.Invoke(context.Background(), provider.Registration{
			Identity: provider.ProviderIdentity{ProviderInstanceID: "provider-a1"},
		}, compat.Request{
			ID:      "req_queue_depth_1",
			Dialect: compat.APIDialectOpenAI,
			Model:   "gpt-5-sim",
			Messages: []compat.Message{{
				Role:    compat.MessageRoleUser,
				Content: []compat.ContentPart{{Type: compat.ContentPartText, Text: "hello"}},
			}},
		})
		done <- err
	}()

	var request tunnel.DataRequest
	if err := conn.ReadJSON(&request); err != nil {
		t.Fatalf("read request frame: %v", err)
	}
	if got := broker.ProviderQueueDepth("provider-a1"); got != 1 {
		t.Fatalf("queue depth while request pending = %d, want 1", got)
	}
	if err := conn.WriteJSON(tunnel.DataResponse{
		Type:      tunnel.DataFrameResponse,
		RequestID: request.RequestID,
		StreamID:  request.Descriptor.StreamID,
		Response: compat.Response{
			Dialect: compat.APIDialectOpenAI,
			Model:   "gpt-5-sim",
			Message: compat.Message{
				Role:    compat.MessageRoleAssistant,
				Content: []compat.ContentPart{{Type: compat.ContentPartText, Text: "ok"}},
			},
			StopReason: "stop",
			Usage:      compat.Usage{InputTokens: 1, OutputTokens: 1, TotalTokens: 2},
		},
	}); err != nil {
		t.Fatalf("write response frame: %v", err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("invoke: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatalf("invoke did not finish")
	}
	if got := broker.ProviderQueueDepth("provider-a1"); got != 0 {
		t.Fatalf("queue depth after request finished = %d, want 0", got)
	}
}

func TestHTTPDataSessionsIncludesProviderMetadata(t *testing.T) {
	engine, _ := testEngine(t)
	broker, err := NewDataBroker([]byte("test-data-session-metadata-key"))
	if err != nil {
		t.Fatalf("new data broker: %v", err)
	}
	server := httptest.NewServer(NewHTTPHandler(HTTPOptions{Engine: engine, DataBroker: broker}))
	defer server.Close()

	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+"/router/v1/data/ws?provider_instance_id=codex-samtest-a1", nil)
	if err != nil {
		t.Fatalf("dial data ws: %v", err)
	}
	defer conn.Close()

	resp, err := http.Get(server.URL + "/router/v1/data/sessions")
	if err != nil {
		t.Fatalf("get data sessions: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var out struct {
		Sessions []DataSessionSnapshot `json:"sessions"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode sessions: %v", err)
	}
	if len(out.Sessions) != 1 {
		t.Fatalf("expected one data session, got %#v", out.Sessions)
	}
	got := out.Sessions[0]
	if got.ProviderInstanceID != "codex-samtest-a1" || got.HostName != "snowbox" || got.Account.Display != "samtest4u@gmail.com" {
		t.Fatalf("data session response lost provider metadata: %#v", got)
	}
}

func TestHTTPDataSessionsWithoutBrokerReturnsEmptyList(t *testing.T) {
	server := httptest.NewServer(NewHTTPHandler(HTTPOptions{}))
	defer server.Close()

	resp, err := http.Get(server.URL + "/router/v1/data/sessions")
	if err != nil {
		t.Fatalf("get data sessions: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var out struct {
		Sessions []DataSessionSnapshot `json:"sessions"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode sessions: %v", err)
	}
	if len(out.Sessions) != 0 {
		t.Fatalf("expected empty sessions, got %#v", out.Sessions)
	}
}

func TestDataBrokerSendsCancelFrameWhenInvokeContextCancels(t *testing.T) {
	broker, err := NewDataBroker([]byte("test-data-cancel-key"))
	if err != nil {
		t.Fatalf("new data broker: %v", err)
	}
	server := httptest.NewServer(NewHTTPHandler(HTTPOptions{DataBroker: broker}))
	defer server.Close()

	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+"/router/v1/data/ws?provider_instance_id=provider-a1", nil)
	if err != nil {
		t.Fatalf("dial data ws: %v", err)
	}
	defer conn.Close()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := broker.Invoke(ctx, provider.Registration{
			Identity: provider.ProviderIdentity{ProviderInstanceID: "provider-a1"},
		}, compat.Request{
			ID:      "req_cancel_1",
			Dialect: compat.APIDialectOpenAI,
			Model:   "gpt-5-sim",
			Messages: []compat.Message{{
				Role:    compat.MessageRoleUser,
				Content: []compat.ContentPart{{Type: compat.ContentPartText, Text: "slow"}},
			}},
		})
		done <- err
	}()

	var request tunnel.DataRequest
	if err := conn.ReadJSON(&request); err != nil {
		t.Fatalf("read request frame: %v", err)
	}
	if request.Type != tunnel.DataFrameRequest || request.RequestID != "req_cancel_1" {
		t.Fatalf("unexpected request frame: %#v", request)
	}

	cancel()
	var cancelFrame tunnel.DataRequest
	if err := conn.ReadJSON(&cancelFrame); err != nil {
		t.Fatalf("read cancel frame: %v", err)
	}
	if cancelFrame.Type != tunnel.DataFrameCancel || cancelFrame.RequestID != "req_cancel_1" {
		t.Fatalf("unexpected cancel frame: %#v", cancelFrame)
	}
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected context canceled, got %v", err)
		}
	case <-time.After(time.Second):
		t.Fatalf("invoke did not return after cancellation")
	}
}

func TestDataBrokerInvokeStreamReceivesEventFrames(t *testing.T) {
	broker, err := NewDataBroker([]byte("test-data-stream-key"))
	if err != nil {
		t.Fatalf("new data broker: %v", err)
	}
	server := httptest.NewServer(NewHTTPHandler(HTTPOptions{DataBroker: broker}))
	defer server.Close()

	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+"/router/v1/data/ws?provider_instance_id=provider-a1", nil)
	if err != nil {
		t.Fatalf("dial data ws: %v", err)
	}
	defer conn.Close()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	events := make(chan compat.Event, 4)
	done := make(chan struct {
		response compat.Response
		err      error
	}, 1)
	go func() {
		response, err := broker.InvokeStream(ctx, provider.Registration{
			Identity: provider.ProviderIdentity{ProviderInstanceID: "provider-a1"},
		}, compat.Request{
			ID:      "req_stream_1",
			Dialect: compat.APIDialectOpenAI,
			Model:   "gpt-5-sim",
			Stream:  true,
			Messages: []compat.Message{{
				Role:    compat.MessageRoleUser,
				Content: []compat.ContentPart{{Type: compat.ContentPartText, Text: "hello"}},
			}},
		}, func(event compat.Event) error {
			events <- event
			return nil
		})
		done <- struct {
			response compat.Response
			err      error
		}{response: response, err: err}
	}()

	var request tunnel.DataRequest
	if err := conn.ReadJSON(&request); err != nil {
		t.Fatalf("read request frame: %v", err)
	}
	if request.Type != tunnel.DataFrameRequest || request.RequestID != "req_stream_1" || !request.Request.Stream {
		t.Fatalf("unexpected request frame: %#v", request)
	}
	if err := conn.WriteJSON(tunnel.DataResponse{
		Type:      tunnel.DataFrameEvent,
		RequestID: request.RequestID,
		StreamID:  request.Descriptor.StreamID,
		Event: compat.Event{
			Type:         compat.EventContentDelta,
			ContentDelta: &compat.ContentPart{Type: compat.ContentPartText, Text: "hello stream"},
		},
	}); err != nil {
		t.Fatalf("write event frame: %v", err)
	}
	if err := conn.WriteJSON(tunnel.DataResponse{
		Type:      tunnel.DataFrameResponse,
		RequestID: request.RequestID,
		StreamID:  request.Descriptor.StreamID,
		Response: compat.Response{
			Dialect: compat.APIDialectOpenAI,
			Model:   "gpt-5-sim",
			Message: compat.Message{
				Role:    compat.MessageRoleAssistant,
				Content: []compat.ContentPart{{Type: compat.ContentPartText, Text: "hello stream"}},
			},
			StopReason: "stop",
			Usage:      compat.Usage{InputTokens: 1, OutputTokens: 2, TotalTokens: 3},
		},
	}); err != nil {
		t.Fatalf("write response frame: %v", err)
	}

	select {
	case event := <-events:
		if event.Type != compat.EventContentDelta || event.ContentDelta.Text != "hello stream" {
			t.Fatalf("unexpected event: %#v", event)
		}
	case <-time.After(time.Second):
		t.Fatalf("stream event was not emitted")
	}
	select {
	case result := <-done:
		if result.err != nil {
			t.Fatalf("invoke stream: %v", result.err)
		}
		if result.response.Usage.TotalTokens != 3 {
			t.Fatalf("unexpected response: %#v", result.response)
		}
	case <-time.After(time.Second):
		t.Fatalf("invoke stream did not finish")
	}
}

func TestDataBrokerInvokeStreamAppliesBackpressureWithoutDroppingEvents(t *testing.T) {
	broker, err := NewDataBroker([]byte("test-data-stream-backpressure-key"))
	if err != nil {
		t.Fatalf("new data broker: %v", err)
	}
	server := httptest.NewServer(NewHTTPHandler(HTTPOptions{DataBroker: broker}))
	defer server.Close()

	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+"/router/v1/data/ws?provider_instance_id=provider-a1", nil)
	if err != nil {
		t.Fatalf("dial data ws: %v", err)
	}
	defer conn.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	firstEmit := make(chan struct{}, 1)
	releaseEmit := make(chan struct{})
	var mu sync.Mutex
	received := []string{}
	done := make(chan struct {
		response compat.Response
		err      error
	}, 1)
	go func() {
		response, err := broker.InvokeStream(ctx, provider.Registration{
			Identity: provider.ProviderIdentity{ProviderInstanceID: "provider-a1"},
		}, compat.Request{
			ID:      "req_stream_backpressure_1",
			Dialect: compat.APIDialectOpenAI,
			Model:   "gpt-5-sim",
			Stream:  true,
			Messages: []compat.Message{{
				Role:    compat.MessageRoleUser,
				Content: []compat.ContentPart{{Type: compat.ContentPartText, Text: "hello"}},
			}},
		}, func(event compat.Event) error {
			if event.Type == compat.EventContentDelta {
				if event.ContentDelta.Text == "chunk-000" {
					select {
					case firstEmit <- struct{}{}:
					default:
					}
					<-releaseEmit
				}
				mu.Lock()
				received = append(received, event.ContentDelta.Text)
				mu.Unlock()
			}
			return nil
		})
		done <- struct {
			response compat.Response
			err      error
		}{response: response, err: err}
	}()

	var request tunnel.DataRequest
	if err := conn.ReadJSON(&request); err != nil {
		t.Fatalf("read request frame: %v", err)
	}
	if request.Type != tunnel.DataFrameRequest || request.RequestID != "req_stream_backpressure_1" || !request.Request.Stream {
		t.Fatalf("unexpected request frame: %#v", request)
	}

	const eventCount = 80
	writeDone := make(chan error, 1)
	go func() {
		for i := 0; i < eventCount; i++ {
			text := fmt.Sprintf("chunk-%03d", i)
			if err := conn.WriteJSON(tunnel.DataResponse{
				Type:      tunnel.DataFrameEvent,
				RequestID: request.RequestID,
				StreamID:  request.Descriptor.StreamID,
				Event: compat.Event{
					Type:         compat.EventContentDelta,
					ContentDelta: &compat.ContentPart{Type: compat.ContentPartText, Text: text},
				},
			}); err != nil {
				writeDone <- err
				return
			}
		}
		writeDone <- conn.WriteJSON(tunnel.DataResponse{
			Type:      tunnel.DataFrameResponse,
			RequestID: request.RequestID,
			StreamID:  request.Descriptor.StreamID,
			Response: compat.Response{
				Dialect: compat.APIDialectOpenAI,
				Model:   "gpt-5-sim",
				Message: compat.Message{
					Role:    compat.MessageRoleAssistant,
					Content: []compat.ContentPart{{Type: compat.ContentPartText, Text: "complete"}},
				},
				StopReason: "stop",
				Usage:      compat.Usage{InputTokens: 1, OutputTokens: 2, TotalTokens: 3},
			},
		})
	}()

	select {
	case <-firstEmit:
	case <-time.After(time.Second):
		t.Fatalf("first stream event was not emitted")
	}
	time.Sleep(50 * time.Millisecond)
	close(releaseEmit)

	select {
	case err := <-writeDone:
		if err != nil {
			t.Fatalf("write stream frames: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatalf("provider stream frame writer did not finish")
	}
	select {
	case result := <-done:
		if result.err != nil {
			t.Fatalf("invoke stream: %v", result.err)
		}
		if result.response.Message.Content[0].Text != "complete" {
			t.Fatalf("unexpected response: %#v", result.response)
		}
	case <-time.After(time.Second):
		t.Fatalf("invoke stream did not finish")
	}

	mu.Lock()
	defer mu.Unlock()
	if len(received) != eventCount {
		t.Fatalf("received %d stream events, want %d: %#v", len(received), eventCount, received)
	}
	if received[0] != "chunk-000" || received[eventCount-1] != "chunk-079" {
		t.Fatalf("unexpected event ordering: first=%q last=%q", received[0], received[eventCount-1])
	}
}
