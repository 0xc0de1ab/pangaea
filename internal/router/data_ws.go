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

const (
	defaultDataRequestTimeout    = 10 * time.Minute
	defaultCapabilityTokenMaxTTL = 30 * time.Second
)

var (
	ErrDataBrokerNotReady = errors.New("router data broker not ready")
	ErrNoDataSession      = errors.New("router data session not found")
	ErrDataRequestFailed  = errors.New("router data request failed")
)

type DataBroker struct {
	mu                    sync.RWMutex
	signer                *tunnel.TokenSigner
	sessions              map[string]*dataSession
	requestTimeout        time.Duration
	capabilityTokenMaxTTL time.Duration
	nextID                atomic.Uint64
}

type DataBrokerOptions struct {
	RequestTimeout        time.Duration
	CapabilityTokenMaxTTL time.Duration
}

type DataSessionSnapshot struct {
	ProviderInstanceID string           `json:"provider_instance_id"`
	ProviderType       string           `json:"provider_type,omitempty"`
	NodeID             string           `json:"node_id,omitempty"`
	HostName           string           `json:"host_name,omitempty"`
	Service            provider.Service `json:"service,omitempty"`
	Account            provider.Account `json:"account,omitempty"`
	ConnectedAt        time.Time        `json:"connected_at"`
	PendingRequests    int              `json:"pending_requests"`
}

func NewDataBroker(tokenKey []byte) (*DataBroker, error) {
	return NewDataBrokerWithOptions(tokenKey, DataBrokerOptions{})
}

func NewDataBrokerWithOptions(tokenKey []byte, opts DataBrokerOptions) (*DataBroker, error) {
	signer, err := tunnel.NewTokenSigner(tokenKey)
	if err != nil {
		return nil, err
	}
	requestTimeout := opts.RequestTimeout
	if requestTimeout <= 0 {
		requestTimeout = defaultDataRequestTimeout
	}
	capabilityTokenMaxTTL := opts.CapabilityTokenMaxTTL
	if capabilityTokenMaxTTL <= 0 {
		capabilityTokenMaxTTL = defaultCapabilityTokenMaxTTL
	}
	return &DataBroker{
		signer:                signer,
		sessions:              make(map[string]*dataSession),
		requestTimeout:        requestTimeout,
		capabilityTokenMaxTTL: capabilityTokenMaxTTL,
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
	dataRequest, deadline, err := b.newDataRequest(ctx, registration, request, false)
	if err != nil {
		return compat.Response{}, err
	}
	session := b.session(registration.Identity.ProviderInstanceID)
	if session == nil {
		return compat.Response{}, fmt.Errorf("%w: %s", ErrNoDataSession, registration.Identity.ProviderInstanceID)
	}
	invokeCtx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	response, err := session.invoke(invokeCtx, dataRequest)
	if err != nil {
		return compat.Response{}, err
	}
	if response.Error != "" {
		return compat.Response{}, dataResponseError(response)
	}
	if err := response.Response.Validate(); err != nil {
		return compat.Response{}, err
	}
	return response.Response, nil
}

func (b *DataBroker) InvokeStream(ctx context.Context, registration provider.Registration, request compat.Request, emit func(compat.Event) error) (compat.Response, error) {
	if b == nil || b.signer == nil {
		return compat.Response{}, ErrDataBrokerNotReady
	}
	if emit == nil {
		return compat.Response{}, fmt.Errorf("%w: stream emit callback is required", ErrDataRequestFailed)
	}
	request.Stream = true
	dataRequest, deadline, err := b.newDataRequest(ctx, registration, request, true)
	if err != nil {
		return compat.Response{}, err
	}
	providerInstanceID := registration.Identity.ProviderInstanceID
	session := b.session(providerInstanceID)
	if session == nil {
		return compat.Response{}, fmt.Errorf("%w: %s", ErrNoDataSession, providerInstanceID)
	}
	invokeCtx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	response, err := session.invokeStream(invokeCtx, dataRequest, emit)
	if err != nil {
		return compat.Response{}, err
	}
	return response, nil
}

func (b *DataBroker) newDataRequest(ctx context.Context, registration provider.Registration, request compat.Request, acceptEvents bool) (tunnel.DataRequest, time.Time, error) {
	requestID := request.ID
	if strings.TrimSpace(requestID) == "" {
		requestID = b.nextRequestID("req")
		request.ID = requestID
	}
	request.Stream = acceptEvents
	now := time.Now().UTC()
	requestTimeout := b.requestTimeout
	if requestTimeout <= 0 {
		requestTimeout = defaultDataRequestTimeout
	}
	deadline := now.Add(requestTimeout)
	if ctxDeadline, ok := ctx.Deadline(); ok && ctxDeadline.Before(deadline) {
		deadline = ctxDeadline
	}
	capabilityTokenMaxTTL := b.capabilityTokenMaxTTL
	if capabilityTokenMaxTTL <= 0 {
		capabilityTokenMaxTTL = defaultCapabilityTokenMaxTTL
	}
	tokenDeadline := now.Add(capabilityTokenMaxTTL)
	if deadline.Before(tokenDeadline) {
		tokenDeadline = deadline
	}
	providerInstanceID := registration.Identity.ProviderInstanceID
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
		Deadline:           tokenDeadline,
	})
	if err != nil {
		return tunnel.DataRequest{}, time.Time{}, err
	}
	return tunnel.DataRequest{
		Type:            tunnel.DataFrameRequest,
		RequestID:       requestID,
		Descriptor:      descriptor,
		CapabilityToken: token,
		Request:         request,
	}, deadline, nil
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

