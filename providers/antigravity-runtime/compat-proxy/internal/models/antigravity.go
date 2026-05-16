package models

// AntigravityRequest represents the payload for the ls_core GetModelResponse endpoint.
type AntigravityRequest struct {
	Model           string           `json:"model"`
	Prompt          string           `json:"prompt"`
	Stream          bool             `json:"stream"`
	Metadata        *RequestMetadata `json:"metadata"`
	ToolDefinitions []ToolDefinition `json:"tool_definitions,omitempty"`
	Media           []Media          `json:"media,omitempty"`
}

type Media struct {
	Image *ImageData `json:"image,omitempty"`
}

type ImageData struct {
	Base64Data string `json:"base64Data"`
	MimeType   string `json:"mimeType"`
}

type RequestMetadata struct {
	WorkspaceId string `json:"workspace_id,omitempty"`
	AuthToken   string `json:"auth_token,omitempty"`
}

type ToolDefinition struct {
	Name        string      `json:"name"`
	Description string      `json:"description"`
	InputSchema interface{} `json:"input_schema"`
}

type ToolResult struct {
	ToolUseId string `json:"tool_use_id"`
	Content   string `json:"content"`
	IsError   bool   `json:"is_error,omitempty"`
}

// GetAvailableModelsRequest for /exa.language_server_pb.LanguageServerService/GetAvailableModels
type GetAvailableModelsRequest struct {
	Metadata *RequestMetadata `json:"metadata"`
}

// GetAvailableModelsResponse
type GetAvailableModelsResponse struct {
	Response struct {
		Models map[string]ModelDetail `json:"models"`
	} `json:"response"`
}

type ModelDetail struct {
	Model          string     `json:"model"`
	Label          string     `json:"label,omitempty"`
	Kind           string     `json:"kind,omitempty"`
	GroupMembers   []string   `json:"groupMembers,omitempty"`
	MaxTokens      int        `json:"maxTokens,omitempty"`
	QuotaInfo      *QuotaInfo `json:"quotaInfo,omitempty"`
	SupportsImages bool       `json:"supportsImages,omitempty"`
}

type QuotaInfo struct {
	RemainingFraction float64 `json:"remainingFraction"`
	ResetTime         string  `json:"resetTime,omitempty"` // RFC3339 string
}

// GetCreditUsageSummaryResponse
type GetCreditUsageSummaryResponse struct {
	CreditUsageSummary *CreditUsageSummary `json:"credit_usage_summary"`
}

type CreditUsageSummary struct {
	TotalTokens     int        `json:"total_tokens"`
	RemainingTokens int        `json:"remaining_tokens"`
	QuotaInfo       *QuotaInfo `json:"quota_info,omitempty"`
}

// GetModelResponseRequest for /exa.language_server_pb.LanguageServerService/GetModelResponse
type GetModelResponseRequest struct {
	Metadata        *RequestMetadata `json:"metadata"`
	Prompt          string           `json:"prompt"`
	Model           string           `json:"model"`
	Stream          bool             `json:"stream"`
	ToolDefinitions []ToolDefinition `json:"tool_definitions,omitempty"`
	Media           []Media          `json:"media,omitempty"`
}

// GetUserStatusRequest for /exa.language_server_pb.LanguageServerService/GetUserStatus
type GetUserStatusRequest struct {
	Metadata *RequestMetadata `json:"metadata"`
}

// GetUserStatusResponse
type GetUserStatusResponse struct {
	UserStatus *UserStatus `json:"userStatus"`
}

type UserStatus struct {
	Name                   string                  `json:"name"`
	Email                  string                  `json:"email"`
	PlanStatus             *PlanStatus             `json:"planStatus"`
	UserTier               *UserTier               `json:"userTier"`
	CascadeModelConfigData *CascadeModelConfigData `json:"cascadeModelConfigData"`
}

type CascadeModelConfigData struct {
	ClientModelConfigs []ClientModelConfig `json:"clientModelConfigs"`
}

type ClientModelConfig struct {
	Label        string        `json:"label"`
	ModelOrAlias *ModelOrAlias `json:"modelOrAlias"`
	QuotaInfo    *QuotaInfo    `json:"quotaInfo"`
}

type ModelOrAlias struct {
	Model string `json:"model"`
}

type PlanStatus struct {
	PlanInfo               *PlanInfo `json:"planInfo"`
	AvailablePromptCredits int       `json:"availablePromptCredits"`
	AvailableFlowCredits   int       `json:"availableFlowCredits"`
}

type PlanInfo struct {
	PlanName string `json:"planName"`
}

type UserTier struct {
	ID                      string `json:"id"`
	Name                    string `json:"name"`
	UpgradeSubscriptionText string `json:"upgradeSubscriptionText"`
}

// AntigravityResponse represents the response from the ls_core GetModelResponse endpoint.
type AntigravityResponse struct {
	Response      string         `json:"response"`
	UsageMetadata *UsageMetadata `json:"usage_metadata,omitempty"`
}

type UsageMetadata struct {
	PromptTokenCount     int `json:"prompt_token_count"`
	CandidatesTokenCount int `json:"candidates_token_count"`
	TotalTokenCount      int `json:"total_token_count"`
}

type UsageReport struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}
