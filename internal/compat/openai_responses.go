package compat

import (
	"encoding/json"
	"strings"
	"time"
)

type OpenAIResponsesRequest struct {
	Model           string                    `json:"model"`
	Input           json.RawMessage           `json:"input"`
	Instructions    string                    `json:"instructions,omitempty"`
	Tools           []OpenAIChatTool          `json:"tools,omitempty"`
	Temperature     *float64                  `json:"temperature,omitempty"`
	ReasoningEffort string                    `json:"reasoning_effort,omitempty"`
	Reasoning       *OpenAIResponsesReasoning `json:"reasoning,omitempty"`
	MaxOutputTokens int                       `json:"max_output_tokens,omitempty"`
	Stream          bool                      `json:"stream,omitempty"`
}

type OpenAIResponsesReasoning struct {
	Effort string `json:"effort,omitempty"`
}

type OpenAIResponsesResponse struct {
	ID         string                      `json:"id,omitempty"`
	Object     string                      `json:"object"`
	CreatedAt  int64                       `json:"created_at,omitempty"`
	Status     string                      `json:"status"`
	Model      string                      `json:"model"`
	Output     []OpenAIResponsesOutputItem `json:"output"`
	OutputText string                      `json:"output_text,omitempty"`
	Usage      *OpenAIResponsesUsage       `json:"usage,omitempty"`
	Error      any                         `json:"error,omitempty"`
}

type OpenAIResponsesOutputItem struct {
	ID        string                         `json:"id,omitempty"`
	Type      string                         `json:"type"`
	Status    string                         `json:"status,omitempty"`
	Role      string                         `json:"role,omitempty"`
	Content   []OpenAIResponsesOutputContent `json:"content,omitempty"`
	CallID    string                         `json:"call_id,omitempty"`
	Name      string                         `json:"name,omitempty"`
	Arguments string                         `json:"arguments,omitempty"`
}

type OpenAIResponsesOutputContent struct {
	Type        string `json:"type"`
	Text        string `json:"text,omitempty"`
	Annotations []any  `json:"annotations,omitempty"`
}

type OpenAIResponsesUsage struct {
	InputTokens  int64 `json:"input_tokens,omitempty"`
	OutputTokens int64 `json:"output_tokens,omitempty"`
	TotalTokens  int64 `json:"total_tokens,omitempty"`
}

type openAIResponsesInputItem struct {
	Type     string          `json:"type,omitempty"`
	Role     string          `json:"role,omitempty"`
	Content  json.RawMessage `json:"content,omitempty"`
	Text     string          `json:"text,omitempty"`
	ImageURL any             `json:"image_url,omitempty"`
}

type openAIResponsesContentPart struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	ImageURL any    `json:"image_url,omitempty"`
}

func OpenAIResponsesRequestToCanonical(in OpenAIResponsesRequest) (Request, error) {
	reasoningEffort := in.ReasoningEffort
	if reasoningEffort == "" && in.Reasoning != nil {
		reasoningEffort = in.Reasoning.Effort
	}
	out := Request{
		Dialect:             APIDialectOpenAI,
		Model:               in.Model,
		Tools:               openAIToolsToCanonical(in.Tools),
		Temperature:         in.Temperature,
		ReasoningEffort:     reasoningEffort,
		MaxOutputTokens:     in.MaxOutputTokens,
		Stream:              in.Stream,
		UnsupportedFeatures: UnsupportedFeatureReject,
	}
	if strings.TrimSpace(in.Instructions) != "" {
		out.Messages = append(out.Messages, Message{
			Role:    MessageRoleSystem,
			Content: []ContentPart{{Type: ContentPartText, Text: in.Instructions}},
		})
	}
	messages, err := openAIResponsesInputToCanonical(in.Input)
	if err != nil {
		return Request{}, err
	}
	out.Messages = append(out.Messages, messages...)
	if err := out.Validate(); err != nil {
		return Request{}, err
	}
	return out, nil
}

func OpenAIResponsesResponseFromCanonical(in Response) (OpenAIResponsesResponse, error) {
	if err := in.Validate(); err != nil {
		return OpenAIResponsesResponse{}, err
	}
	output := make([]OpenAIResponsesOutputItem, 0, 1+len(in.Message.ToolCalls))
	outputText, err := contentText(in.Message.Content)
	if err != nil && len(in.Message.Content) > 0 {
		return OpenAIResponsesResponse{}, err
	}
	if outputText != "" || len(in.Message.Content) > 0 {
		output = append(output, OpenAIResponsesOutputItem{
			ID:     "msg_" + in.ID,
			Type:   "message",
			Status: "completed",
			Role:   string(MessageRoleAssistant),
			Content: []OpenAIResponsesOutputContent{{
				Type: "output_text",
				Text: outputText,
			}},
		})
	}
	for _, toolCall := range in.Message.ToolCalls {
		output = append(output, OpenAIResponsesOutputItem{
			ID:        toolCall.ID,
			Type:      "function_call",
			Status:    "completed",
			CallID:    toolCall.ID,
			Name:      toolCall.Name,
			Arguments: toolCall.Arguments,
		})
	}
	out := OpenAIResponsesResponse{
		ID:         in.ID,
		Object:     "response",
		CreatedAt:  time.Now().Unix(),
		Status:     "completed",
		Model:      in.Model,
		Output:     output,
		OutputText: outputText,
	}
	if in.Usage != (Usage{}) {
		total := in.Usage.TotalTokens
		if total == 0 {
			total = in.Usage.InputTokens + in.Usage.OutputTokens
		}
		out.Usage = &OpenAIResponsesUsage{
			InputTokens:  in.Usage.InputTokens,
			OutputTokens: in.Usage.OutputTokens,
			TotalTokens:  total,
		}
	}
	return out, nil
}

