package compat

import (
	"encoding/json"
	"strings"
)

type AnthropicMessagesRequest struct {
	Model       string                    `json:"model"`
	MaxTokens   int                       `json:"max_tokens,omitempty"`
	Messages    []AnthropicMessage        `json:"messages"`
	System      json.RawMessage           `json:"system,omitempty"`
	Temperature *float64                  `json:"temperature,omitempty"`
	Stream      bool                      `json:"stream,omitempty"`
	Tools       []AnthropicToolDefinition `json:"tools,omitempty"`
}

type AnthropicMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

type AnthropicContentBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text,omitempty"`
	ID        string          `json:"id,omitempty"`
	Name      string          `json:"name,omitempty"`
	Input     json.RawMessage `json:"input,omitempty"`
	ToolUseID string          `json:"tool_use_id,omitempty"`
	Content   json.RawMessage `json:"content,omitempty"`
}

type AnthropicToolDefinition struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"input_schema,omitempty"`
}

type AnthropicMessagesResponse struct {
	ID           string                  `json:"id,omitempty"`
	Type         string                  `json:"type"`
	Role         string                  `json:"role"`
	Model        string                  `json:"model"`
	Content      []AnthropicContentBlock `json:"content"`
	StopReason   string                  `json:"stop_reason,omitempty"`
	StopSequence *string                 `json:"stop_sequence,omitempty"`
	Usage        AnthropicUsage          `json:"usage"`
}

type AnthropicUsage struct {
	InputTokens  int64 `json:"input_tokens,omitempty"`
	OutputTokens int64 `json:"output_tokens,omitempty"`
}

func AnthropicMessagesRequestToCanonical(in AnthropicMessagesRequest) (Request, error) {
	out := Request{
		Dialect:             APIDialectAnthropic,
		Model:               in.Model,
		MaxOutputTokens:     in.MaxTokens,
		Temperature:         in.Temperature,
		Stream:              in.Stream,
		UnsupportedFeatures: UnsupportedFeatureReject,
	}
	if system, err := parseAnthropicSystem(in.System); err != nil {
		return Request{}, err
	} else if system != "" {
		out.Messages = append(out.Messages, Message{
			Role:    MessageRoleSystem,
			Content: []ContentPart{{Type: ContentPartText, Text: system}},
		})
	}
	for _, message := range in.Messages {
		converted, err := anthropicMessageToCanonical(message)
		if err != nil {
			return Request{}, err
		}
		out.Messages = append(out.Messages, converted...)
	}
	if err := out.Validate(); err != nil {
		return Request{}, err
	}
	return out, nil
}

func AnthropicMessagesRequestFromCanonical(in Request) (AnthropicMessagesRequest, error) {
	if err := in.Validate(); err != nil {
		return AnthropicMessagesRequest{}, err
	}
	out := AnthropicMessagesRequest{
		Model:       in.Model,
		MaxTokens:   in.MaxOutputTokens,
		Messages:    make([]AnthropicMessage, 0, len(in.Messages)),
		Temperature: in.Temperature,
		Stream:      in.Stream,
	}
	var systemParts []string
	for _, message := range in.Messages {
		if message.Role == MessageRoleSystem || message.Role == MessageRoleDeveloper {
			text, err := contentText(message.Content)
			if err != nil {
				return AnthropicMessagesRequest{}, err
			}
			if text != "" {
				systemParts = append(systemParts, text)
			}
			continue
		}
		converted, err := canonicalMessageToAnthropic(message)
		if err != nil {
			return AnthropicMessagesRequest{}, err
		}
		out.Messages = append(out.Messages, converted)
	}
	if len(systemParts) > 0 {
		raw, err := json.Marshal(strings.Join(systemParts, "\n"))
		if err != nil {
			return AnthropicMessagesRequest{}, ErrInvalidRequest
		}
		out.System = raw
	}
	if len(out.Messages) == 0 {
		return AnthropicMessagesRequest{}, ErrInvalidRequest
	}
	return out, nil
}

func AnthropicMessagesResponseFromCanonical(in Response) (AnthropicMessagesResponse, error) {
	if err := in.Validate(); err != nil {
		return AnthropicMessagesResponse{}, err
	}
	content := make([]AnthropicContentBlock, 0, len(in.Message.Content)+len(in.Message.ToolCalls))
	for _, part := range in.Message.Content {
		if part.Type != ContentPartText {
			return AnthropicMessagesResponse{}, ErrInvalidResponse
		}
		content = append(content, AnthropicContentBlock{Type: "text", Text: part.Text})
	}
	for _, toolCall := range in.Message.ToolCalls {
		input := json.RawMessage(`{}`)
		if strings.TrimSpace(toolCall.Arguments) != "" {
			input = json.RawMessage(toolCall.Arguments)
		}
		content = append(content, AnthropicContentBlock{
			Type:  "tool_use",
			ID:    toolCall.ID,
			Name:  toolCall.Name,
			Input: input,
		})
	}
	if len(content) == 0 {
		content = append(content, AnthropicContentBlock{Type: "text", Text: ""})
	}
	return AnthropicMessagesResponse{
		ID:         in.ID,
		Type:       "message",
		Role:       string(MessageRoleAssistant),
		Model:      in.Model,
		Content:    content,
		StopReason: canonicalStopToAnthropic(in.StopReason),
		Usage: AnthropicUsage{
			InputTokens:  in.Usage.InputTokens,
			OutputTokens: in.Usage.OutputTokens,
		},
	}, nil
}

func AnthropicMessagesResponseToCanonical(in AnthropicMessagesResponse) (Response, error) {
	raw, err := json.Marshal(in.Content)
	if err != nil {
		return Response{}, ErrInvalidResponse
	}
	messages, err := anthropicMessageToCanonical(AnthropicMessage{Role: in.Role, Content: raw})
	if err != nil || len(messages) == 0 {
		return Response{}, ErrInvalidResponse
	}
	message := messages[0]
	message.Role = MessageRoleAssistant
	total := in.Usage.InputTokens + in.Usage.OutputTokens
	out := Response{
		ID:      in.ID,
		Dialect: APIDialectAnthropic,
		Model:   in.Model,
		Message: message,
		Usage: Usage{
			InputTokens:  in.Usage.InputTokens,
			OutputTokens: in.Usage.OutputTokens,
			TotalTokens:  total,
		},
		StopReason: anthropicStopToCanonical(in.StopReason),
	}
	if err := out.Validate(); err != nil {
		return Response{}, err
	}
	return out, nil
}

func parseAnthropicSystem(raw json.RawMessage) (string, error) {
	if len(raw) == 0 {
		return "", nil
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return text, nil
	}
	blocks, err := parseAnthropicContentBlocks(raw)
	if err != nil {
		return "", err
	}
	return textFromAnthropicBlocks(blocks)
}

func anthropicMessageToCanonical(in AnthropicMessage) ([]Message, error) {
	role := anthropicRoleToCanonical(in.Role)
	var text string
	if err := json.Unmarshal(in.Content, &text); err == nil {
		return []Message{{Role: role, Content: []ContentPart{{Type: ContentPartText, Text: text}}}}, nil
	}

	blocks, err := parseAnthropicContentBlocks(in.Content)
	if err != nil {
		return nil, err
	}
	out := make([]Message, 0, 1)
	message := Message{Role: role}
	for _, block := range blocks {
		switch block.Type {
		case "text":
			if block.Text != "" {
				message.Content = append(message.Content, ContentPart{Type: ContentPartText, Text: block.Text})
			}
		case "tool_use":
			message.ToolCalls = append(message.ToolCalls, ToolCall{
				Index:     len(message.ToolCalls),
				ID:        block.ID,
				Type:      ToolCallFunction,
				Name:      block.Name,
				Arguments: string(block.Input),
			})
		case "tool_result":
			toolText, err := textFromAnthropicRawContent(block.Content)
			if err != nil {
				return nil, err
			}
			out = append(out, Message{
				Role:       MessageRoleTool,
				ToolCallID: block.ToolUseID,
				Content:    []ContentPart{{Type: ContentPartText, Text: toolText}},
			})
		default:
			return nil, ErrInvalidRequest
		}
	}
	if len(message.Content) > 0 || len(message.ToolCalls) > 0 {
		out = append([]Message{message}, out...)
	}
	if len(out) == 0 {
		return nil, ErrInvalidRequest
	}
	return out, nil
}

func canonicalMessageToAnthropic(in Message) (AnthropicMessage, error) {
	if err := in.Validate(); err != nil {
		return AnthropicMessage{}, err
	}
	blocks := make([]AnthropicContentBlock, 0, len(in.Content)+len(in.ToolCalls))
	for _, part := range in.Content {
		if part.Type != ContentPartText {
			return AnthropicMessage{}, ErrInvalidRequest
		}
		blocks = append(blocks, AnthropicContentBlock{Type: "text", Text: part.Text})
	}
	for _, toolCall := range in.ToolCalls {
		input := json.RawMessage(`{}`)
		if strings.TrimSpace(toolCall.Arguments) != "" {
			input = json.RawMessage(toolCall.Arguments)
		}
		blocks = append(blocks, AnthropicContentBlock{
			Type:  "tool_use",
			ID:    toolCall.ID,
			Name:  toolCall.Name,
			Input: input,
		})
	}
	role := "user"
	switch in.Role {
	case MessageRoleAssistant:
		role = "assistant"
	case MessageRoleTool:
		role = "user"
		text, err := contentText(in.Content)
		if err != nil {
			return AnthropicMessage{}, err
		}
		rawText, err := json.Marshal(text)
		if err != nil {
			return AnthropicMessage{}, ErrInvalidRequest
		}
		blocks = []AnthropicContentBlock{{
			Type:      "tool_result",
			ToolUseID: in.ToolCallID,
			Content:   rawText,
		}}
	default:
		role = "user"
	}
	raw, err := json.Marshal(blocks)
	if err != nil {
		return AnthropicMessage{}, ErrInvalidRequest
	}
	return AnthropicMessage{Role: role, Content: raw}, nil
}

func parseAnthropicContentBlocks(raw json.RawMessage) ([]AnthropicContentBlock, error) {
	var blocks []AnthropicContentBlock
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return nil, ErrInvalidRequest
	}
	return blocks, nil
}

func textFromAnthropicRawContent(raw json.RawMessage) (string, error) {
	if len(raw) == 0 {
		return "", nil
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return text, nil
	}
	blocks, err := parseAnthropicContentBlocks(raw)
	if err != nil {
		return "", err
	}
	return textFromAnthropicBlocks(blocks)
}

func textFromAnthropicBlocks(blocks []AnthropicContentBlock) (string, error) {
	parts := make([]string, 0, len(blocks))
	for _, block := range blocks {
		if block.Type != "text" {
			return "", ErrInvalidRequest
		}
		if block.Text != "" {
			parts = append(parts, block.Text)
		}
	}
	return strings.Join(parts, "\n"), nil
}

func anthropicRoleToCanonical(role string) MessageRole {
	switch strings.ToLower(role) {
	case "assistant":
		return MessageRoleAssistant
	case "tool":
		return MessageRoleTool
	default:
		return MessageRoleUser
	}
}

func canonicalStopToAnthropic(stop string) string {
	switch stop {
	case "max_tokens", "length":
		return "max_tokens"
	case "tool_calls", "tool_use":
		return "tool_use"
	case "stop_sequence":
		return "stop_sequence"
	default:
		return "end_turn"
	}
}

func anthropicStopToCanonical(stop string) string {
	switch stop {
	case "max_tokens":
		return "max_tokens"
	case "tool_use":
		return "tool_calls"
	case "stop_sequence":
		return "stop_sequence"
	default:
		return "stop"
	}
}
