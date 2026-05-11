package models

// Anthropic Error Response
type AnthropicErrorResponse struct {
	Type  string `json:"type"`
	Error struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error"`
}

type AnthropicRequest struct {
	Model     string            `json:"model"`
	Messages  []AnthropicMessage `json:"messages"`
	System    string            `json:"system,omitempty"`
	MaxTokens int               `json:"max_tokens"`
	Stream    bool              `json:"stream,omitempty"`
	Tools     []AnthropicTool   `json:"tools,omitempty"`
}

type AnthropicMessage struct {
	Role    string      `json:"role"`
	Content interface{} `json:"content"` // Can be string or []ContentBlock
}

type AnthropicContentBlock struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
	// For tool_use
	ID    string                 `json:"id,omitempty"`
	Name  string                 `json:"name,omitempty"`
	Input map[string]interface{} `json:"input,omitempty"`
	// For tool_result
	ToolUseID string      `json:"tool_use_id,omitempty"`
	Content   string      `json:"content,omitempty"`
	IsError   bool        `json:"is_error,omitempty"`
}

type AnthropicTool struct {
	Name        string      `json:"name"`
	Description string      `json:"description"`
	InputSchema interface{} `json:"input_schema"`
}

type AnthropicResponse struct {
	ID      string        `json:"id"`
	Type    string        `json:"type"`
	Role    string        `json:"role"`
	Model   string        `json:"model"`
	Content []interface{} `json:"content"`
	Usage   *UsageReport  `json:"usage,omitempty"`
}
