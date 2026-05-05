package providershim

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/0xc0de1ab/pangaea/internal/compat"
	"github.com/0xc0de1ab/pangaea/internal/provider"
	"github.com/0xc0de1ab/pangaea/internal/providersim"
	"github.com/0xc0de1ab/pangaea/internal/tunnel"
	"github.com/gorilla/websocket"
	"golang.org/x/sync/errgroup"
)

type SimulatorShimOptions struct {
	ControlURL        string
	DataURL           string
	PeerToken         string
	HeartbeatInterval time.Duration
	TokenKey          []byte
	Simulator         *providersim.Simulator
}

type DataClientOptions struct {
	DataURL   string
	PeerToken string
	TokenKey  []byte
	Provider  providerInvoker
}

type providerInvoker interface {
	Registration() (provider.Registration, error)
	Invoke(context.Context, provider.Registration, compat.Request) (compat.Response, error)
}

type providerStreamInvoker interface {
	InvokeStream(context.Context, provider.Registration, compat.Request, func(compat.Event) error) (compat.Response, error)
}

func RunSimulatorShim(ctx context.Context, opts SimulatorShimOptions) error {
	if opts.ControlURL == "" {
		return fmt.Errorf("%w: control url is required", ErrShimConfig)
	}
	if opts.Simulator == nil {
		return fmt.Errorf("%w: simulator is required", ErrShimConfig)
	}
	registration, err := opts.Simulator.Registration()
	if err != nil {
		return err
	}
	dataURL := opts.DataURL
	if dataURL == "" {
		dataURL, err = DeriveDataURL(opts.ControlURL, registration.Identity.ProviderInstanceID)
		if err != nil {
			return err
		}
	}

	eg, ctx := errgroup.WithContext(ctx)
	eg.Go(func() error {
		return RunSimulatorControlClient(ctx, ControlClientOptions{
			ControlURL:        opts.ControlURL,
			PeerToken:         opts.PeerToken,
			HeartbeatInterval: opts.HeartbeatInterval,
			Simulator:         opts.Simulator,
		})
	})
	eg.Go(func() error {
		return RunSimulatorDataClient(ctx, DataClientOptions{
			DataURL:   dataURL,
			PeerToken: opts.PeerToken,
			TokenKey:  opts.TokenKey,
			Provider:  opts.Simulator,
		})
	})
	return eg.Wait()
}

func RunSimulatorDataClient(ctx context.Context, opts DataClientOptions) error {
	if opts.DataURL == "" {
		return fmt.Errorf("%w: data url is required", ErrShimConfig)
	}
	if opts.Provider == nil {
		return fmt.Errorf("%w: provider is required", ErrShimConfig)
	}
	signer, err := tunnel.NewTokenSigner(opts.TokenKey)
	if err != nil {
		return err
	}
	registration, err := opts.Provider.Registration()
	if err != nil {
		return err
	}
	conn, _, err := websocket.DefaultDialer.DialContext(ctx, opts.DataURL, routerPeerDialHeader(opts.PeerToken))
	if err != nil {
		if ctx.Err() != nil {
			return nil
		}
		return err
	}
	defer conn.Close()

	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, "context done"), time.Now().Add(time.Second))
			_ = conn.Close()
		case <-done:
		}
	}()
	defer close(done)

	state := newDataClientState(conn)
	defer state.cancelAll()
	for {
		var request tunnel.DataRequest
		if err := conn.ReadJSON(&request); err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		switch request.Type {
		case "", tunnel.DataFrameRequest:
			state.handleRequest(ctx, signer, registration, opts.Provider, request)
		case tunnel.DataFrameCancel:
			state.cancel(request.RequestID)
		default:
			if err := state.writeResponse(ctx, tunnel.DataResponse{
				Type:      tunnel.DataFrameResponse,
				RequestID: request.RequestID,
				StreamID:  request.Descriptor.StreamID,
				Error:     fmt.Sprintf("%s: unsupported data frame type %q", ErrShimConfig, request.Type),
			}); err != nil {
				return err
			}
		}
	}
}

func DeriveDataURL(controlURL string, providerInstanceID string) (string, error) {
	if strings.TrimSpace(providerInstanceID) == "" {
		return "", fmt.Errorf("%w: provider instance id is required", ErrShimConfig)
	}
	u, err := url.Parse(controlURL)
	if err != nil {
		return "", err
	}
	switch u.Scheme {
	case "http":
		u.Scheme = "ws"
	case "https":
		u.Scheme = "wss"
	}
	if strings.Contains(u.Path, "/control/ws") {
		u.Path = strings.Replace(u.Path, "/control/ws", "/data/ws", 1)
	} else {
		u.Path = "/router/v1/data/ws"
	}
	q := u.Query()
	q.Set("provider_instance_id", providerInstanceID)
	u.RawQuery = q.Encode()
	return u.String(), nil
}

type dataClientState struct {
	conn     *websocket.Conn
	writeMu  sync.Mutex
	mu       sync.Mutex
	inflight map[string]context.CancelFunc
}

func newDataClientState(conn *websocket.Conn) *dataClientState {
	return &dataClientState{
		conn:     conn,
		inflight: make(map[string]context.CancelFunc),
	}
}

