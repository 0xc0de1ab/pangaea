package geminidirect

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/0xc0de1ab/pangaea/internal/compat"
	"github.com/0xc0de1ab/pangaea/internal/provider"
)

type oauthCredentials struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiryDate   int64  `json:"expiry_date"`
}

type loadCodeAssistRequest struct {
	CloudaiCompanionProject string                 `json:"cloudaicompanionProject,omitempty"`
	Metadata                loadCodeAssistMetadata `json:"metadata"`
	Mode                    string                 `json:"mode,omitempty"`
}

type loadCodeAssistMetadata struct {
	IDEType    string `json:"ideType"`
	Platform   string `json:"platform"`
	PluginType string `json:"pluginType"`
}

type loadCodeAssistResponse struct {
	CloudaiCompanionProject string `json:"cloudaicompanionProject"`
}

type retrieveUserQuotaRequest struct {
	Project string `json:"project,omitempty"`
}

type retrieveUserQuotaResponse struct {
	Buckets []quotaBucket `json:"buckets"`
}

type quotaBucket struct {
	ModelID           string  `json:"modelId"`
	RemainingFraction float64 `json:"remainingFraction"`
	RemainingAmount   string  `json:"remainingAmount,omitempty"`
	ResetTime         string  `json:"resetTime,omitempty"`
}

type codeAssistGenerateRequest struct {
	Model        string         `json:"model"`
	Project      string         `json:"project"`
	UserPromptID string         `json:"user_prompt_id"`
	Request      map[string]any `json:"request"`
}

type codeAssistGenerateResponse struct {
	Response codeAssistModelResponse `json:"response"`
	TraceID  string                  `json:"traceId,omitempty"`
	Metadata map[string]any          `json:"metadata,omitempty"`
}

type codeAssistModelResponse struct {
	Candidates    []codeAssistCandidate `json:"candidates"`
	UsageMetadata *compat.GeminiUsage   `json:"usageMetadata,omitempty"`
	ModelVersion  string                `json:"modelVersion,omitempty"`
	ResponseID    string                `json:"responseId,omitempty"`
}

type codeAssistCandidate struct {
	Content      codeAssistContent `json:"content"`
	FinishReason string            `json:"finishReason,omitempty"`
	Index        int               `json:"index,omitempty"`
}

type codeAssistContent struct {
	Role  string           `json:"role,omitempty"`
	Parts []codeAssistPart `json:"parts"`
}

type codeAssistPart struct {
	Text             string                         `json:"text,omitempty"`
	Thought          bool                           `json:"thought,omitempty"`
	ThoughtSignature string                         `json:"thoughtSignature,omitempty"`
	InlineData       *compat.GeminiInlineData       `json:"inlineData,omitempty"`
	FunctionCall     *compat.GeminiFunctionCall     `json:"functionCall,omitempty"`
	FunctionResponse *compat.GeminiFunctionResponse `json:"functionResponse,omitempty"`
	ExecutableCode   map[string]any                 `json:"executableCode,omitempty"`
	CodeExecution    map[string]any                 `json:"codeExecutionResult,omitempty"`
}

func codeAssistResponseToCanonical(in codeAssistModelResponse, dialect compat.APIDialect, fallbackModel string) (compat.Response, error) {
	model := strings.TrimSpace(in.ModelVersion)
	if model == "" {
		model = fallbackModel
	}
	if len(in.Candidates) == 0 {
		return compat.Response{}, compat.ErrInvalidResponse
	}
	candidate := in.Candidates[0]
	message, err := codeAssistContentToCanonical(candidate.Content)
	if err != nil {
		return compat.Response{}, err
	}
	usage := compat.Usage{}
	if in.UsageMetadata != nil {
		usage = compat.Usage{
			InputTokens:  in.UsageMetadata.PromptTokenCount,
			OutputTokens: in.UsageMetadata.CandidatesTokenCount,
			TotalTokens:  in.UsageMetadata.TotalTokenCount,
		}
		if usage.TotalTokens == 0 {
			usage.TotalTokens = usage.InputTokens + usage.OutputTokens
		}
	}
	out := compat.Response{
		ID:         in.ResponseID,
		Dialect:    dialect,
		Model:      model,
		Message:    message,
		StopReason: geminiStopToCanonical(candidate.FinishReason),
		Usage:      usage,
	}
	if err := out.Validate(); err != nil {
		return compat.Response{}, err
	}
	return out, nil
}

