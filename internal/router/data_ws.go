package router

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/0xc0de1ab/pangaea/internal/compat"
	"github.com/0xc0de1ab/pangaea/internal/provider"
	"github.com/0xc0de1ab/pangaea/internal/tunnel"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

const defaultDataRequestTimeout = 2 * time.Minute

var (
	ErrDataBrokerNotReady = errors.New("router data broker not ready")
	ErrNoDataSession      = errors.New("router data session not found")
	ErrDataRequestFailed  = errors.New("router data request failed")
)

type DataBroker struct {
	mu       sync.RWMutex
	signer   *tunnel.TokenSigner
	sessions map[string]*dataSession
	nextID   atomic.Uint64
}

type DataSessionSnapshot struct {
	ProviderInstanceID string    `json:"provider_instance_id"`
	ConnectedAt        time.Time `json:"connected_at"`
	PendingRequests    int       `json:"pending_requests"`
}

func NewDataBroker(tokenKey []byte) (*DataBroker, error) {
	signer, err := tunnel.NewTokenSigner(tokenKey)
	if err != nil {
		return nil, err
	}
	return &DataBroker{
		signer:   signer,
		sessions: make(map[string]*dataSession),
	}, nil
}

func (b *DataBroker) HandleDataWS(c *gin.Context) {
	if b == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": ErrDataBrokerNotReady.Error()})
		return
	}
	providerInstanceID := strings.TrimSpace(c.Query("provider_instance_id"))
	if providerInstanceID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "provider_instance_id is required"})
		return
	}
	conn, err := controlUpgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		return
	}
	session := newDataSession(providerInstanceID, conn)
	b.putSession(session)
	defer b.removeSession(session)
	session.readLoop()
}

func (b *DataBroker) Invoke(ctx context.Context, registration provider.Registration, request compat.Request) (compat.Response, error) {
	if b == nil || b.signer == nil {
		return compat.Response{}, ErrDataBrokerNotReady
	}
	providerInstanceID := registration.Identity.ProviderInstanceID
	session := b.session(providerInstanceID)
	if session == nil {
		return compat.Response{}, fmt.Errorf("%w: %s", ErrNoDataSession, providerInstanceID)
	}
	requestID := request.ID
	if strings.TrimSpace(requestID) == "" {
		requestID = b.nextRequestID("req")
		request.ID = requestID
	}
	now := time.Now().UTC()
	deadline := now.Add(defaultDataRequestTimeout)
	if ctxDeadline, ok := ctx.Deadline(); ok && ctxDeadline.Before(deadline) {
		deadline = ctxDeadline
	}
	invokeCtx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	descriptor := tunnel.StreamDescriptor{
		StreamID:           tunnel.StreamID("stream_" + requestID),
		ProviderInstanceID: tunnel.ProviderInstanceID(providerInstanceID),
		Model:              request.Model,
		State:              tunnel.StateActive,
		CreatedAt:          now,
		UpdatedAt:          now,
	}
	token, err := b.signer.Sign(tunnel.StreamTokenClaims{
		RequestID:          requestID,
		StreamID:           descriptor.StreamID,
		ProviderInstanceID: descriptor.ProviderInstanceID,
		Model:              descriptor.Model,
		Deadline:           deadline,
	})
	if err != nil {
		return compat.Response{}, err
	}
	response, err := session.invoke(invokeCtx, tunnel.DataRequest{
		Type:            tunnel.DataFrameRequest,
		RequestID:       requestID,
		Descriptor:      descriptor,
		CapabilityToken: token,
		Request:         request,
	})
	if err != nil {
		return compat.Response{}, err
	}
	if response.Error != "" {
		return compat.Response{}, fmt.Errorf("%w: %s", ErrDataRequestFailed, response.Error)
	}
	if err := response.Response.Validate(); err != nil {
		return compat.Response{}, err
	}
	return response.Response, nil
}

func (b *DataBroker) putSession(session *dataSession) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if previous := b.sessions[session.providerInstanceID]; previous != nil {
		previous.closeWithError(fmt.Errorf("data session replaced"))
	}
	b.sessions[session.providerInstanceID] = session
}