func (s *dataClientState) handleRequest(ctx context.Context, signer *tunnel.TokenSigner, registration provider.Registration, invoker providerInvoker, request tunnel.DataRequest) {
	if strings.TrimSpace(request.RequestID) == "" {
		_ = s.writeResponse(ctx, tunnel.DataResponse{
			Type:     tunnel.DataFrameResponse,
			StreamID: request.Descriptor.StreamID,
			Error:    fmt.Sprintf("%s: request_id is required", ErrShimConfig),
		})
		return
	}
	requestCtx, cancel := context.WithCancel(ctx)
	if !s.add(request.RequestID, cancel) {
		cancel()
		_ = s.writeResponse(ctx, tunnel.DataResponse{
			Type:      tunnel.DataFrameResponse,
			RequestID: request.RequestID,
			StreamID:  request.Descriptor.StreamID,
			Error:     fmt.Sprintf("%s: duplicate request_id %q", ErrShimConfig, request.RequestID),
		})
		return
	}
	go func() {
		defer s.done(request.RequestID)
		response := s.handleDataRequest(requestCtx, signer, registration, invoker, request)
		_ = s.writeResponse(ctx, response)
	}()
}

func (s *dataClientState) add(requestID string, cancel context.CancelFunc) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.inflight[requestID]; exists {
		return false
	}
	s.inflight[requestID] = cancel
	return true
}

func (s *dataClientState) cancel(requestID string) {
	s.mu.Lock()
	cancel := s.inflight[requestID]
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (s *dataClientState) done(requestID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.inflight, requestID)
}

func (s *dataClientState) cancelAll() {
	s.mu.Lock()
	inflight := s.inflight
	s.inflight = make(map[string]context.CancelFunc)
	s.mu.Unlock()
	for _, cancel := range inflight {
		cancel()
	}
}

func (s *dataClientState) writeResponse(ctx context.Context, response tunnel.DataResponse) error {
	if response.Type == "" {
		response.Type = tunnel.DataFrameResponse
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if err := s.conn.WriteJSON(response); err != nil {
		if ctx.Err() != nil {
			return nil
		}
		return err
	}
	return nil
}

func (s *dataClientState) handleDataRequest(ctx context.Context, signer *tunnel.TokenSigner, registration provider.Registration, invoker providerInvoker, request tunnel.DataRequest) tunnel.DataResponse {
	if request.Request.Stream {
		return s.handleStreamDataRequest(ctx, signer, registration, invoker, request)
	}
	response := tunnel.DataResponse{Type: tunnel.DataFrameResponse, RequestID: request.RequestID, StreamID: request.Descriptor.StreamID}
	if err := request.Descriptor.Validate(); err != nil {
		response.Error = err.Error()
		return response
	}
	claims, err := signer.VerifyForDescriptor(request.CapabilityToken, request.Descriptor, time.Now().UTC())
	if err != nil {
		response.Error = err.Error()
		return response
	}
	if claims.RequestID != request.RequestID {
		response.Error = fmt.Sprintf("%s: token request_id %q does not match frame request_id %q", ErrShimConfig, claims.RequestID, request.RequestID)
		return response
	}
	compatResponse, err := invoker.Invoke(ctx, registration, request.Request)
	if err != nil {
		applyDataResponseError(&response, err)
		return response
	}
	response.Response = compatResponse
	return response
}

func (s *dataClientState) handleStreamDataRequest(ctx context.Context, signer *tunnel.TokenSigner, registration provider.Registration, invoker providerInvoker, request tunnel.DataRequest) tunnel.DataResponse {
	response := tunnel.DataResponse{Type: tunnel.DataFrameResponse, RequestID: request.RequestID, StreamID: request.Descriptor.StreamID}
	if err := request.Descriptor.Validate(); err != nil {
		response.Error = err.Error()
		return response
	}
	claims, err := signer.VerifyForDescriptor(request.CapabilityToken, request.Descriptor, time.Now().UTC())
	if err != nil {
		response.Error = err.Error()
		return response
	}
	if claims.RequestID != request.RequestID {
		response.Error = fmt.Sprintf("%s: token request_id %q does not match frame request_id %q", ErrShimConfig, claims.RequestID, request.RequestID)
		return response
	}
	if streamInvoker, ok := invoker.(providerStreamInvoker); ok {
		compatResponse, err := streamInvoker.InvokeStream(ctx, registration, request.Request, func(event compat.Event) error {
			if err := event.Validate(); err != nil {
				return err
			}
			return s.writeResponse(ctx, tunnel.DataResponse{
				Type:      tunnel.DataFrameEvent,
				RequestID: request.RequestID,
				StreamID:  request.Descriptor.StreamID,
				Event:     event,
			})
		})
		if err != nil {
			applyDataResponseError(&response, err)
			return response
		}
		response.Response = compatResponse
		return response
	}
	compatResponse, err := invoker.Invoke(ctx, registration, request.Request)
	if err != nil {
		applyDataResponseError(&response, err)
		return response
	}
	events, err := compat.EventsFromResponse(compatResponse)
	if err != nil {
		response.Error = err.Error()
		return response
	}
	for _, event := range events {
		if err := s.writeResponse(ctx, tunnel.DataResponse{
			Type:      tunnel.DataFrameEvent,
			RequestID: request.RequestID,
			StreamID:  request.Descriptor.StreamID,
			Event:     event,
		}); err != nil {
			response.Error = err.Error()
			return response
		}
	}
	response.Response = compatResponse
	return response
}

func applyDataResponseError(response *tunnel.DataResponse, err error) {
	if response == nil || err == nil {
		return
	}
	response.Error = err.Error()
	var upstream *provider.UpstreamError
	if errors.As(err, &upstream) && upstream != nil {
		response.Error = upstream.Message
		if response.Error == "" {
			response.Error = upstream.Error()
		}
		response.ErrorCode = upstream.Code
		response.ErrorStatusCode = upstream.StatusCode
		response.ErrorRetryAfter = upstream.RetryAfter
	}
}
