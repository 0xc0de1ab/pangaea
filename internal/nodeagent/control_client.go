// Package nodeagent contains node-side router control-plane clients.
package nodeagent

import (
	"context"
	"errors"
	"fmt"
	"os"
	goruntime "runtime"
	"strings"
	"time"

	"github.com/0xc0de1ab/pangaea/internal/control"
	"github.com/0xc0de1ab/pangaea/internal/provider"
	containerruntime "github.com/0xc0de1ab/pangaea/internal/runtime"
	"github.com/gorilla/websocket"
)

var ErrNodeAgentConfig = errors.New("invalid node agent config")

type ControlClientOptions struct {
	ControlURL        string
	RouterDataURL     string
	StreamTokenKey    string
	PeerToken         string
	NodeID            string
	HostName          string
	AgentVersion      string
	OS                string
	Arch              string
	Runtime           control.RuntimeInfo
	Capabilities      []string
	ProviderSpecs     []ProviderSpec
	HeartbeatInterval time.Duration
	Resources         control.ResourceUsage
	ContainerRuntime  containerruntime.Runtime
	ReconcileInterval time.Duration
}

func RunControlClient(ctx context.Context, opts ControlClientOptions) error {
	opts, err := normalizeOptions(opts)
	if err != nil {
		return err
	}

	conn, _, err := websocket.DefaultDialer.DialContext(ctx, opts.ControlURL, routerPeerDialHeader(opts.PeerToken))
	if err != nil {
		return err
	}
	defer conn.Close()

	if err := writeEnvelope(conn, control.MessageTypeNodeHello, "node_hello", control.NodeHello{
		NodeID:       opts.NodeID,
		AgentVersion: opts.AgentVersion,
		OS:           opts.OS,
		Arch:         opts.Arch,
		Runtime:      opts.Runtime,
		Capabilities: append([]string(nil), opts.Capabilities...),
	}); err != nil {
		return err
	}
	if err := readControlOK(conn); err != nil {
		return err
	}
	if err := writeHeartbeat(conn, opts); err != nil {
		return err
	}
	containerReports, err := reconcileProviderContainers(ctx, opts)
	if err != nil {
		return err
	}
	if err := writeInventory(conn, opts, containerReports); err != nil {
		return err
	}

	ticker := time.NewTicker(opts.HeartbeatInterval)
	defer ticker.Stop()
	var reconcileTicker *time.Ticker
	var reconcileC <-chan time.Time
	if opts.ContainerRuntime != nil && len(opts.ProviderSpecs) > 0 {
		reconcileTicker = time.NewTicker(opts.ReconcileInterval)
		defer reconcileTicker.Stop()
		reconcileC = reconcileTicker.C
	}
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if err := writeHeartbeat(conn, opts); err != nil {
				return err
			}
			if err := writeInventory(conn, opts, containerReports); err != nil {
				return err
			}
		case <-reconcileC:
			reports, err := reconcileProviderContainers(ctx, opts)
			if err != nil {
				return err
			}
			containerReports = reports
			if err := writeInventory(conn, opts, containerReports); err != nil {
				return err
			}
		}
	}
}

func normalizeOptions(opts ControlClientOptions) (ControlClientOptions, error) {
	if strings.TrimSpace(opts.ControlURL) == "" {
		return opts, fmt.Errorf("%w: control url is required", ErrNodeAgentConfig)
	}
	if strings.TrimSpace(opts.HostName) == "" {
		hostName, err := os.Hostname()
		if err != nil {
			return opts, fmt.Errorf("%w: host name is required: %v", ErrNodeAgentConfig, err)
		}
		opts.HostName = hostName
	}
	if strings.TrimSpace(opts.NodeID) == "" {
		opts.NodeID = opts.HostName
	}
	if strings.TrimSpace(opts.AgentVersion) == "" {
		opts.AgentVersion = "dev"
	}
	if strings.TrimSpace(opts.OS) == "" {
		opts.OS = goruntime.GOOS
	}
	if strings.TrimSpace(opts.Arch) == "" {
		opts.Arch = goruntime.GOARCH
	}
	if strings.TrimSpace(opts.Runtime.Kind) == "" {
		opts.Runtime.Kind = "docker"
	}
	if len(opts.Capabilities) == 0 {
		opts.Capabilities = []string{"container.inventory", "container.stats", "provider.lifecycle"}
	}
	if opts.HeartbeatInterval <= 0 {
		opts.HeartbeatInterval = 30 * time.Second
	}
	if opts.ReconcileInterval <= 0 {
		opts.ReconcileInterval = opts.HeartbeatInterval
	}
	return opts, nil
}

