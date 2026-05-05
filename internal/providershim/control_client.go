// Package providershim contains provider-shim side control-plane clients.
package providershim

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/0xc0de1ab/pangaea/internal/control"
	"github.com/0xc0de1ab/pangaea/internal/provider"
	"github.com/0xc0de1ab/pangaea/internal/providersim"
	"github.com/gorilla/websocket"
)

var ErrShimConfig = errors.New("invalid provider shim config")

type ControlClientOptions struct {
	ControlURL        string
	HeartbeatInterval time.Duration
	Simulator         *providersim.Simulator
}

func RunSimulatorControlClient(ctx context.Context, opts ControlClientOptions) error {
	if opts.ControlURL == "" {
		return fmt.Errorf("%w: control url is required", ErrShimConfig)
	}
	if opts.Simulator == nil {
		return fmt.Errorf("%w: simulator is required", ErrShimConfig)
	}
	heartbeatInterval := opts.HeartbeatInterval
	if heartbeatInterval <= 0 {
		heartbeatInterval = 30 * time.Second
	}

	conn, _, err := websocket.DefaultDialer.DialContext(ctx, opts.ControlURL, nil)
	if err != nil {
		return err
	}
	defer conn.Close()
	client := newControlClientConn(conn)
	defer client.close()

	registration, err := opts.Simulator.Registration()
	if err != nil {
		return err
	}
	if err := client.sendAndWaitAck(ctx, control.MessageTypeProviderRegister, "provider_register", registration); err != nil {
		return err
	}
	if err := writeSimulatorInventoryReport(ctx, client, opts.Simulator, "provider_inventory_initial"); err != nil {
		return err
	}
	if err := writeSimulatorAuthReport(ctx, client, opts.Simulator, "provider_auth_initial"); err != nil {
		return err
	}
	if err := writeSimulatorUsageReport(ctx, client, opts.Simulator, "provider_usage_initial"); err != nil {
		return err
	}

	ticker := time.NewTicker(heartbeatInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case err := <-client.errCh:
			if ctx.Err() != nil {
				return nil
			}
			return err
		case env := <-client.incoming:
			if err := handleSimulatorControlRequest(ctx, client, opts.Simulator, env); err != nil {
				if ctx.Err() != nil {
					return nil
				}
				return err
			}
		case <-ticker.C:
			heartbeat := opts.Simulator.Heartbeat()
			auth := opts.Simulator.Auth()
			if err := client.sendAndWaitAck(ctx, control.MessageTypeProviderHeartbeat, "provider_heartbeat_"+time.Now().UTC().Format("20060102150405.000000000"), control.ProviderHeartbeat{
				ProviderInstanceID: heartbeat.Identity.ProviderInstanceID,
				Health:             heartbeat.Health,
				Auth:               auth.Auth,
				Limits:             heartbeat.Limits,
				ReportedAt:         heartbeat.ReportedAt,
			}); err != nil {
				if ctx.Err() != nil {
					return nil
				}
				return err
			}
			if err := writeSimulatorUsageReport(ctx, client, opts.Simulator, "provider_usage_"+time.Now().UTC().Format("20060102150405.000000000")); err != nil {
				if ctx.Err() != nil {
					return nil
				}
				return err
			}
		}
	}
}

func RegisterSimulatorOnce(ctx context.Context, controlURL string, sim *providersim.Simulator) error {
	if controlURL == "" || sim == nil {
		return fmt.Errorf("%w: control url and simulator are required", ErrShimConfig)
	}
	conn, _, err := websocket.DefaultDialer.DialContext(ctx, controlURL, nil)
	if err != nil {
		return err
	}
	defer conn.Close()
	client := newControlClientConn(conn)
	defer client.close()

	registration, err := sim.Registration()
	if err != nil {
		return err
	}
	return client.sendAndWaitAck(ctx, control.MessageTypeProviderRegister, "provider_register", registration)
}

