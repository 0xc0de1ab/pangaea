// Package providershim contains provider-shim side control-plane clients.
package providershim

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/0xc0de1ab/pangaea/internal/control"
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

	registration, err := opts.Simulator.Registration()
	if err != nil {
		return err
	}
	if err := writeEnvelope(conn, control.MessageTypeProviderRegister, "provider_register", registration); err != nil {
		return err
	}
	if err := readControlOK(conn); err != nil {
		return err
	}
	if err := writeSimulatorInventoryReport(conn, opts.Simulator, "provider_inventory_initial"); err != nil {
		return err
	}
	if err := writeSimulatorAuthReport(conn, opts.Simulator, "provider_auth_initial"); err != nil {
		return err
	}
	if err := writeSimulatorUsageReport(conn, opts.Simulator, "provider_usage_initial"); err != nil {
		return err
	}

	ticker := time.NewTicker(heartbeatInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			heartbeat := opts.Simulator.Heartbeat()
			auth := opts.Simulator.Auth()
			if err := writeEnvelope(conn, control.MessageTypeProviderHeartbeat, "provider_heartbeat_"+time.Now().UTC().Format("20060102150405.000000000"), control.ProviderHeartbeat{
				ProviderInstanceID: heartbeat.Identity.ProviderInstanceID,
				Health:             heartbeat.Health,
				Auth:               auth.Auth,
				Limits:             heartbeat.Limits,
				ReportedAt:         heartbeat.ReportedAt,
			}); err != nil {
				return err
			}
			if err := readControlOK(conn); err != nil {
				return err
			}
			if err := writeSimulatorUsageReport(conn, opts.Simulator, "provider_usage_"+time.Now().UTC().Format("20060102150405.000000000")); err != nil {
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

	registration, err := sim.Registration()
	if err != nil {
		return err
	}
	if err := writeEnvelope(conn, control.MessageTypeProviderRegister, "provider_register", registration); err != nil {
		return err
	}
	return readControlOK(conn)
}

func writeEnvelope(conn *websocket.Conn, messageType control.MessageType, id string, payload any) error {
	data, err := control.Marshal(messageType, id, time.Now().UTC(), payload)
	if err != nil {
		return err
	}
	return conn.WriteMessage(websocket.TextMessage, data)
}

func writeSimulatorInventoryReport(conn *websocket.Conn, sim *providersim.Simulator, id string) error {
	registration, err := sim.Registration()
	if err != nil {
		return err
	}
	inventory := sim.Inventory()
	if err := writeEnvelope(conn, control.MessageTypeProviderInventoryReport, id, control.ProviderInventoryReport{
		Mode:       "full",
		NodeID:     registration.Identity.NodeID,
		HostName:   registration.Identity.HostName,
		Providers:  []control.ProviderRegisterPayload{registration},
		ReportedAt: inventory.ReportedAt,
	}); err != nil {
		return err
	}
	return readControlOK(conn)
}

func writeSimulatorAuthReport(conn *websocket.Conn, sim *providersim.Simulator, id string) error {
	auth := sim.Auth()
	if err := writeEnvelope(conn, control.MessageTypeProviderAuthReport, id, control.ProviderAuthReport{
		ProviderInstanceID: auth.Identity.ProviderInstanceID,
		Auth:               auth.Auth,
		ReportedAt:         auth.ReportedAt,
	}); err != nil {
		return err
	}
	return readControlOK(conn)
}

func writeSimulatorUsageReport(conn *websocket.Conn, sim *providersim.Simulator, id string) error {
	usage, err := sim.Usage()
	if err != nil {
		return nil
	}
	if err := writeEnvelope(conn, control.MessageTypeProviderUsageReport, id, control.ProviderUsageReport{
		ProviderInstanceID: usage.Identity.ProviderInstanceID,
		Usage:              usage.Usage,
		ReportedAt:         usage.ReportedAt,
	}); err != nil {
		return err
	}
	return readControlOK(conn)
}

func readControlOK(conn *websocket.Conn) error {
	_, data, err := conn.ReadMessage()
	if err != nil {
		return err
	}
	env, err := control.Unmarshal(data)
	if err != nil {
		return err
	}
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
