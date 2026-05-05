package compat

import "strings"

type OpenAIChatRequest struct {
	Model               string              `json:"model"`
	Messages            []OpenAIChatMessage `json:"messages"`
	Temperature         *float64            `json:"temperature,omitempty"`
	MaxTokens           int                 `json:"max_tokens,omitempty"`
	MaxCompletionTokens int                 `json:"max_completion_tokens,omitempty"`
	Stream              bool                `json:"stream,omitempty"`
}

type OpenAIChatMessage struct {
	Role       string               `json:"role"`
	Content    string               `json:"content,omitempty"`
	Name       string               `json:"name,omitempty"`
	ToolCalls  []OpenAIChatToolCall `json:"tool_calls,omitempty"`
	ToolCallID string               `json:"tool_call_id,omitempty"`
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
		Temperature:         in.Temperature,
		Stream:              in.Stream,
		UnsupportedFeatures: UnsupportedFeatureReject,
	}
	if in.MaxCompletionTokens > 0 {
		out.MaxOutputTokens = in.MaxCompletionTokens
	} else {
		out.MaxOutputTokens = in.MaxTokens
	}
	for _, message := range in.Messages {
		out.Messages = append(out.Messages, openAIMessageToCanonical(message))
	}
	if err := out.Validate(); err != nil {
		return Request{}, err
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

func openAIMessageToCanonical(in OpenAIChatMessage) Message {
	out := Message{
		Role:       MessageRole(in.Role),
		Name:       in.Name,
		ToolCallID: in.ToolCallID,
		ToolCalls:  make([]ToolCall, 0, len(in.ToolCalls)),
	}
	if in.Content != "" {
		out.Content = []ContentPart{{Type: ContentPartText, Text: in.Content}}
	}
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
	return out
}

func canonicalMessageToOpenAI(in Message) (OpenAIChatMessage, error) {
	content, err := contentText(in.Content)
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
