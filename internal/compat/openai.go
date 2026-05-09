package compat

import (
	"encoding/json"
	"strings"
)

type OpenAIChatRequest struct {
	Model               string              `json:"model"`
	Messages            []OpenAIChatMessage `json:"messages"`
	Tools               []OpenAIChatTool    `json:"tools,omitempty"`
	Temperature         *float64            `json:"temperature,omitempty"`
	ReasoningEffort     string              `json:"reasoning_effort,omitempty"`
	MaxTokens           int                 `json:"max_tokens,omitempty"`
	MaxCompletionTokens int                 `json:"max_completion_tokens,omitempty"`
	Stream              bool                `json:"stream,omitempty"`
}

type OpenAIChatMessage struct {
	Role       string               `json:"role"`
	Content    any                  `json:"content,omitempty"`
	Name       string               `json:"name,omitempty"`
	ToolCalls  []OpenAIChatToolCall `json:"tool_calls,omitempty"`
	ToolCallID string               `json:"tool_call_id,omitempty"`
}

type OpenAIContentPart struct {
	Type     string              `json:"type"`
	Text     string              `json:"text,omitempty"`
	ImageURL *OpenAIImageURLPart `json:"image_url,omitempty"`
}

type OpenAIImageURLPart struct {
	URL string `json:"url"`
}

type OpenAIChatToolCall struct {
	ID       string             `json:"id,omitempty"`
	Type     string             `json:"type"`
	Function OpenAIChatFunction `json:"function"`
}