func openAIResponsesInputToCanonical(input json.RawMessage) ([]Message, error) {
	trimmed := strings.TrimSpace(string(input))
	if trimmed == "" || trimmed == "null" {
		return nil, ErrInvalidRequest
	}
	var text string
	if err := json.Unmarshal(input, &text); err == nil {
		if strings.TrimSpace(text) == "" {
			return nil, ErrInvalidRequest
		}
		return []Message{{
			Role:    MessageRoleUser,
			Content: []ContentPart{{Type: ContentPartText, Text: text}},
		}}, nil
	}
	var items []openAIResponsesInputItem
	if err := json.Unmarshal(input, &items); err == nil {
		return openAIResponsesInputItemsToCanonical(items)
	}
	var item openAIResponsesInputItem
	if err := json.Unmarshal(input, &item); err == nil {
		return openAIResponsesInputItemsToCanonical([]openAIResponsesInputItem{item})
	}
	return nil, ErrInvalidRequest
}

func openAIResponsesInputItemsToCanonical(items []openAIResponsesInputItem) ([]Message, error) {
	if len(items) == 0 {
		return nil, ErrInvalidRequest
	}
	messages := make([]Message, 0, len(items))
	pendingUserParts := make([]ContentPart, 0)
	flushPending := func() {
		if len(pendingUserParts) == 0 {
			return
		}
		messages = append(messages, Message{
			Role:    MessageRoleUser,
			Content: append([]ContentPart(nil), pendingUserParts...),
		})
		pendingUserParts = pendingUserParts[:0]
	}
	for _, item := range items {
		if openAIResponsesItemIsMessage(item) {
			flushPending()
			message, err := openAIResponsesMessageItemToCanonical(item)
			if err != nil {
				return nil, err
			}
			messages = append(messages, message)
			continue
		}
		parts, err := openAIResponsesTopLevelContentItemToCanonical(item)
		if err != nil {
			return nil, err
		}
		pendingUserParts = append(pendingUserParts, parts...)
	}
	flushPending()
	if len(messages) == 0 {
		return nil, ErrInvalidRequest
	}
	return messages, nil
}

func openAIResponsesItemIsMessage(item openAIResponsesInputItem) bool {
	return item.Role != "" || len(item.Content) > 0 || strings.EqualFold(strings.TrimSpace(item.Type), "message")
}

func openAIResponsesMessageItemToCanonical(item openAIResponsesInputItem) (Message, error) {
	role := item.Role
	if role == "" {
		role = string(MessageRoleUser)
	}
	parts, err := openAIResponsesItemContentToCanonical(item)
	if err != nil {
		return Message{}, err
	}
	return Message{
		Role:    MessageRole(role),
		Content: parts,
	}, nil
}

func openAIResponsesItemContentToCanonical(item openAIResponsesInputItem) ([]ContentPart, error) {
	if len(item.Content) > 0 && strings.TrimSpace(string(item.Content)) != "null" {
		return openAIResponsesContentRawToCanonical(item.Content)
	}
	return openAIResponsesTopLevelContentItemToCanonical(item)
}

func openAIResponsesTopLevelContentItemToCanonical(item openAIResponsesInputItem) ([]ContentPart, error) {
	raw, err := json.Marshal([]openAIResponsesContentPart{{
		Type:     item.Type,
		Text:     item.Text,
		ImageURL: item.ImageURL,
	}})
	if err != nil {
		return nil, ErrInvalidRequest
	}
	return openAIResponsesContentRawToCanonical(raw)
}

func openAIResponsesContentRawToCanonical(raw json.RawMessage) ([]ContentPart, error) {
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		if text == "" {
			return nil, nil
		}
		return []ContentPart{{Type: ContentPartText, Text: text}}, nil
	}
	var blocks []openAIResponsesContentPart
	if err := json.Unmarshal(raw, &blocks); err == nil {
		return openAIResponsesContentBlocksToCanonical(blocks)
	}
	var block openAIResponsesContentPart
	if err := json.Unmarshal(raw, &block); err == nil {
		return openAIResponsesContentBlocksToCanonical([]openAIResponsesContentPart{block})
	}
	return nil, ErrInvalidRequest
}

func openAIResponsesContentBlocksToCanonical(blocks []openAIResponsesContentPart) ([]ContentPart, error) {
	parts := make([]ContentPart, 0, len(blocks))
	for _, block := range blocks {
		switch strings.TrimSpace(block.Type) {
		case "text", "input_text", "output_text":
			if block.Text != "" {
				parts = append(parts, ContentPart{Type: ContentPartText, Text: block.Text})
			}
		case "image_url", "input_image":
			imageURL, ok := openAIResponsesImageURL(block.ImageURL)
			if !ok {
				return nil, ErrInvalidRequest
			}
			parts = append(parts, imageURLContentPart(imageURL))
		default:
			return nil, ErrInvalidRequest
		}
	}
	return parts, nil
}

func openAIResponsesImageURL(value any) (string, bool) {
	switch typed := value.(type) {
	case string:
		url := strings.TrimSpace(typed)
		return url, url != ""
	case map[string]any:
		rawURL, ok := typed["url"].(string)
		url := strings.TrimSpace(rawURL)
		return url, ok && url != ""
	default:
		return "", false
	}
}
