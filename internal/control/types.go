// Package control defines the v2 provider control-plane wire protocol.
package control

import (
	"encoding/json"
	"time"

	"github.com/0xc0de1ab/pangaea/internal/provider"
)

const ProtocolVersion = "provider-protocol/v1"

type MessageType string

const (
	MessageTypeNodeHello               MessageType = "node.hello"
	MessageTypeNodeHeartbeat           MessageType = "node.heartbeat"
	MessageTypeProviderRegister        MessageType = "provider.register"
	MessageTypeProviderHeartbeat       MessageType = "provider.heartbeat"
	MessageTypeProviderInventoryReport MessageType = "provider.inventory.report"
	MessageTypeProviderAuthReport      MessageType = "provider.auth.report"
	MessageTypeProviderUsageReport     MessageType = "provider.usage.report"
	MessageTypeAuthSnapshot            MessageType = "auth.snapshot"
	MessageTypeAuthPush                MessageType = "auth.push"
	MessageTypeAuthRefreshRequest      MessageType = "auth.refresh.request"
	MessageTypeAuthRefreshResult       MessageType = "auth.refresh.result"
	MessageTypeProviderDrain           MessageType = "provider.drain"
	MessageTypeStreamOpenRequest       MessageType = "stream.open.request"
	MessageTypeStreamOpenReady         MessageType = "stream.open.ready"
	MessageTypeStreamCancel            MessageType = "stream.cancel"
	MessageTypeStreamClosed            MessageType = "stream.closed"
	MessageTypeAck                     MessageType = "control.ack"
	MessageTypeControlError            MessageType = "control.error"
)

func (t MessageType) Valid() bool {
	switch t {
	case MessageTypeNodeHello,
		MessageTypeNodeHeartbeat,
		MessageTypeProviderRegister,
		MessageTypeProviderHeartbeat,
		MessageTypeProviderInventoryReport,
		MessageTypeProviderAuthReport,
		MessageTypeProviderUsageReport,
		MessageTypeAuthSnapshot,
		MessageTypeAuthPush,
		MessageTypeAuthRefreshRequest,
		MessageTypeAuthRefreshResult,
		MessageTypeProviderDrain,
		MessageTypeStreamOpenRequest,
		MessageTypeStreamOpenReady,
		MessageTypeStreamCancel,
		MessageTypeStreamClosed,
		MessageTypeAck,
		MessageTypeControlError:
		return true
	default:
		return false
	}
}

type Trace struct {
	RequestID          string `json:"request_id,omitempty"`
	RouteID            string `json:"route_id,omitempty"`
	TenantID           string `json:"tenant_id,omitempty"`
	UserID             string `json:"user_id,omitempty"`
	APIKeyID           string `json:"api_key_id,omitempty"`
	NodeID             string `json:"node_id,omitempty"`
	HostName           string `json:"host_name,omitempty"`
	ContainerID        string `json:"container_id,omitempty"`
	ProviderID         string `json:"provider_id,omitempty"`
	ProviderInstanceID string `json:"provider_instance_id,omitempty"`
	AccountID          string `json:"account_id,omitempty"`
	ModelID            string `json:"model_id,omitempty"`
	StreamID           string `json:"stream_id,omitempty"`
}

type Envelope struct {
	Version string          `json:"version"`
	Type    MessageType     `json:"type"`
	ID      string          `json:"id"`
	SentAt  time.Time       `json:"sent_at"`
	Trace   Trace           `json:"trace,omitempty"`
	Payload json.RawMessage `json:"payload"`
}

type RuntimeInfo struct {
	Kind     string `json:"kind"`
	Version  string `json:"version,omitempty"`
	Rootless bool   `json:"rootless,omitempty"`
}

type NodeHello struct {
	NodeID       string      `json:"node_id"`
	AgentVersion string      `json:"agent_version"`
	OS           string      `json:"os"`
	Arch         string      `json:"arch"`
	Runtime      RuntimeInfo `json:"runtime"`
	Capabilities []string    `json:"capabilities,omitempty"`
}