type OpenAIChatFunction struct {
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`
}

type OpenAIChatTool struct {
	Type     string                    `json:"type"`
	Function OpenAIChatToolDeclaration `json:"function"`
}

type OpenAIChatToolDeclaration struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Parameters  map[string]any `json:"parameters,omitempty"`
}

type OpenAIChatResponse struct {
	ID      string             `json:"id,omitempty"`
	Object  string             `json:"object"`
	Model   string             `json:"model"`
	Choices []OpenAIChatChoice `json:"choices"`
	Usage   *OpenAIUsage       `json:"usage,omitempty"`
}

type OpenAIChatChoice struct {
	Index        int               `json:"index"`
	Message      OpenAIChatMessage `json:"message"`
	FinishReason string            `json:"finish_reason,omitempty"`
}

type OpenAIUsage struct {
	PromptTokens     int64 `json:"prompt_tokens,omitempty"`
	CompletionTokens int64 `json:"completion_tokens,omitempty"`
	TotalTokens      int64 `json:"total_tokens,omitempty"`
}

func OpenAIChatRequestToCanonical(in OpenAIChatRequest) (Request, error) {
	out := Request{
		Dialect:             APIDialectOpenAI,
		Model:               in.Model,
		Messages:            make([]Message, 0, len(in.Messages)),
		Tools:               openAIToolsToCanonical(in.Tools),
		Temperature:         in.Temperature,
		ReasoningEffort:     in.ReasoningEffort,
		Stream:              in.Stream,
		UnsupportedFeatures: UnsupportedFeatureReject,
	}
	if in.MaxCompletionTokens > 0 {
		out.MaxOutputTokens = in.MaxCompletionTokens
	} else {
		out.MaxOutputTokens = in.MaxTokens
	}
	for _, message := range in.Messages {
		converted, err := openAIMessageToCanonical(message)
		if err != nil {
			return Request{}, err
		}
		out.Messages = append(out.Messages, converted)
	}
	if err := out.Validate(); err != nil {
		return Request{}, err
	}
	return out, nil
}

func OpenAIChatRequestFromCanonical(in Request) (OpenAIChatRequest, error) {
	if err := in.Validate(); err != nil {
		return OpenAIChatRequest{}, err
	}
	out := OpenAIChatRequest{
		Model:               in.Model,
		Messages:            make([]OpenAIChatMessage, 0, len(in.Messages)),
		Tools:               openAIToolsFromCanonical(in.Tools),
		Temperature:         in.Temperature,
		ReasoningEffort:     in.ReasoningEffort,
		MaxCompletionTokens: in.MaxOutputTokens,
		Stream:              in.Stream,
	}
	for _, message := range in.Messages {
		converted, err := canonicalMessageToOpenAI(message)
		if err != nil {
			return OpenAIChatRequest{}, err
		}
		out.Messages = append(out.Messages, converted)
	}
	return out, nil
}

func OpenAIChatResponseFromCanonical(in Response) (OpenAIChatResponse, error) {
	if err := in.Validate(); err != nil {
		return OpenAIChatResponse{}, err
	}
	message, err := canonicalMessageToOpenAI(in.Message)
	if err != nil {
		return OpenAIChatResponse{}, err
	}
	out := OpenAIChatResponse{
		ID:     in.ID,
		Object: "chat.completion",
		Model:  in.Model,
		Choices: []OpenAIChatChoice{
			{
				Index:        0,
				Message:      message,
				FinishReason: in.StopReason,
			},
		},
	}
	if in.Usage != (Usage{}) {
		total := in.Usage.TotalTokens
		if total == 0 {
			total = in.Usage.InputTokens + in.Usage.OutputTokens
		}
		out.Usage = &OpenAIUsage{
			PromptTokens:     in.Usage.InputTokens,
			CompletionTokens: in.Usage.OutputTokens,
			TotalTokens:      total,
		}
	}
	return out, nil
}

func OpenAIChatResponseToCanonical(in OpenAIChatResponse) (Response, error) {
	if len(in.Choices) == 0 {
		return Response{}, ErrInvalidResponse
	}
	choice := in.Choices[0]
	message, err := openAIMessageToCanonical(choice.Message)
	if err != nil {
		return Response{}, err
	}
	if message.Role == "" {
		message.Role = MessageRoleAssistant
	}
	if message.Role != MessageRoleAssistant {
		return Response{}, ErrInvalidResponse
	}
	usage := Usage{}
	if in.Usage != nil {
		usage = Usage{
			InputTokens:  in.Usage.PromptTokens,
			OutputTokens: in.Usage.CompletionTokens,
			TotalTokens:  in.Usage.TotalTokens,
		}
		if usage.TotalTokens == 0 {
			usage.TotalTokens = usage.InputTokens + usage.OutputTokens
		}
	}
	out := Response{
		ID:         in.ID,
		Dialect:    APIDialectOpenAI,
		Model:      in.Model,
		Message:    message,
		StopReason: choice.FinishReason,
		Usage:      usage,
	}
	if err := out.Validate(); err != nil {
		return Response{}, err
	}
	return out, nil
}

func openAIMessageToCanonical(in OpenAIChatMessage) (Message, error) {
	out := Message{
		Role:       MessageRole(in.Role),
		Name:       in.Name,
		ToolCallID: in.ToolCallID,
		ToolCalls:  make([]ToolCall, 0, len(in.ToolCalls)),
	}
	parts, err := openAIContentToCanonical(in.Content)
	if err != nil {
		return Message{}, err
	}
	out.Content = parts
	for index, toolCall := range in.ToolCalls {
		callType := ToolCallType(toolCall.Type)
		if callType == "" {
			callType = ToolCallFunction
		}
		out.ToolCalls = append(out.ToolCalls, ToolCall{
			Index:     index,
			ID:        toolCall.ID,
			Type:      callType,
			Name:      toolCall.Function.Name,
			Arguments: toolCall.Function.Arguments,
		})
	}
	return out, nil
}

func canonicalMessageToOpenAI(in Message) (OpenAIChatMessage, error) {
	content, err := openAIContentFromCanonical(in.Content)
	if err != nil {
		return OpenAIChatMessage{}, err
	}
	out := OpenAIChatMessage{
		Role:       string(in.Role),
		Content:    content,
		Name:       in.Name,
		ToolCallID: in.ToolCallID,
		ToolCalls:  make([]OpenAIChatToolCall, 0, len(in.ToolCalls)),
	}
	for _, toolCall := range in.ToolCalls {
		out.ToolCalls = append(out.ToolCalls, OpenAIChatToolCall{
			ID:   toolCall.ID,
			Type: string(toolCall.Type),
			Function: OpenAIChatFunction{
				Name:      toolCall.Name,
				Arguments: toolCall.Arguments,
			},
		})
	}
	return out, nil
}

func openAIToolsToCanonical(in []OpenAIChatTool) []ToolDefinition {
	out := make([]ToolDefinition, 0, len(in))
	for _, tool := range in {
		if tool.Type != "" && tool.Type != "function" {
			continue
		}
		if strings.TrimSpace(tool.Function.Name) == "" {
			continue
		}
		out = append(out, ToolDefinition{
			Name:        tool.Function.Name,
			Description: tool.Function.Description,
			Parameters:  tool.Function.Parameters,
		})
	}
	return out
}

func openAIToolsFromCanonical(in []ToolDefinition) []OpenAIChatTool {
	out := make([]OpenAIChatTool, 0, len(in))
	for _, tool := range in {
		if strings.TrimSpace(tool.Name) == "" {
			continue
		}
		out = append(out, OpenAIChatTool{
			Type: "function",
			Function: OpenAIChatToolDeclaration{
				Name:        tool.Name,
				Description: tool.Description,
				Parameters:  tool.Parameters,
			},
		})
	}
	return out
}

func openAIContentToCanonical(content any) ([]ContentPart, error) {
	switch value := content.(type) {
	case nil:
		return nil, nil
	case string:
		if value == "" {
			return nil, nil
		}
		return []ContentPart{{Type: ContentPartText, Text: value}}, nil
	case []OpenAIContentPart:
		return openAIBlocksToCanonical(value)
	case []any:
		raw, err := json.Marshal(value)
		if err != nil {
			return nil, ErrInvalidRequest
		}
		var blocks []OpenAIContentPart
		if err := json.Unmarshal(raw, &blocks); err != nil {
			return nil, ErrInvalidRequest
		}
		return openAIBlocksToCanonical(blocks)
	case map[string]any:
		raw, err := json.Marshal([]any{value})
		if err != nil {
			return nil, ErrInvalidRequest
		}
		var blocks []OpenAIContentPart
		if err := json.Unmarshal(raw, &blocks); err != nil {
			return nil, ErrInvalidRequest
		}
		return openAIBlocksToCanonical(blocks)
	case json.RawMessage:
		var text string
		if err := json.Unmarshal(value, &text); err == nil {
			return openAIContentToCanonical(text)
		}
		var blocks []OpenAIContentPart
		if err := json.Unmarshal(value, &blocks); err != nil {
			return nil, ErrInvalidRequest
		}
		return openAIBlocksToCanonical(blocks)
	default:
		raw, err := json.Marshal(value)
		if err != nil {
			return nil, ErrInvalidRequest
		}
		return openAIContentToCanonical(json.RawMessage(raw))
	}
}

func openAIBlocksToCanonical(blocks []OpenAIContentPart) ([]ContentPart, error) {
	parts := make([]ContentPart, 0, len(blocks))
	for _, block := range blocks {
		switch block.Type {
		case "text", "input_text":
			if block.Text != "" {
				parts = append(parts, ContentPart{Type: ContentPartText, Text: block.Text})
			}
		case "image_url", "input_image":
			if block.ImageURL == nil || strings.TrimSpace(block.ImageURL.URL) == "" {
				return nil, ErrInvalidRequest
			}
			parts = append(parts, imageURLContentPart(block.ImageURL.URL))
		default:
			return nil, ErrInvalidRequest
		}
	}
	return parts, nil
}

func openAIContentFromCanonical(parts []ContentPart) (any, error) {
	if onlyTextContent(parts) {
		return contentText(parts)
	}
	blocks := make([]OpenAIContentPart, 0, len(parts))
	for _, part := range parts {
		switch part.Type {
		case ContentPartText:
			blocks = append(blocks, OpenAIContentPart{Type: "text", Text: part.Text})
		case ContentPartImage:
			url := part.URL
			if url == "" && part.Data != "" {
				url = dataURL(part.MIME, part.Data)
			}
			if url == "" {
				return nil, ErrInvalidRequest
			}
			blocks = append(blocks, OpenAIContentPart{Type: "image_url", ImageURL: &OpenAIImageURLPart{URL: url}})
		default:
			return nil, ErrInvalidRequest
		}
	}
	return blocks, nil
}

func onlyTextContent(parts []ContentPart) bool {
	for _, part := range parts {
		if part.Type != ContentPartText {
			return false
		}
	}
	return true
}

func contentText(parts []ContentPart) (string, error) {
	var b strings.Builder
	for _, part := range parts {
		if part.Type != ContentPartText {
			return "", ErrInvalidResponse
		}
		b.WriteString(part.Text)
	}
	return b.String(), nil
}
