// Package provider defines the shared identity and capability model used by
// the v2 router, node-agent, and provider-shim packages.
package provider

import "time"

type Kind string

const (
	KindCLIContainer  Kind = "cli-container"
	KindAPICompatible Kind = "api-compatible"
	KindSidecar       Kind = "sidecar-agent"
	KindSimulator     Kind = "simulator"
)

type Service string

const (
	ServiceCodex         Service = "codex"
	ServiceClaude        Service = "claude"
	ServiceGemini        Service = "gemini"
	ServiceOpenAI        Service = "openai"
	ServiceAnthropic     Service = "anthropic"
	ServiceGLM           Service = "glm"
	ServiceMiniMAX       Service = "minimax"
	ServiceDeepSeek      Service = "deepseek"
	ServiceAntigravity   Service = "antigravity"
	ServiceCline         Service = "cline"
	ServiceGitHubCopilot Service = "github-copilot"
)

type Capability string

const (
	CapabilityOpenAIChat            Capability = "api.openai.chat"
	CapabilityOpenAIResponses       Capability = "api.openai.responses"
	CapabilityAnthropicMessages     Capability = "api.anthropic.messages"
	CapabilityGeminiGenerateContent Capability = "api.gemini.generateContent"
	CapabilityStreamSSE             Capability = "stream.sse"
	CapabilityUsageRead             Capability = "usage.read"
	CapabilityModelsRead            Capability = "models.read"
	CapabilityAuthFile              Capability = "auth.file"
	CapabilityAuthRefreshOneshot    Capability = "auth.refresh.oneshot"
	CapabilityAgentWorkspaceRead    Capability = "agent.workspace.read"
	CapabilityAgentWorkspaceWrite   Capability = "agent.workspace.write"
	CapabilityAgentTerminal         Capability = "agent.terminal"
	CapabilityCodeCompletion        Capability = "code.completion"
)

type ProviderIdentity struct {
	ProviderID         string  `json:"provider_id"`
	ProviderInstanceID string  `json:"provider_instance_id"`
	NodeID             string  `json:"node_id"`
	HostName           string  `json:"host_name"`
	ContainerID        string  `json:"container_id,omitempty"`
	Service            Service `json:"service"`
	Kind               Kind    `json:"kind"`
	Account            Account `json:"account,omitempty"`
}

type Account struct {
	ID      string `json:"id,omitempty"`
	Display string `json:"display,omitempty"`
}

type Model struct {
	ID            string       `json:"id"`
	Aliases       []string     `json:"aliases,omitempty"`
	Capabilities  []Capability `json:"capabilities,omitempty"`
	ContextTokens int          `json:"context_tokens,omitempty"`
}

type HealthStatus string

const (
	HealthUnknown  HealthStatus = "unknown"
	HealthReady    HealthStatus = "ready"
	HealthDegraded HealthStatus = "degraded"
	HealthDraining HealthStatus = "draining"
	HealthDown     HealthStatus = "down"
)

type Health struct {
	Status    HealthStatus `json:"status"`
	Reason    string       `json:"reason,omitempty"`
	CheckedAt time.Time    `json:"checked_at,omitempty"`
}

type AuthStatus string

const (
	AuthUnknown     AuthStatus = "unknown"
	AuthHealthy     AuthStatus = "healthy"
	AuthRefreshSoon AuthStatus = "refresh_soon"
	AuthRefreshing  AuthStatus = "refreshing"
	AuthExpired     AuthStatus = "expired"
	AuthRevoked     AuthStatus = "revoked"
	AuthConflict    AuthStatus = "conflict"
	AuthUnavailable AuthStatus = "unavailable"
)

type AuthState struct {
	Status          AuthStatus `json:"status"`
	Account         Account    `json:"account,omitempty"`
	ExpiresAt       time.Time  `json:"expires_at,omitempty"`
	Refreshable     bool       `json:"refreshable"`
	LastRefreshAt   time.Time  `json:"last_refresh_at,omitempty"`
	LastRefreshErr  string     `json:"last_refresh_error,omitempty"`
	SelectedSource  string     `json:"selected_source,omitempty"`
	ReplicaCount    int        `json:"replica_count,omitempty"`
	BootstrapSource string     `json:"bootstrap_source,omitempty"`
}

type LimitState struct {
	MaxConcurrency int `json:"max_concurrency,omitempty"`
	QueueDepth     int `json:"queue_depth,omitempty"`
	ActiveStreams  int `json:"active_streams,omitempty"`
}

type UsageReport struct {
	ObservedAt    time.Time `json:"observed_at"`
	Source        string    `json:"source,omitempty"`
	Requests      int64     `json:"requests,omitempty"`
	InputTokens   int64     `json:"input_tokens,omitempty"`
	OutputTokens  int64     `json:"output_tokens,omitempty"`
	TotalTokens   int64     `json:"total_tokens,omitempty"`
	NativeSummary any       `json:"native_summary,omitempty"`
}

type Registration struct {
	Identity     ProviderIdentity `json:"identity"`
	Capabilities []Capability     `json:"capabilities,omitempty"`
	Models       []Model          `json:"models,omitempty"`
	Health       Health           `json:"health"`
	Auth         AuthState        `json:"auth,omitempty"`
	Limits       LimitState       `json:"limits,omitempty"`
	RegisteredAt time.Time        `json:"registered_at"`
}
