package compat

import (
	"encoding/json"
	"strings"
)

type GeminiGenerateContentRequest struct {
	Contents          []GeminiContent          `json:"contents"`
	SystemInstruction *GeminiContent           `json:"systemInstruction,omitempty"`
	GenerationConfig  *GeminiGenerationConfig  `json:"generationConfig,omitempty"`
	Tools             []GeminiToolDeclaration  `json:"tools,omitempty"`
	ToolConfig        *GeminiToolConfiguration `json:"toolConfig,omitempty"`
}

type GeminiContent struct {
	Role  string       `json:"role,omitempty"`
	Parts []GeminiPart `json:"parts"`
}

type GeminiPart struct {
	Text             string                  `json:"text,omitempty"`
	InlineData       *GeminiInlineData       `json:"inlineData,omitempty"`
	FunctionCall     *GeminiFunctionCall     `json:"functionCall,omitempty"`
	FunctionResponse *GeminiFunctionResponse `json:"functionResponse,omitempty"`
}

type GeminiInlineData struct {
	MIMEType string `json:"mimeType,omitempty"`
	Data     string `json:"data,omitempty"`
}

type GeminiFunctionCall struct {
	Name string         `json:"name"`
	Args map[string]any `json:"args,omitempty"`
	ID   string         `json:"id,omitempty"`
}

type GeminiFunctionResponse struct {
	Name     string         `json:"name,omitempty"`
	ID       string         `json:"id,omitempty"`
	Response map[string]any `json:"response,omitempty"`
}

type GeminiGenerationConfig struct {
	Temperature     *float64 `json:"temperature,omitempty"`
	MaxOutputTokens int      `json:"maxOutputTokens,omitempty"`
	StopSequences   []string `json:"stopSequences,omitempty"`
}

type GeminiToolDeclaration struct {
	FunctionDeclarations []GeminiFunctionDeclaration `json:"functionDeclarations,omitempty"`
}

type GeminiFunctionDeclaration struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Parameters  map[string]any `json:"parameters,omitempty"`
}

type GeminiToolConfiguration struct {
	FunctionCallingConfig *GeminiFunctionCallingConfig `json:"functionCallingConfig,omitempty"`
}

type GeminiFunctionCallingConfig struct {
	Mode                 string   `json:"mode,omitempty"`
	AllowedFunctionNames []string `json:"allowedFunctionNames,omitempty"`
}

type GeminiGenerateContentResponse struct {
	Candidates    []GeminiCandidate `json:"candidates"`
	UsageMetadata *GeminiUsage      `json:"usageMetadata,omitempty"`
	ModelVersion  string            `json:"modelVersion,omitempty"`
}

type GeminiCandidate struct {
	Content      GeminiContent `json:"content"`
	FinishReason string        `json:"finishReason,omitempty"`
	Index        int           `json:"index,omitempty"`
}

type GeminiUsage struct {
	PromptTokenCount     int64 `json:"promptTokenCount,omitempty"`
	CandidatesTokenCount int64 `json:"candidatesTokenCount,omitempty"`
	TotalTokenCount      int64 `json:"totalTokenCount,omitempty"`
}

