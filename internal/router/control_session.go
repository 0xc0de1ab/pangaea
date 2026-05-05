package router

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/0xc0de1ab/pangaea/internal/control"
	"github.com/0xc0de1ab/pangaea/internal/provider"
	"github.com/gorilla/websocket"
)

var ErrProviderControlSessionNotFound = errors.New("provider control session not found")

type controlSession struct {
	conn    *websocket.Conn
	writeMu sync.Mutex
}

func newControlSession(conn *websocket.Conn) *controlSession {
	return &controlSession{conn: conn}
}

func (s *controlSession) write(messageType control.MessageType, id string, payload any) error {
	if s == nil || s.conn == nil {
		return ErrProviderControlSessionNotFound
	}
	data, err := control.Marshal(messageType, id, time.Now().UTC(), payload)
	if err != nil {
		return err
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.conn.WriteMessage(websocket.TextMessage, data)
}

func (e *Engine) RequestAuthRefresh(ctx context.Context, request control.AuthRefreshRequest) (control.AuthRefreshResult, error) {
	if e == nil || e.registry == nil {
		return control.AuthRefreshResult{}, ErrRouterNotReady
	}
	request.ProviderInstanceID = strings.TrimSpace(request.ProviderInstanceID)
	if request.ProviderInstanceID == "" {
		return control.AuthRefreshResult{}, control.ErrInvalidPayload
	}
	if strings.TrimSpace(request.RefreshID) == "" {
		request.RefreshID = newControlRequestID("refresh", request.ProviderInstanceID)
	}
	if request.DeadlineAt.IsZero() {
		if deadline, ok := ctx.Deadline(); ok {
			request.DeadlineAt = deadline.UTC()
		}
	}
	if _, ok := e.registry.Get(request.ProviderInstanceID); !ok {
		return control.AuthRefreshResult{}, provider.ErrProviderNotFound
	}

	e.controlMu.Lock()
	session := e.controlSessions[request.ProviderInstanceID]
	if session == nil {
		e.controlMu.Unlock()
		return control.AuthRefreshResult{}, fmt.Errorf("%w: %s", ErrProviderControlSessionNotFound, request.ProviderInstanceID)
	}
	if e.pendingAuthRefresh == nil {
		e.pendingAuthRefresh = make(map[string]chan control.AuthRefreshResult)
	}
	if _, exists := e.pendingAuthRefresh[request.RefreshID]; exists {
		e.controlMu.Unlock()
		return control.AuthRefreshResult{}, fmt.Errorf("%w: duplicate refresh id %q", control.ErrInvalidPayload, request.RefreshID)
	}
	resultCh := make(chan control.AuthRefreshResult, 1)
	e.pendingAuthRefresh[request.RefreshID] = resultCh
	e.controlMu.Unlock()
	defer e.removePendingAuthRefresh(request.RefreshID)

	if err := e.markProviderAuthRefreshing(request.ProviderInstanceID); err != nil {
		return control.AuthRefreshResult{}, err
	}
	if err := session.write(control.MessageTypeAuthRefreshRequest, request.RefreshID, request); err != nil {
		return control.AuthRefreshResult{}, err
	}
	select {
	case result := <-resultCh:
		return result, nil
	case <-ctx.Done():
		return control.AuthRefreshResult{}, ctx.Err()
	}
}

func (e *Engine) SendProviderDrain(ctx context.Context, request control.ProviderDrain) error {
	if e == nil || e.registry == nil {
		return ErrRouterNotReady
	}
	request.ProviderInstanceID = strings.TrimSpace(request.ProviderInstanceID)
	if request.ProviderInstanceID == "" {
		return control.ErrInvalidPayload
	}
	if _, ok := e.registry.Get(request.ProviderInstanceID); !ok {
		return provider.ErrProviderNotFound
	}
	e.controlMu.RLock()
	session := e.controlSessions[request.ProviderInstanceID]
	e.controlMu.RUnlock()
	if session == nil {
		return fmt.Errorf("%w: %s", ErrProviderControlSessionNotFound, request.ProviderInstanceID)
	}
	if err := e.markProviderDrainState(request.ProviderInstanceID, request.Drain, request.Reason); err != nil {
		return err
	}
	id := newControlRequestID("drain", request.ProviderInstanceID)
	if request.DeadlineAt.IsZero() {
		if deadline, ok := ctx.Deadline(); ok {
			request.DeadlineAt = deadline.UTC()
		}
	}
	return session.write(control.MessageTypeProviderDrain, id, request)
}

func (e *Engine) bindProviderControlSession(providerInstanceID string, session *controlSession) {
	if e == nil || session == nil || strings.TrimSpace(providerInstanceID) == "" {
		return
	}
	e.controlMu.Lock()
	defer e.controlMu.Unlock()
	if e.controlSessions == nil {
		e.controlSessions = make(map[string]*controlSession)
	}
	e.controlSessions[providerInstanceID] = session
}

func (e *Engine) removeControlSession(session *controlSession) {
	if e == nil || session == nil {
		return
	}
	e.controlMu.Lock()
	defer e.controlMu.Unlock()
	for providerInstanceID, current := range e.controlSessions {
		if current == session {
			delete(e.controlSessions, providerInstanceID)
		}
	}
}

func (e *Engine) completeAuthRefreshResult(result control.AuthRefreshResult) {
	if e == nil || strings.TrimSpace(result.RefreshID) == "" {
		return
	}
	e.controlMu.RLock()
	resultCh := e.pendingAuthRefresh[result.RefreshID]
	e.controlMu.RUnlock()
	if resultCh == nil {
		return
	}
	select {
	case resultCh <- result:
	default:
	}
}

func (e *Engine) removePendingAuthRefresh(refreshID string) {
	e.controlMu.Lock()
	defer e.controlMu.Unlock()
	delete(e.pendingAuthRefresh, refreshID)
}

func (e *Engine) markProviderAuthRefreshing(providerInstanceID string) error {
	registration, ok := e.registry.Get(providerInstanceID)
	if !ok {
		return provider.ErrProviderNotFound
	}
	auth := registration.Auth
	auth.Status = provider.AuthRefreshing
	auth.LastRefreshErr = ""
	registration.Auth = auth
	return e.registry.Upsert(registration)
}

func (e *Engine) markProviderDrainState(providerInstanceID string, drain bool, reason string) error {
	registration, ok := e.registry.Get(providerInstanceID)
	if !ok {
		return provider.ErrProviderNotFound
	}
	if drain {
		registration.Health.Status = provider.HealthDraining
	} else {
		registration.Health.Status = provider.HealthReady
	}
	registration.Health.Reason = reason
	registration.Health.CheckedAt = time.Now().UTC()
	return e.registry.Upsert(registration)
}

func newControlRequestID(prefix string, providerInstanceID string) string {
	cleaned := strings.NewReplacer("/", "_", " ", "_", ":", "_").Replace(providerInstanceID)
	return fmt.Sprintf("%s_%s_%s", prefix, cleaned, time.Now().UTC().Format("20060102150405.000000000"))
}
