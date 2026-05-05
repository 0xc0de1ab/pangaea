package router

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/0xc0de1ab/pangaea/internal/control"
	"github.com/0xc0de1ab/pangaea/internal/provider"
	"github.com/gorilla/websocket"
)

func TestHTTPAuthRefreshRoutesRequestToControlSession(t *testing.T) {
	engine, _ := testEngine(t)
	server := httptest.NewServer(NewHTTPHandler(HTTPOptions{Engine: engine}))
	defer server.Close()

	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+"/router/v1/control/ws", nil)
	if err != nil {
		t.Fatalf("dial control ws: %v", err)
	}
	defer conn.Close()

	reg := registration("codex-control-a1", "codex-cli", "control@example.test", 10, 0)
	writeControlEnvelope(t, conn, control.MessageTypeProviderRegister, "msg_register", reg)
	readControlAck(t, conn, "msg_register")

	respCh := make(chan *http.Response, 1)
	errCh := make(chan error, 1)
	go func() {
		body := bytes.NewBufferString(`{"refresh_id":"refresh_test","reason":"manual","timeout_seconds":2}`)
		resp, err := http.Post(server.URL+"/router/v1/providers/codex-control-a1/auth/refresh", "application/json", body)
		if err != nil {
			errCh <- err
			return
		}
		respCh <- resp
	}()

	_, data, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("read refresh request: %v", err)
	}
	env, err := control.Unmarshal(data)
	if err != nil {
		t.Fatalf("unmarshal refresh request: %v", err)
	}
	if env.Type != control.MessageTypeAuthRefreshRequest {
		t.Fatalf("expected auth refresh request, got %s", env.Type)
	}
	request, err := control.Decode[control.AuthRefreshRequest](env, control.MessageTypeAuthRefreshRequest)
	if err != nil {
		t.Fatalf("decode refresh request: %v", err)
	}
	if request.RefreshID != "refresh_test" || request.ProviderInstanceID != "codex-control-a1" || request.Reason != "manual" {
		t.Fatalf("unexpected refresh request: %#v", request)
	}

	reportedAt := time.Now().UTC()
	writeControlEnvelope(t, conn, control.MessageTypeAuthRefreshResult, "msg_refresh_result", control.AuthRefreshResult{
		RefreshID:          request.RefreshID,
		ProviderInstanceID: request.ProviderInstanceID,
		OK:                 true,
		Auth: provider.AuthState{
			Status:        provider.AuthHealthy,
			Account:       reg.Identity.Account,
			ExpiresAt:     reportedAt.Add(time.Hour),
			Refreshable:   true,
			LastRefreshAt: reportedAt,
		},
		ReportedAt: reportedAt,
	})
	readControlAck(t, conn, "msg_refresh_result")

	select {
	case err := <-errCh:
		t.Fatalf("post refresh: %v", err)
	case resp := <-respCh:
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("expected refresh 200, got %d", resp.StatusCode)
		}
		var result control.AuthRefreshResult
		if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
			t.Fatalf("decode refresh response: %v", err)
		}
		if !result.OK || result.RefreshID != "refresh_test" {
			t.Fatalf("unexpected refresh response: %#v", result)
		}
	case <-time.After(time.Second):
		t.Fatalf("refresh HTTP request did not complete")
	}
}

func TestHTTPAuthRefreshWithoutControlSessionReturnsConflict(t *testing.T) {
	engine, _ := testEngine(t)
	handler := NewHTTPHandler(HTTPOptions{Engine: engine})
	body := bytes.NewBufferString(`{"timeout_seconds":1}`)

	req := httptest.NewRequest(http.MethodPost, "/router/v1/providers/codex-samtest-a1/auth/refresh", body)
	req.Header.Set("content-type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d body=%s", rec.Code, rec.Body.String())
	}
}
