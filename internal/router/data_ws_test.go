package router

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
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
