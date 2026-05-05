// Package compat defines the canonical request, response, and streaming event
// model shared by the v2 router and provider shims.
package compat

type APIDialect string

const (
	APIDialectOpenAI    APIDialect = "openai"
	APIDialectAnthropic APIDialect = "anthropic"
	APIDialectGemini    APIDialect = "gemini"
)

type UnsupportedFeaturePolicy string

const (
	UnsupportedFeatureReject      UnsupportedFeaturePolicy = "reject"
	UnsupportedFeatureDrop        UnsupportedFeaturePolicy = "drop"
	UnsupportedFeatureApproximate UnsupportedFeaturePolicy = "approximate"
)

type MessageRole string

const (
	MessageRoleSystem    MessageRole = "system"
	MessageRoleDeveloper MessageRole = "developer"
	MessageRoleUser      MessageRole = "user"
	MessageRoleAssistant MessageRole = "assistant"
	MessageRoleTool      MessageRole = "tool"
)

type ContentPartType string

const (
	ContentPartText ContentPartType = "text"
)

type ToolCallType string

const (
	ToolCallFunction ToolCallType = "function"
)

type Request struct {
	ID                  string                   `json:"id,omitempty"`
	Dialect             APIDialect               `json:"dialect"`
	Model               string                   `json:"model"`
	Messages            []Message                `json:"messages"`
	Temperature         *float64                 `json:"temperature,omitempty"`
	MaxOutputTokens     int                      `json:"max_output_tokens,omitempty"`
	Stream              bool                     `json:"stream,omitempty"`
	UnsupportedFeatures UnsupportedFeaturePolicy `json:"unsupported_features,omitempty"`
}

type Message struct {
	Role       MessageRole   `json:"role"`
	Name       string        `json:"name,omitempty"`
	Content    []ContentPart `json:"content,omitempty"`
	ToolCalls  []ToolCall    `json:"tool_calls,omitempty"`
	ToolCallID string        `json:"tool_call_id,omitempty"`
}

type ContentPart struct {
	Type ContentPartType `json:"type"`
	Text string          `json:"text,omitempty"`
}

type ToolCall struct {
	Index     int          `json:"index,omitempty"`
	ID        string       `json:"id,omitempty"`
	Type      ToolCallType `json:"type"`
	Name      string       `json:"name,omitempty"`
	Arguments string       `json:"arguments,omitempty"`
}

type Response struct {
	ID         string     `json:"id,omitempty"`
	Dialect    APIDialect `json:"dialect"`
	Model      string     `json:"model"`
	Message    Message    `json:"message"`
	StopReason string     `json:"stop_reason,omitempty"`
	Usage      Usage      `json:"usage,omitempty"`
}

type Usage struct {
	InputTokens  int64 `json:"input_tokens,omitempty"`
	OutputTokens int64 `json:"output_tokens,omitempty"`
	TotalTokens  int64 `json:"total_tokens,omitempty"`
}

type EventType string

const (
	EventMessageStart  EventType = "message_start"
	EventContentDelta  EventType = "content_delta"
	EventToolCallDelta EventType = "tool_call_delta"
	EventUsageDelta    EventType = "usage_delta"
	EventError         EventType = "error"
	EventDone          EventType = "done"
)

type Event struct {
	ResponseID    string             `json:"response_id,omitempty"`
	Dialect       APIDialect         `json:"dialect,omitempty"`
	Model         string             `json:"model,omitempty"`
	Type          EventType          `json:"type"`
	Message       *Message           `json:"message,omitempty"`
	ContentDelta  *ContentPart       `json:"content_delta,omitempty"`
	ToolCallDelta *ToolCall          `json:"tool_call_delta,omitempty"`
	UsageDelta    *Usage             `json:"usage_delta,omitempty"`
	Error         *EventErrorPayload `json:"error,omitempty"`
	DoneReason    string             `json:"done_reason,omitempty"`
}

type EventErrorPayload struct {
	Code    string `json:"code,omitempty"`
	Message string `json:"message"`
}
