package provider

import (
	"errors"
	"strings"
)

var (
	ErrInvalidIdentity     = errors.New("invalid provider identity")
	ErrInvalidRegistration = errors.New("invalid provider registration")
)

func (i ProviderIdentity) Validate() error {
	if blank(i.ProviderID) || blank(i.ProviderInstanceID) || blank(i.NodeID) || blank(i.HostName) {
		return ErrInvalidIdentity
	}
	if !i.Service.Valid() || !i.Kind.Valid() {
		return ErrInvalidIdentity
	}
	return nil
}

func (r Registration) Validate() error {
	if err := r.Identity.Validate(); err != nil {
		return err
	}
	if len(r.Capabilities) == 0 {
		return ErrInvalidRegistration
	}
	for _, capability := range r.Capabilities {
		if !capability.Valid() {
			return ErrInvalidRegistration
		}
	}
	if !r.Health.Status.Valid() {
		return ErrInvalidRegistration
	}
	return nil
}

func (k Kind) Valid() bool {
	switch k {
	case KindCLIContainer, KindAPICompatible, KindSidecar, KindSimulator:
		return true
	}
	return false
}

func (s Service) Valid() bool {
	switch s {
	case ServiceCodex, ServiceClaude, ServiceGemini, ServiceOpenAI, ServiceAnthropic,
		ServiceGLM, ServiceMiniMAX, ServiceDeepSeek, ServiceAntigravity, ServiceCline,
		ServiceGitHubCopilot:
		return true
	}
	return false
}

func (c Capability) Valid() bool {
	switch c {
	case CapabilityOpenAIChat, CapabilityOpenAIResponses, CapabilityAnthropicMessages,
		CapabilityGeminiGenerateContent, CapabilityStreamSSE, CapabilityUsageRead,
		CapabilityModelsRead,
		CapabilityAuthFile, CapabilityAuthRefreshOneshot, CapabilityAgentWorkspaceRead,
		CapabilityAgentWorkspaceWrite, CapabilityAgentTerminal, CapabilityCodeCompletion:
		return true
	}
	return false
}

func (s HealthStatus) Valid() bool {
	switch s {
	case HealthUnknown, HealthReady, HealthDegraded, HealthDraining, HealthDown:
		return true
	}
	return false
}

func (s AuthStatus) Valid() bool {
	switch s {
	case AuthUnknown, AuthHealthy, AuthRefreshSoon, AuthRefreshing, AuthExpired, AuthRevoked,
		AuthConflict, AuthUnavailable:
		return true
	}
	return false
}

func blank(s string) bool {
	return strings.TrimSpace(s) == ""
}