func GeminiGenerateContentRequestToCanonical(in GeminiGenerateContentRequest, model string) (Request, error) {
	out := Request{
		Dialect:             APIDialectGemini,
		Model:               model,
		UnsupportedFeatures: UnsupportedFeatureReject,
	}
	if in.SystemInstruction != nil {
		system, err := textFromGeminiParts(in.SystemInstruction.Parts)
		if err != nil {
			return Request{}, err
		}
		if system != "" {
			out.Messages = append(out.Messages, Message{
				Role:    MessageRoleSystem,
				Content: []ContentPart{{Type: ContentPartText, Text: system}},
			})
		}
	}
	if in.GenerationConfig != nil {
		out.Temperature = in.GenerationConfig.Temperature
		out.MaxOutputTokens = in.GenerationConfig.MaxOutputTokens
	}
	for _, content := range in.Contents {
		converted, err := geminiContentToCanonical(content)
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

func GeminiGenerateContentRequestFromCanonical(in Request) (GeminiGenerateContentRequest, error) {
	if err := in.Validate(); err != nil {
		return GeminiGenerateContentRequest{}, err
	}
	out := GeminiGenerateContentRequest{
		Contents: make([]GeminiContent, 0, len(in.Messages)),
	}
	if in.Temperature != nil || in.MaxOutputTokens > 0 {
		out.GenerationConfig = &GeminiGenerationConfig{
			Temperature:     in.Temperature,
			MaxOutputTokens: in.MaxOutputTokens,
		}
	}
	var systemParts []GeminiPart
	for _, message := range in.Messages {
		if message.Role == MessageRoleSystem || message.Role == MessageRoleDeveloper {
			for _, part := range message.Content {
				if part.Type != ContentPartText {
					return GeminiGenerateContentRequest{}, ErrInvalidRequest
				}
				systemParts = append(systemParts, GeminiPart{Text: part.Text})
			}
			continue
		}
		converted, err := canonicalMessageToGemini(message)
		if err != nil {
			return GeminiGenerateContentRequest{}, err
		}
		out.Contents = append(out.Contents, converted)
	}
	if len(systemParts) > 0 {
		out.SystemInstruction = &GeminiContent{Parts: systemParts}
	}
	if len(out.Contents) == 0 {
		return GeminiGenerateContentRequest{}, ErrInvalidRequest
	}
	return out, nil
}

func GeminiGenerateContentResponseFromCanonical(in Response) (GeminiGenerateContentResponse, error) {
	if err := in.Validate(); err != nil {
		return GeminiGenerateContentResponse{}, err
	}
	parts := make([]GeminiPart, 0, len(in.Message.Content)+len(in.Message.ToolCalls))
	for _, content := range in.Message.Content {
		if content.Type != ContentPartText {
			return GeminiGenerateContentResponse{}, ErrInvalidResponse
		}
		parts = append(parts, GeminiPart{Text: content.Text})
	}
	for _, toolCall := range in.Message.ToolCalls {
		var args map[string]any
		if strings.TrimSpace(toolCall.Arguments) != "" {
			if err := json.Unmarshal([]byte(toolCall.Arguments), &args); err != nil {
				return GeminiGenerateContentResponse{}, ErrInvalidResponse
			}
		}
		parts = append(parts, GeminiPart{FunctionCall: &GeminiFunctionCall{
			Name: toolCall.Name,
			Args: args,
			ID:   toolCall.ID,
		}})
	}
	if len(parts) == 0 {
		parts = append(parts, GeminiPart{Text: ""})
	}
	total := in.Usage.TotalTokens
	if total == 0 {
		total = in.Usage.InputTokens + in.Usage.OutputTokens
	}
	out := GeminiGenerateContentResponse{
		ModelVersion: in.Model,
		Candidates: []GeminiCandidate{
			{
				Index:        0,
				FinishReason: canonicalStopToGemini(in.StopReason),
				Content:      GeminiContent{Role: "model", Parts: parts},
			},
		},
	}
	if in.Usage != (Usage{}) {
		out.UsageMetadata = &GeminiUsage{
			PromptTokenCount:     in.Usage.InputTokens,
			CandidatesTokenCount: in.Usage.OutputTokens,
			TotalTokenCount:      total,
		}
	}
	return out, nil
}

func GeminiGenerateContentResponseToCanonical(in GeminiGenerateContentResponse) (Response, error) {
	if len(in.Candidates) == 0 {
		return Response{}, ErrInvalidResponse
	}
	candidate := in.Candidates[0]
	messages, err := geminiContentToCanonical(candidate.Content)
	if err != nil || len(messages) == 0 {
		return Response{}, ErrInvalidResponse
	}
	message := messages[0]
	message.Role = MessageRoleAssistant
	usage := Usage{}
	if in.UsageMetadata != nil {
		usage = Usage{
			InputTokens:  in.UsageMetadata.PromptTokenCount,
			OutputTokens: in.UsageMetadata.CandidatesTokenCount,
			TotalTokens:  in.UsageMetadata.TotalTokenCount,
		}
		if usage.TotalTokens == 0 {
			usage.TotalTokens = usage.InputTokens + usage.OutputTokens
		}
	}
	out := Response{
		Dialect:    APIDialectGemini,
		Model:      in.ModelVersion,
		Message:    message,
		StopReason: geminiStopToCanonical(candidate.FinishReason),
		Usage:      usage,
	}
	if err := out.Validate(); err != nil {
		return Response{}, err
	}
	return out, nil
}

func geminiContentToCanonical(in GeminiContent) ([]Message, error) {
	role := geminiRoleToCanonical(in.Role)
	message := Message{Role: role}
	out := make([]Message, 0, 1)
	for _, part := range in.Parts {
		switch {
		case part.InlineData != nil:
			return nil, ErrInvalidRequest
		case part.Text != "":
			message.Content = append(message.Content, ContentPart{Type: ContentPartText, Text: part.Text})
		case part.FunctionCall != nil:
			args, err := json.Marshal(part.FunctionCall.Args)
			if err != nil {
				return nil, ErrInvalidRequest
			}
			message.ToolCalls = append(message.ToolCalls, ToolCall{
				Index:     len(message.ToolCalls),
				ID:        part.FunctionCall.ID,
				Type:      ToolCallFunction,
				Name:      part.FunctionCall.Name,
				Arguments: string(args),
			})
		case part.FunctionResponse != nil:
			response, err := json.Marshal(part.FunctionResponse.Response)
			if err != nil {
				return nil, ErrInvalidRequest
			}
			out = append(out, Message{
				Role:       MessageRoleTool,
				ToolCallID: part.FunctionResponse.ID,
				Content:    []ContentPart{{Type: ContentPartText, Text: string(response)}},
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

func canonicalMessageToGemini(in Message) (GeminiContent, error) {
	if err := in.Validate(); err != nil {
		return GeminiContent{}, err
	}
	out := GeminiContent{Role: canonicalRoleToGemini(in.Role)}
	for _, part := range in.Content {
		if part.Type != ContentPartText {
			return GeminiContent{}, ErrInvalidRequest
		}
		out.Parts = append(out.Parts, GeminiPart{Text: part.Text})
	}
	for _, toolCall := range in.ToolCalls {
		var args map[string]any
		if strings.TrimSpace(toolCall.Arguments) != "" {
			if err := json.Unmarshal([]byte(toolCall.Arguments), &args); err != nil {
				return GeminiContent{}, ErrInvalidRequest
			}
		}
		out.Parts = append(out.Parts, GeminiPart{FunctionCall: &GeminiFunctionCall{
			Name: toolCall.Name,
			Args: args,
			ID:   toolCall.ID,
		}})
	}
	if in.Role == MessageRoleTool {
		text, err := contentText(in.Content)
		if err != nil {
			return GeminiContent{}, err
		}
		var response map[string]any
		if strings.TrimSpace(text) != "" {
			if err := json.Unmarshal([]byte(text), &response); err != nil {
				response = map[string]any{"content": text}
			}
		}
		out.Parts = []GeminiPart{{FunctionResponse: &GeminiFunctionResponse{
			ID:       in.ToolCallID,
			Response: response,
		}}}
	}
	return out, nil
}

func textFromGeminiParts(parts []GeminiPart) (string, error) {
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if part.InlineData != nil || part.FunctionCall != nil || part.FunctionResponse != nil {
			return "", ErrInvalidRequest
		}
		if part.Text != "" {
			out = append(out, part.Text)
		}
	}
	return strings.Join(out, "\n"), nil
}

func geminiRoleToCanonical(role string) MessageRole {
	switch strings.ToLower(role) {
	case "model":
		return MessageRoleAssistant
	case "user":
		return MessageRoleUser
	default:
		return MessageRoleUser
	}
}

func canonicalRoleToGemini(role MessageRole) string {
	switch role {
	case MessageRoleAssistant:
		return "model"
	default:
		return "user"
	}
}

func canonicalStopToGemini(stop string) string {
	switch stop {
	case "max_tokens", "length":
		return "MAX_TOKENS"
	case "safety":
		return "SAFETY"
	default:
		return "STOP"
	}
}

func geminiStopToCanonical(stop string) string {
	switch strings.ToUpper(stop) {
	case "MAX_TOKENS":
		return "max_tokens"
	case "SAFETY":
		return "safety"
	default:
		return "stop"
	}
}