func (b *DataBroker) removeSession(session *dataSession) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.sessions[session.providerInstanceID] == session {
		delete(b.sessions, session.providerInstanceID)
	}
	session.closeWithError(fmt.Errorf("data session closed"))
}

func (b *DataBroker) session(providerInstanceID string) *dataSession {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.sessions[providerInstanceID]
}

func (b *DataBroker) Sessions() []DataSessionSnapshot {
	if b == nil {
		return nil
	}
	b.mu.RLock()
	sessions := make([]*dataSession, 0, len(b.sessions))
	for _, session := range b.sessions {
		sessions = append(sessions, session)
	}
	b.mu.RUnlock()
	out := make([]DataSessionSnapshot, 0, len(sessions))
	for _, session := range sessions {
		out = append(out, session.snapshot())
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].ProviderInstanceID < out[j].ProviderInstanceID
	})
	return out
}

func (b *DataBroker) nextRequestID(prefix string) string {
	return fmt.Sprintf("%s_%d", prefix, b.nextID.Add(1))
}

type dataSession struct {
	providerInstanceID string
	conn               *websocket.Conn
	connectedAt        time.Time
	writeMu            sync.Mutex
	mu                 sync.Mutex
	closed             bool
	pending            map[string]chan tunnel.DataResponse
}

func newDataSession(providerInstanceID string, conn *websocket.Conn) *dataSession {
	return &dataSession{
		providerInstanceID: providerInstanceID,
		conn:               conn,
		connectedAt:        time.Now().UTC(),
		pending:            make(map[string]chan tunnel.DataResponse),
	}
}

func (s *dataSession) invoke(ctx context.Context, request tunnel.DataRequest) (tunnel.DataResponse, error) {
	responseCh := make(chan tunnel.DataResponse, 1)
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return tunnel.DataResponse{}, ErrNoDataSession
	}
	s.pending[request.RequestID] = responseCh
	s.mu.Unlock()
	defer s.deletePending(request.RequestID)

	s.writeMu.Lock()
	err := s.conn.WriteJSON(request)
	s.writeMu.Unlock()
	if err != nil {
		return tunnel.DataResponse{}, err
	}
	select {
	case <-ctx.Done():
		s.sendCancel(request)
		return tunnel.DataResponse{}, ctx.Err()
	case response := <-responseCh:
		return response, nil
	}
}

func (s *dataSession) sendCancel(request tunnel.DataRequest) {
	cancel := tunnel.DataRequest{
		Type:      tunnel.DataFrameCancel,
		RequestID: request.RequestID,
		Descriptor: tunnel.StreamDescriptor{
			StreamID:           request.Descriptor.StreamID,
			ProviderInstanceID: request.Descriptor.ProviderInstanceID,
			Model:              request.Descriptor.Model,
			State:              tunnel.StateClosed,
			CreatedAt:          request.Descriptor.CreatedAt,
			UpdatedAt:          time.Now().UTC(),
		},
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	_ = s.conn.WriteJSON(cancel)
}

func (s *dataSession) readLoop() {
	defer s.conn.Close()
	for {
		var response tunnel.DataResponse
		if err := s.conn.ReadJSON(&response); err != nil {
			return
		}
		s.dispatch(response)
	}
}

func (s *dataSession) dispatch(response tunnel.DataResponse) {
	s.mu.Lock()
	ch := s.pending[response.RequestID]
	s.mu.Unlock()
	if ch == nil {
		return
	}
	select {
	case ch <- response:
	default:
	}
}

func (s *dataSession) deletePending(requestID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.pending, requestID)
}

func (s *dataSession) closeWithError(err error) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	pending := s.pending
	s.pending = make(map[string]chan tunnel.DataResponse)
	s.mu.Unlock()

	message := ""
	if err != nil {
		message = err.Error()
	}
	for requestID, ch := range pending {
		select {
		case ch <- tunnel.DataResponse{Type: tunnel.DataFrameResponse, RequestID: requestID, Error: message}:
		default:
		}
	}
	_ = s.conn.Close()
}

func (s *dataSession) snapshot() DataSessionSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	return DataSessionSnapshot{
		ProviderInstanceID: s.providerInstanceID,
		ConnectedAt:        s.connectedAt,
		PendingRequests:    len(s.pending),
	}
}