func (b *DataBroker) ProviderAvailable(providerInstanceID string) bool {
	if b == nil {
		return false
	}
	return b.session(providerInstanceID) != nil
}

func (b *DataBroker) ProviderQueueDepth(providerInstanceID string) int {
	if b == nil {
		return 0
	}
	session := b.session(providerInstanceID)
	if session == nil {
		return 0
	}
	return session.pendingCount()
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

func (e *Engine) EnrichDataSessions(sessions []DataSessionSnapshot) []DataSessionSnapshot {
	if e == nil || e.registry == nil || len(sessions) == 0 {
		return sessions
	}
	out := append([]DataSessionSnapshot(nil), sessions...)
	for i := range out {
		registration, ok := e.registry.Get(out[i].ProviderInstanceID)
		if !ok {
			continue
		}
		identity := registration.Identity
		out[i].ProviderType = identity.ProviderType
		out[i].NodeID = identity.NodeID
		out[i].HostName = identity.HostName
		out[i].Service = identity.Service
		out[i].Account = identity.Account
	}
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
	pending            map[string]*pendingResponse
}

func newDataSession(providerInstanceID string, conn *websocket.Conn) *dataSession {
	return &dataSession{
		providerInstanceID: providerInstanceID,
		conn:               conn,
		connectedAt:        time.Now().UTC(),
		pending:            make(map[string]*pendingResponse),
	}
}

type pendingResponse struct {
	ch        chan tunnel.DataResponse
	done      chan struct{}
	closeOnce sync.Once
}

func newPendingResponse(buffer int) *pendingResponse {
	if buffer <= 0 {
		buffer = 1
	}
	return &pendingResponse{
		ch:   make(chan tunnel.DataResponse, buffer),
		done: make(chan struct{}),
	}
}

func (p *pendingResponse) close() {
	if p == nil {
		return
	}
	p.closeOnce.Do(func() {
		close(p.done)
	})
}

func (s *dataSession) invoke(ctx context.Context, request tunnel.DataRequest) (tunnel.DataResponse, error) {
	pending := newPendingResponse(1)
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return tunnel.DataResponse{}, ErrNoDataSession
	}
	s.pending[request.RequestID] = pending
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
	case response := <-pending.ch:
		return response, nil
	case <-pending.done:
		return tunnel.DataResponse{}, ErrNoDataSession
	}
}

func (s *dataSession) invokeStream(ctx context.Context, request tunnel.DataRequest, emit func(compat.Event) error) (compat.Response, error) {
	pending := newPendingResponse(32)
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return compat.Response{}, ErrNoDataSession
	}
	s.pending[request.RequestID] = pending
	s.mu.Unlock()
	defer s.deletePending(request.RequestID)

	s.writeMu.Lock()
	err := s.conn.WriteJSON(request)
	s.writeMu.Unlock()
	if err != nil {
		return compat.Response{}, err
	}
	for {
		select {
		case <-ctx.Done():
			s.sendCancel(request)
			return compat.Response{}, ctx.Err()
		case response := <-pending.ch:
			if response.Error != "" {
				return compat.Response{}, dataResponseError(response)
			}
			switch response.Type {
			case tunnel.DataFrameEvent:
				if err := response.Event.Validate(); err != nil {
					return compat.Response{}, fmt.Errorf("%w: validate data stream event %s: %v", ErrDataRequestFailed, response.Event.Type, err)
				}
				if err := emit(response.Event); err != nil {
					s.sendCancel(request)
					return compat.Response{}, err
				}
			case "", tunnel.DataFrameResponse:
				if err := response.Response.Validate(); err != nil {
					return compat.Response{}, fmt.Errorf("%w: validate data stream response: %v", ErrDataRequestFailed, err)
				}
				return response.Response, nil
			default:
				return compat.Response{}, fmt.Errorf("%w: unsupported data frame type %q", ErrDataRequestFailed, response.Type)
			}
		case <-pending.done:
			return compat.Response{}, ErrNoDataSession
		}
	}
}

func dataResponseError(response tunnel.DataResponse) error {
	if response.ErrorStatusCode != 0 || response.ErrorCode != "" || response.ErrorRetryAfter != "" {
		return &provider.UpstreamError{
			StatusCode: response.ErrorStatusCode,
			Code:       response.ErrorCode,
			Message:    response.Error,
			RetryAfter: response.ErrorRetryAfter,
		}
	}
	return fmt.Errorf("%w: %s", ErrDataRequestFailed, response.Error)
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
	pending := s.pending[response.RequestID]
	s.mu.Unlock()
	if pending == nil {
		return
	}
	select {
	case pending.ch <- response:
	case <-pending.done:
	}
}

func (s *dataSession) deletePending(requestID string) {
	s.mu.Lock()
	pending := s.pending[requestID]
	delete(s.pending, requestID)
	s.mu.Unlock()
	pending.close()
}

func (s *dataSession) closeWithError(err error) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	pending := s.pending
	s.pending = make(map[string]*pendingResponse)
	s.mu.Unlock()

	message := ""
	if err != nil {
		message = err.Error()
	}
	for requestID, ch := range pending {
		response := tunnel.DataResponse{Type: tunnel.DataFrameResponse, RequestID: requestID, Error: message}
		select {
		case ch.ch <- response:
		default:
		}
		ch.close()
	}
	_ = s.conn.Close()
}

func (s *dataSession) snapshot() DataSessionSnapshot {
	pending := s.pendingCount()
	return DataSessionSnapshot{
		ProviderInstanceID: s.providerInstanceID,
		ConnectedAt:        s.connectedAt,
		PendingRequests:    pending,
	}
}

func (s *dataSession) pendingCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.pending)
}