func codeAssistContentToCanonical(in codeAssistContent) (compat.Message, error) {
	message := compat.Message{Role: compat.MessageRoleAssistant}
	for _, part := range in.Parts {
		if part.Thought {
			continue
		}
		switch {
		case part.Text != "":
			message.Content = append(message.Content, compat.ContentPart{Type: compat.ContentPartText, Text: part.Text})
		case part.FunctionCall != nil:
			args, err := json.Marshal(part.FunctionCall.Args)
			if err != nil {
				return compat.Message{}, compat.ErrInvalidResponse
			}
			message.ToolCalls = append(message.ToolCalls, compat.ToolCall{
				Index:     len(message.ToolCalls),
				ID:        part.FunctionCall.ID,
				Type:      compat.ToolCallFunction,
				Name:      part.FunctionCall.Name,
				Arguments: string(args),
			})
		case part.InlineData != nil:
			message.Content = append(message.Content, compat.ContentPart{
				Type: compat.ContentPartImage,
				MIME: part.InlineData.MIMEType,
				Data: part.InlineData.Data,
			})
		}
	}
	if err := message.Validate(); err != nil {
		return compat.Message{}, compat.ErrInvalidResponse
	}
	return message, nil
}

func applyCodeAssistStreamPayload(response *compat.Response, started *bool, payload string, emit func(compat.Event) error) (bool, error) {
	payload = strings.TrimSpace(payload)
	if payload == "" || payload == "[DONE]" {
		return payload == "[DONE]", nil
	}
	if message, code := upstreamErrorDetails([]byte(payload)); message != "" {
		errEvent := compat.Event{
			ResponseID: response.ID,
			Dialect:    response.Dialect,
			Model:      response.Model,
			Type:       compat.EventError,
			Error:      &compat.EventErrorPayload{Message: message, Code: code},
		}
		if err := emit(errEvent); err != nil {
			return false, err
		}
		return false, &provider.UpstreamError{Code: code, Message: message}
	}
	var envelope codeAssistGenerateResponse
	if err := json.Unmarshal([]byte(payload), &envelope); err != nil {
		return false, err
	}
	chunk := envelope.Response
	if chunk.ResponseID != "" {
		response.ID = chunk.ResponseID
	}
	if chunk.ModelVersion != "" {
		response.Model = chunk.ModelVersion
	}
	if !*started {
		if err := emit(compat.Event{ResponseID: response.ID, Dialect: response.Dialect, Model: response.Model, Type: compat.EventMessageStart, Message: &compat.Message{Role: compat.MessageRoleAssistant}}); err != nil {
			return false, err
		}
		*started = true
	}
	for _, candidate := range chunk.Candidates {
		for _, part := range candidate.Content.Parts {
			if part.Thought {
				continue
			}
			switch {
			case part.Text != "":
				content := compat.ContentPart{Type: compat.ContentPartText, Text: part.Text}
				if err := compat.ApplyEventToResponse(response, compat.Event{Type: compat.EventContentDelta, ContentDelta: &content}); err != nil {
					return false, err
				}
				if err := emit(compat.Event{ResponseID: response.ID, Dialect: response.Dialect, Model: response.Model, Type: compat.EventContentDelta, ContentDelta: &content}); err != nil {
					return false, err
				}
			case part.FunctionCall != nil:
				args, err := json.Marshal(part.FunctionCall.Args)
				if err != nil {
					return false, compat.ErrInvalidResponse
				}
				call := compat.ToolCall{
					Index:     len(response.Message.ToolCalls),
					ID:        part.FunctionCall.ID,
					Type:      compat.ToolCallFunction,
					Name:      part.FunctionCall.Name,
					Arguments: string(args),
				}
				if err := compat.ApplyEventToResponse(response, compat.Event{Type: compat.EventToolCallDelta, ToolCallDelta: &call}); err != nil {
					return false, err
				}
				if err := emit(compat.Event{ResponseID: response.ID, Dialect: response.Dialect, Model: response.Model, Type: compat.EventToolCallDelta, ToolCallDelta: &call}); err != nil {
					return false, err
				}
			}
		}
		if candidate.FinishReason != "" {
			response.StopReason = geminiStopToCanonical(candidate.FinishReason)
		}
	}
	if chunk.UsageMetadata != nil {
		usage := compat.Usage{
			InputTokens:  chunk.UsageMetadata.PromptTokenCount,
			OutputTokens: chunk.UsageMetadata.CandidatesTokenCount,
			TotalTokens:  chunk.UsageMetadata.TotalTokenCount,
		}
		if usage.TotalTokens == 0 {
			usage.TotalTokens = usage.InputTokens + usage.OutputTokens
		}
		delta := usageDifference(usage, response.Usage)
		response.Usage = usage
		if delta != (compat.Usage{}) {
			if err := emit(compat.Event{ResponseID: response.ID, Dialect: response.Dialect, Model: response.Model, Type: compat.EventUsageDelta, UsageDelta: &delta}); err != nil {
				return false, err
			}
		}
	}
	if response.StopReason != "" {
		if err := emit(compat.Event{ResponseID: response.ID, Dialect: response.Dialect, Model: response.Model, Type: compat.EventDone, DoneReason: response.StopReason}); err != nil {
			return false, err
		}
	}
	return false, nil
}