type controlClientConn struct {
	conn      *websocket.Conn
	writeMu   sync.Mutex
	pendingMu sync.Mutex
	pending   map[string]chan controlAckResult
	incoming  chan control.Envelope
	errCh     chan error
}

type controlAckResult struct {
	env control.Envelope
	err error
}

func newControlClientConn(conn *websocket.Conn) *controlClientConn {
	client := &controlClientConn{
		conn:     conn,
		pending:  make(map[string]chan controlAckResult),
		incoming: make(chan control.Envelope, 16),
		errCh:    make(chan error, 1),
	}
	go client.readLoop()
	return client
}

func (c *controlClientConn) close() {
	if c == nil || c.conn == nil {
		return
	}
	_ = c.conn.Close()
}

func (c *controlClientConn) sendAndWaitAck(ctx context.Context, messageType control.MessageType, id string, payload any) error {
	if c == nil || c.conn == nil {
		return fmt.Errorf("%w: control connection is nil", ErrShimConfig)
	}
	ackCh := make(chan controlAckResult, 1)
	c.pendingMu.Lock()
	c.pending[id] = ackCh
	c.pendingMu.Unlock()
	defer c.removePending(id)

	data, err := control.Marshal(messageType, id, time.Now().UTC(), payload)
	if err != nil {
		return err
	}
	c.writeMu.Lock()
	err = c.conn.WriteMessage(websocket.TextMessage, data)
	c.writeMu.Unlock()
	if err != nil {
		return err
	}

	select {
	case <-ctx.Done():
		return ctx.Err()
	case result := <-ackCh:
		if result.err != nil {
			return result.err
		}
		return decodeAckEnvelope(result.env)
	}
}

func (c *controlClientConn) readLoop() {
	for {
		_, data, err := c.conn.ReadMessage()
		if err != nil {
			c.fail(err)
			return
		}
		env, err := control.Unmarshal(data)
		if err != nil {
			c.fail(err)
			return
		}
		if c.deliverAck(env) {
			continue
		}
		select {
		case c.incoming <- env:
		default:
			c.fail(fmt.Errorf("%w: incoming control queue full", ErrShimConfig))
			return
		}
	}
}

func (c *controlClientConn) deliverAck(env control.Envelope) bool {
	var replyTo string
	switch env.Type {
	case control.MessageTypeAck:
		payload, err := control.Decode[control.Ack](env, control.MessageTypeAck)
		if err != nil {
			c.fail(err)
			return true
		}
		replyTo = payload.ReplyTo
	case control.MessageTypeControlError:
		payload, err := control.Decode[control.ControlError](env, control.MessageTypeControlError)
		if err != nil {
			c.fail(err)
			return true
		}
		replyTo = payload.ReplyTo
	default:
		return false
	}
	if replyTo == "" {
		return false
	}
	c.pendingMu.Lock()
	ch := c.pending[replyTo]
	c.pendingMu.Unlock()
	if ch == nil {
		return true
	}
	select {
	case ch <- controlAckResult{env: env}:
	default:
	}
	return true
}

func (c *controlClientConn) fail(err error) {
	c.pendingMu.Lock()
	for id, ch := range c.pending {
		select {
		case ch <- controlAckResult{err: err}:
		default:
		}
		delete(c.pending, id)
	}
	c.pendingMu.Unlock()
	select {
	case c.errCh <- err:
	default:
	}
}

func (c *controlClientConn) removePending(id string) {
	c.pendingMu.Lock()
	defer c.pendingMu.Unlock()
	delete(c.pending, id)
}

