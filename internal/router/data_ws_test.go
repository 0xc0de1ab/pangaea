package router

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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