func enrichModelQuota(models []provider.Model, quota retrieveUserQuotaResponse) {
	if len(models) == 0 || len(quota.Buckets) == 0 {
		return
	}
	byModel := map[string]quotaBucket{}
	for _, bucket := range quota.Buckets {
		modelID := strings.TrimSpace(bucket.ModelID)
		if modelID == "" {
			continue
		}
		byModel[modelID] = bucket
	}
	for i := range models {
		candidates := []string{models[i].ID}
		candidates = append(candidates, models[i].GroupMembers...)
		var best *provider.ModelQuota
		for _, id := range candidates {
			bucket, ok := byModel[id]
			if !ok {
				continue
			}
			current := &provider.ModelQuota{
				RemainingPct: bucket.RemainingFraction * 100,
				Source:       "gemini-codeassist-quota",
				ResetAt:      parseResetTime(bucket.ResetTime),
			}
			if best == nil || current.RemainingPct < best.RemainingPct {
				best = current
			}
		}
		if best != nil {
			models[i].Quota = best
		}
	}
}

func parseResetTime(raw string) time.Time {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}
	}
	if ts, err := time.Parse(time.RFC3339Nano, raw); err == nil {
		return ts
	}
	if ts, err := time.Parse(time.RFC3339, raw); err == nil {
		return ts
	}
	return time.Time{}
}

func defaultModels(capabilities []provider.Capability) []provider.Model {
	caps := dedupeCapabilities(capabilities)
	if len(caps) == 0 {
		caps = []provider.Capability{
			provider.CapabilityOpenAIChat,
			provider.CapabilityAnthropicMessages,
			provider.CapabilityGeminiGenerateContent,
			provider.CapabilityStreamSSE,
		}
	}
	return []provider.Model{
		{ID: "auto-gemini-3", Aliases: []string{"Auto (Gemini 3)"}, Kind: "group", GroupMembers: []string{"gemini-3.1-pro-preview", "gemini-3-flash-preview"}, Capabilities: caps, ContextTokens: 1_048_576, MaxContextTokens: 1_048_576},
		{ID: "auto-gemini-2.5", Aliases: []string{"Auto (Gemini 2.5)"}, Kind: "group", GroupMembers: []string{"gemini-2.5-pro", "gemini-2.5-flash"}, Capabilities: caps, ContextTokens: 1_048_576, MaxContextTokens: 1_048_576},
		{ID: "gemini-3.1-pro-preview", Aliases: []string{"Gemini 3.1 Pro Preview"}, Capabilities: caps, ContextTokens: 1_048_576, MaxContextTokens: 1_048_576},
		{ID: "gemini-3-flash-preview", Aliases: []string{"Gemini 3 Flash Preview"}, Capabilities: caps, ContextTokens: 1_048_576, MaxContextTokens: 1_048_576},
		{ID: "gemini-3.1-flash-lite-preview", Aliases: []string{"Gemini 3.1 Flash Lite Preview"}, Capabilities: caps, ContextTokens: 1_048_576, MaxContextTokens: 1_048_576},
		{ID: "gemini-2.5-pro", Aliases: []string{"Gemini 2.5 Pro"}, Capabilities: caps, ContextTokens: 1_048_576, MaxContextTokens: 1_048_576},
		{ID: "gemini-2.5-flash", Aliases: []string{"Gemini 2.5 Flash"}, Capabilities: caps, ContextTokens: 1_048_576, MaxContextTokens: 1_048_576},
		{ID: "gemini-2.5-flash-lite", Aliases: []string{"Gemini 2.5 Flash Lite"}, Capabilities: caps, ContextTokens: 1_048_576, MaxContextTokens: 1_048_576},
	}
}

func dedupeCapabilities(in []provider.Capability) []provider.Capability {
	out := make([]provider.Capability, 0, len(in))
	seen := map[provider.Capability]struct{}{}
	for _, capability := range in {
		if capability == "" {
			continue
		}
		if _, ok := seen[capability]; ok {
			continue
		}
		seen[capability] = struct{}{}
		out = append(out, capability)
	}
	return out
}

func geminiStopToCanonical(stop string) string {
	switch strings.ToUpper(strings.TrimSpace(stop)) {
	case "MAX_TOKENS":
		return "max_tokens"
	case "SAFETY":
		return "safety"
	default:
		return "stop"
	}
}

func usageDifference(current compat.Usage, previous compat.Usage) compat.Usage {
	return compat.Usage{
		InputTokens:  nonNegativeDifference(current.InputTokens, previous.InputTokens),
		OutputTokens: nonNegativeDifference(current.OutputTokens, previous.OutputTokens),
		TotalTokens:  nonNegativeDifference(current.TotalTokens, previous.TotalTokens),
	}
}

func nonNegativeDifference(current int64, previous int64) int64 {
	if current <= previous {
		return 0
	}
	return current - previous
}