type NodeHeartbeat struct {
	NodeID     string        `json:"node_id"`
	HostName   string        `json:"host_name,omitempty"`
	Health     HealthReport  `json:"health,omitempty"`
	Resources  ResourceUsage `json:"resources,omitempty"`
	ReportedAt time.Time     `json:"reported_at,omitempty"`
}

type ProviderRegister = provider.Registration
type ProviderRegisterPayload = provider.Registration

type ProviderHeartbeat struct {
	ProviderInstanceID string              `json:"provider_instance_id"`
	Health             provider.Health     `json:"health,omitempty"`
	Auth               provider.AuthState  `json:"auth,omitempty"`
	Limits             provider.LimitState `json:"limits,omitempty"`
	ReportedAt         time.Time           `json:"reported_at,omitempty"`
}

type ProviderInventoryReport struct {
	Mode       string                  `json:"mode"`
	NodeID     string                  `json:"node_id,omitempty"`
	HostName   string                  `json:"host_name,omitempty"`
	Providers  []provider.Registration `json:"providers,omitempty"`
	Containers []ContainerReport       `json:"containers,omitempty"`
	Resources  ResourceUsage           `json:"resources,omitempty"`
	ReportedAt time.Time               `json:"reported_at,omitempty"`
}

type ContainerReport struct {
	ContainerID        string            `json:"container_id"`
	ProviderID         string            `json:"provider_id,omitempty"`
	ProviderInstanceID string            `json:"provider_instance_id,omitempty"`
	Image              string            `json:"image,omitempty"`
	State              string            `json:"state,omitempty"`
	Health             HealthReport      `json:"health,omitempty"`
	Resources          ResourceUsage     `json:"resources,omitempty"`
	Labels             map[string]string `json:"labels,omitempty"`
	StartedAt          time.Time         `json:"started_at,omitempty"`
	Extensions         map[string]any    `json:"extensions,omitempty"`
}

type ProviderAuthReport struct {
	ProviderInstanceID string             `json:"provider_instance_id"`
	Auth               provider.AuthState `json:"auth"`
	ReportedAt         time.Time          `json:"reported_at,omitempty"`
}

type ProviderUsageReport struct {
	ProviderInstanceID string               `json:"provider_instance_id"`
	Usage              provider.UsageReport `json:"usage"`
	ReportedAt         time.Time            `json:"reported_at,omitempty"`
}

type AuthSnapshot struct {
	ProviderInstanceID string             `json:"provider_instance_id"`
	AccountID          string             `json:"account_id,omitempty"`
	Auth               provider.AuthState `json:"auth"`
	Fingerprint        string             `json:"fingerprint,omitempty"`
	Source             string             `json:"source,omitempty"`
	Filename           string             `json:"filename,omitempty"`
	Format             string             `json:"format,omitempty"`
	Raw                []byte             `json:"raw,omitempty"`
	ObservedAt         time.Time          `json:"observed_at,omitempty"`
	ReportedAt         time.Time          `json:"reported_at,omitempty"`
}

type AuthPush struct {
	PushID             string             `json:"push_id"`
	ProviderInstanceID string             `json:"provider_instance_id"`
	AccountID          string             `json:"account_id,omitempty"`
	Auth               provider.AuthState `json:"auth"`
	Fingerprint        string             `json:"fingerprint,omitempty"`
	Source             string             `json:"source,omitempty"`
	Filename           string             `json:"filename,omitempty"`
	Format             string             `json:"format,omitempty"`
	Raw                []byte             `json:"raw,omitempty"`
	Reason             string             `json:"reason,omitempty"`
	DeadlineAt         time.Time          `json:"deadline_at,omitempty"`
}

type AuthRefreshRequest struct {
	RefreshID          string    `json:"refresh_id"`
	ProviderInstanceID string    `json:"provider_instance_id"`
	AccountID          string    `json:"account_id,omitempty"`
	Reason             string    `json:"reason,omitempty"`
	DeadlineAt         time.Time `json:"deadline_at,omitempty"`
}

type AuthRefreshResult struct {
	RefreshID          string             `json:"refresh_id"`
	ProviderInstanceID string             `json:"provider_instance_id"`
	Auth               provider.AuthState `json:"auth"`
	OK                 bool               `json:"ok"`
	Error              *ErrorPayload      `json:"error,omitempty"`
	ReportedAt         time.Time          `json:"reported_at,omitempty"`
}