func decodeAckEnvelope(env control.Envelope) error {
	if env.Type == control.MessageTypeControlError {
		payload, err := control.Decode[control.ControlError](env, control.MessageTypeControlError)
		if err != nil {
			return err
		}
		return fmt.Errorf("%w: %s", ErrShimConfig, payload.Message)
	}
	if env.Type != control.MessageTypeAck {
		return fmt.Errorf("%w: unexpected ack type %q", ErrShimConfig, env.Type)
	}
	payload, err := control.Decode[control.Ack](env, control.MessageTypeAck)
	if err != nil {
		return err
	}
	if !payload.OK {
		return fmt.Errorf("%w: %s", ErrShimConfig, payload.Message)
	}
	return nil
}

func writeSimulatorInventoryReport(ctx context.Context, client *controlClientConn, sim *providersim.Simulator, id string) error {
	registration, err := sim.Registration()
	if err != nil {
		return err
	}
	inventory := sim.Inventory()
	return client.sendAndWaitAck(ctx, control.MessageTypeProviderInventoryReport, id, control.ProviderInventoryReport{
		Mode:       "full",
		NodeID:     registration.Identity.NodeID,
		HostName:   registration.Identity.HostName,
		Providers:  []control.ProviderRegisterPayload{registration},
		ReportedAt: inventory.ReportedAt,
	})
}

func writeSimulatorAuthReport(ctx context.Context, client *controlClientConn, sim *providersim.Simulator, id string) error {
	auth := sim.Auth()
	return client.sendAndWaitAck(ctx, control.MessageTypeProviderAuthReport, id, control.ProviderAuthReport{
		ProviderInstanceID: auth.Identity.ProviderInstanceID,
		Auth:               auth.Auth,
		ReportedAt:         auth.ReportedAt,
	})
}

func writeSimulatorUsageReport(ctx context.Context, client *controlClientConn, sim *providersim.Simulator, id string) error {
	usage, err := sim.Usage()
	if err != nil {
		return nil
	}
	return client.sendAndWaitAck(ctx, control.MessageTypeProviderUsageReport, id, control.ProviderUsageReport{
		ProviderInstanceID: usage.Identity.ProviderInstanceID,
		Usage:              usage.Usage,
		ReportedAt:         usage.ReportedAt,
	})
}

func handleSimulatorControlRequest(ctx context.Context, client *controlClientConn, sim *providersim.Simulator, env control.Envelope) error {
	switch env.Type {
	case control.MessageTypeAuthRefreshRequest:
		request, err := control.Decode[control.AuthRefreshRequest](env, control.MessageTypeAuthRefreshRequest)
		if err != nil {
			return err
		}
		return handleSimulatorAuthRefreshRequest(ctx, client, sim, request)
	default:
		return fmt.Errorf("%w: unsupported control request type %q", ErrShimConfig, env.Type)
	}
}

func handleSimulatorAuthRefreshRequest(ctx context.Context, client *controlClientConn, sim *providersim.Simulator, request control.AuthRefreshRequest) error {
	registration, err := sim.Registration()
	if err != nil {
		return err
	}
	result := control.AuthRefreshResult{
		RefreshID:          request.RefreshID,
		ProviderInstanceID: request.ProviderInstanceID,
		ReportedAt:         time.Now().UTC(),
	}
	if request.ProviderInstanceID != registration.Identity.ProviderInstanceID {
		result.OK = false
		result.Error = &control.ErrorPayload{Code: "provider_mismatch", Message: "refresh request provider_instance_id does not match this shim"}
		return client.sendAndWaitAck(ctx, control.MessageTypeAuthRefreshResult, "auth_refresh_result_"+request.RefreshID, result)
	}

	sim.SetAuthStatus(provider.AuthHealthy)
	auth := sim.Auth()
	result.Auth = auth.Auth
	result.ReportedAt = auth.ReportedAt
	result.OK = result.Auth.Status == provider.AuthHealthy
	if !result.OK {
		result.Error = &control.ErrorPayload{Code: "refresh_failed", Message: "simulator auth refresh did not produce healthy auth"}
	}
	return client.sendAndWaitAck(ctx, control.MessageTypeAuthRefreshResult, "auth_refresh_result_"+request.RefreshID, result)
}