func writeHeartbeat(conn *websocket.Conn, opts ControlClientOptions) error {
	now := time.Now().UTC()
	if err := writeEnvelope(conn, control.MessageTypeNodeHeartbeat, "node_heartbeat_"+now.Format("20060102150405.000000000"), control.NodeHeartbeat{
		NodeID:     opts.NodeID,
		HostName:   opts.HostName,
		Health:     control.HealthReport{Status: "ready", CheckedAt: now},
		Resources:  opts.Resources,
		ReportedAt: now,
	}); err != nil {
		return err
	}
	return readControlOK(conn)
}

func writeInventory(conn *websocket.Conn, opts ControlClientOptions, reconciledContainers []control.ContainerReport) error {
	if len(opts.ProviderSpecs) == 0 {
		return nil
	}
	now := time.Now().UTC()
	providers := make([]provider.Registration, 0, len(opts.ProviderSpecs))
	containers := make([]control.ContainerReport, 0, len(opts.ProviderSpecs))
	for _, spec := range opts.ProviderSpecs {
		registration := spec.Registration(opts.NodeID, opts.HostName, now)
		if err := registration.Validate(); err != nil {
			return err
		}
		providers = append(providers, registration)
		containerID := registration.Identity.ContainerID
		if containerID == "" {
			containerID = registration.Identity.ProviderInstanceID
		}
		containers = append(containers, control.ContainerReport{
			ContainerID:        containerID,
			ContainerKind:      opts.Runtime.Kind,
			ContainerName:      registration.Identity.ContainerName,
			ProviderID:         registration.Identity.ProviderID,
			ProviderInstanceID: registration.Identity.ProviderInstanceID,
			Image:              spec.Image,
			State:              "declared",
			Health:             control.HealthReport{Status: "declared", CheckedAt: now},
			Labels: map[string]string{
				"pangaea.provider_id":          registration.Identity.ProviderID,
				"pangaea.provider_instance_id": registration.Identity.ProviderInstanceID,
				"pangaea.service":              string(registration.Identity.Service),
			},
		})
	}
	if len(reconciledContainers) > 0 {
		containers = append([]control.ContainerReport(nil), reconciledContainers...)
	}
	if err := writeEnvelope(conn, control.MessageTypeProviderInventoryReport, "node_inventory_"+now.Format("20060102150405.000000000"), control.ProviderInventoryReport{
		Mode:       "full",
		NodeID:     opts.NodeID,
		HostName:   opts.HostName,
		Providers:  providers,
		Containers: containers,
		Resources:  opts.Resources,
		ReportedAt: now,
	}); err != nil {
		return err
	}
	return readControlOK(conn)
}

func reconcileProviderContainers(ctx context.Context, opts ControlClientOptions) ([]control.ContainerReport, error) {
	if opts.ContainerRuntime == nil || len(opts.ProviderSpecs) == 0 {
		return nil, nil
	}
	reports := make([]control.ContainerReport, 0, len(opts.ProviderSpecs))
	for _, spec := range opts.ProviderSpecs {
		result, err := ReconcileProviderContainerWithOptions(ctx, opts.ContainerRuntime, spec, opts.NodeID, opts.HostName, ContainerSpecOptions{
			RouterControlURL: opts.ControlURL,
			RouterDataURL:    opts.RouterDataURL,
			StreamTokenKey:   opts.StreamTokenKey,
			RouterPeerToken:  opts.PeerToken,
			ContainerKind:    opts.Runtime.Kind,
		})
		if err != nil {
			return nil, err
		}
		reports = append(reports, result.Report)
	}
	return reports, nil
}

func writeEnvelope(conn *websocket.Conn, messageType control.MessageType, id string, payload any) error {
	data, err := control.Marshal(messageType, id, time.Now().UTC(), payload)
	if err != nil {
		return err
	}
	return conn.WriteMessage(websocket.TextMessage, data)
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
		return fmt.Errorf("%w: %s", ErrNodeAgentConfig, payload.Message)
	}
	if env.Type != control.MessageTypeAck {
		return fmt.Errorf("%w: unexpected ack type %q", ErrNodeAgentConfig, env.Type)
	}
	payload, err := control.Decode[control.Ack](env, control.MessageTypeAck)
	if err != nil {
		return err
	}
	if !payload.OK {
		return fmt.Errorf("%w: %s", ErrNodeAgentConfig, payload.Message)
	}
	return nil
}