type ProviderDrain struct {
	ProviderInstanceID string    `json:"provider_instance_id"`
	Reason             string    `json:"reason,omitempty"`
	Drain              bool      `json:"drain"`
	DeadlineAt         time.Time `json:"deadline_at,omitempty"`
}

type StreamOpenRequest struct {
	StreamID           string    `json:"stream_id"`
	RequestID          string    `json:"request_id"`
	RouteID            string    `json:"route_id"`
	ProviderInstanceID string    `json:"provider_instance_id"`
	TenantID           string    `json:"tenant_id"`
	UserID             string    `json:"user_id"`
	Model              string    `json:"model"`
	DeadlineAt         time.Time `json:"deadline_at"`
	Protocol           string    `json:"protocol"`
	CapabilityToken    string    `json:"capability_token"`
}

type StreamOpenReady struct {
	StreamID           string    `json:"stream_id"`
	RequestID          string    `json:"request_id,omitempty"`
	ProviderInstanceID string    `json:"provider_instance_id"`
	Transport          string    `json:"transport,omitempty"`
	Endpoint           string    `json:"endpoint,omitempty"`
	CapabilityToken    string    `json:"capability_token,omitempty"`
	ReadyAt            time.Time `json:"ready_at,omitempty"`
}

type StreamCancel struct {
	StreamID  string    `json:"stream_id"`
	RequestID string    `json:"request_id,omitempty"`
	Reason    string    `json:"reason,omitempty"`
	SentAt    time.Time `json:"sent_at,omitempty"`
}

type StreamClosed struct {
	StreamID           string                `json:"stream_id"`
	RequestID          string                `json:"request_id,omitempty"`
	ProviderInstanceID string                `json:"provider_instance_id,omitempty"`
	Reason             string                `json:"reason,omitempty"`
	Error              *ErrorPayload         `json:"error,omitempty"`
	Usage              *provider.UsageReport `json:"usage,omitempty"`
	ClosedAt           time.Time             `json:"closed_at,omitempty"`
}

type HealthReport struct {
	State      string    `json:"state,omitempty"`
	Status     string    `json:"status,omitempty"`
	Reason     string    `json:"reason,omitempty"`
	ReadySince time.Time `json:"ready_since,omitempty"`
	CheckedAt  time.Time `json:"checked_at,omitempty"`
}

type ResourceUsage struct {
	CPUPercent      float64 `json:"cpu_percent,omitempty"`
	MemoryBytes     int64   `json:"memory_bytes,omitempty"`
	MemoryPeakBytes int64   `json:"memory_peak_bytes,omitempty"`
	OOMCount        int64   `json:"oom_count,omitempty"`
}

type ErrorPayload struct {
	Code      string         `json:"code"`
	Message   string         `json:"message"`
	Retryable bool           `json:"retryable,omitempty"`
	Details   map[string]any `json:"details,omitempty"`
}

type Ack struct {
	ReplyTo string `json:"reply_to"`
	OK      bool   `json:"ok"`
	Message string `json:"message,omitempty"`
}

type ControlError struct {
	ReplyTo   string `json:"reply_to,omitempty"`
	Code      string `json:"code"`
	Message   string `json:"message"`
	Retryable bool   `json:"retryable,omitempty"`
}

type NodeHelloPayload = NodeHello
type NodeHeartbeatPayload = NodeHeartbeat
type ProviderHeartbeatPayload = ProviderHeartbeat
type ProviderInventoryReportPayload = ProviderInventoryReport
type ProviderAuthReportPayload = ProviderAuthReport
type ProviderUsageReportPayload = ProviderUsageReport
type AuthSnapshotPayload = AuthSnapshot
type AuthPushPayload = AuthPush
type AuthRefreshRequestPayload = AuthRefreshRequest
type AuthRefreshResultPayload = AuthRefreshResult
type ProviderDrainPayload = ProviderDrain
type StreamOpenRequestPayload = StreamOpenRequest
type StreamOpenReadyPayload = StreamOpenReady
type StreamCancelPayload = StreamCancel
type StreamClosedPayload = StreamClosed
type AckPayload = Ack
type ControlErrorPayload = ControlError
