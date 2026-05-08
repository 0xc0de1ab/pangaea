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
		body := bytes.NewBufferString(`{"refresh_id":"refresh_test","reason":"manual","timeout_seconds":2,"confirm":true}`)
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
	body := bytes.NewBufferString(`{"reason":"manual","timeout_seconds":1,"confirm":true}`)

	req := httptest.NewRequest(http.MethodPost, "/router/v1/providers/codex-samtest-a1/auth/refresh", body)
	req.Header.Set("content-type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestHTTPProviderDrainRoutesCommandToControlSession(t *testing.T) {
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

	body := bytes.NewBufferString(`{"drain":true,"reason":"maintenance","timeout_seconds":1,"confirm":true}`)
	resp, err := http.Post(server.URL+"/router/v1/providers/codex-control-a1/drain", "application/json", body)
	if err != nil {
		t.Fatalf("post drain: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("expected drain 202, got %d", resp.StatusCode)
	}

	_, data, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("read drain command: %v", err)
	}
	env, err := control.Unmarshal(data)
	if err != nil {
		t.Fatalf("unmarshal drain command: %v", err)
	}
	if env.Type != control.MessageTypeProviderDrain {
		t.Fatalf("expected provider drain command, got %s", env.Type)
	}
	command, err := control.Decode[control.ProviderDrain](env, control.MessageTypeProviderDrain)
	if err != nil {
		t.Fatalf("decode drain command: %v", err)
	}
	if !command.Drain || command.Reason != "maintenance" || command.ProviderInstanceID != "codex-control-a1" {
		t.Fatalf("unexpected drain command: %#v", command)
	}

	writeControlEnvelope(t, conn, control.MessageTypeProviderHeartbeat, "msg_draining", control.ProviderHeartbeat{
		ProviderInstanceID: "codex-control-a1",
		Health:             provider.Health{Status: provider.HealthDraining, Reason: "maintenance", CheckedAt: time.Now().UTC()},
	})
	readControlAck(t, conn, "msg_draining")
	for _, registration := range engine.Providers() {
		if registration.Identity.ProviderInstanceID == "codex-control-a1" {
			if registration.Health.Status != provider.HealthDraining {
				t.Fatalf("expected draining provider, got %#v", registration.Health)
			}
			return
		}
	}
	t.Fatalf("registered provider missing")
}

func TestHTTPControlSessionsListsBoundProviderSession(t *testing.T) {
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

	resp, err := http.Get(server.URL + "/router/v1/control/sessions")
	if err != nil {
		t.Fatalf("get control sessions: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var out struct {
		Sessions []ControlSessionSnapshot `json:"sessions"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode sessions: %v", err)
	}
	if len(out.Sessions) != 1 || out.Sessions[0].ProviderInstanceID != "codex-control-a1" || out.Sessions[0].HostName != "snowbox" {
		t.Fatalf("unexpected control sessions: %#v", out.Sessions)
	}
}

func TestControlSessionReplacementIgnoresStaleDisconnect(t *testing.T) {
	engine, _ := testEngine(t)
	reg := registration("codex-control-a1", "codex-cli", "control@example.test", 10, 0)
	if err := engine.UpsertProvider(reg); err != nil {
		t.Fatalf("upsert provider: %v", err)
	}

	oldSession := newControlSession(nil)
	replacement := newControlSession(nil)
	engine.bindProviderControlSession(reg.Identity.ProviderInstanceID, oldSession)
	engine.bindProviderControlSession(reg.Identity.ProviderInstanceID, replacement)
	engine.removeControlSession(oldSession)

	got, ok := engine.registry.Get(reg.Identity.ProviderInstanceID)
	if !ok {
		t.Fatalf("provider missing after stale disconnect")
	}
	if got.Health.Status != provider.HealthReady {
		t.Fatalf("stale disconnect should not mark replacement down, got %#v", got.Health)
	}

	engine.removeControlSession(replacement)
	got, ok = engine.registry.Get(reg.Identity.ProviderInstanceID)
	if !ok {
		t.Fatalf("provider missing after replacement disconnect")
	}
	if got.Health.Status != provider.HealthDown || got.Health.Reason != "control session disconnected" {
		t.Fatalf("expected final disconnect to mark provider down, got %#v", got.Health)
	}
}

func TestControlSessionBindRestoresControlDisconnectedHealth(t *testing.T) {
	engine, _ := testEngine(t)
	reg := registration("codex-control-a1", "codex-cli", "control@example.test", 10, 0)
	if err := engine.UpsertProvider(reg); err != nil {
		t.Fatalf("upsert provider: %v", err)
	}

	oldSession := newControlSession(nil)
	engine.bindProviderControlSession(reg.Identity.ProviderInstanceID, oldSession)
	engine.removeControlSession(oldSession)
	got, ok := engine.registry.Get(reg.Identity.ProviderInstanceID)
	if !ok {
		t.Fatalf("provider missing after disconnect")
	}
	if got.Health.Status != provider.HealthDown || got.Health.Reason != "control session disconnected" {
		t.Fatalf("expected provider down after disconnect, got %#v", got.Health)
	}

	engine.bindProviderControlSession(reg.Identity.ProviderInstanceID, newControlSession(nil))
	got, ok = engine.registry.Get(reg.Identity.ProviderInstanceID)
	if !ok {
		t.Fatalf("provider missing after reconnect")
	}
	if got.Health.Status != provider.HealthReady || got.Health.Reason != "" {
		t.Fatalf("expected reconnect to restore provider health, got %#v", got.Health)
	}
}
