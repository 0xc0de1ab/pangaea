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
	byModel := quotaBucketsByModel(quota)
	for i := range models {
		candidates := []string{models[i].ID}
		candidates = append(candidates, models[i].Aliases...)
		candidates = append(candidates, models[i].GroupMembers...)
		var best *provider.ModelQuota
		for _, id := range candidates {
			bucket, ok := byModel[normalizeModelID(id)]
			if !ok {
				continue
			}
			current := quotaToModelQuota(bucket)
			if best == nil || current.RemainingPct < best.RemainingPct {
				best = current
			}
		}
		if best != nil {
			models[i].Quota = best
		}
	}
}

func modelsFromQuota(quota retrieveUserQuotaResponse, capabilities []provider.Capability) []provider.Model {
	if len(quota.Buckets) == 0 {
		return nil
	}
	caps := geminiModelCapabilities(capabilities)
	models := make([]provider.Model, 0, len(quota.Buckets)+2)
	seen := map[string]struct{}{}
	for _, bucket := range quota.Buckets {
		rawID := normalizeModelID(bucket.ModelID)
		if rawID == "" {
			continue
		}
		modelID := canonicalGeminiModelID(rawID)
		if _, ok := seen[modelID]; ok {
			continue
		}
		seen[modelID] = struct{}{}
		models = append(models, provider.Model{
			ID:               modelID,
			Aliases:          geminiModelAliases(rawID, modelID),
			Capabilities:     caps,
			ContextTokens:    geminiContextTokens(modelID),
			MaxContextTokens: geminiContextTokens(modelID),
			MaxOutputTokens:  geminiMaxOutputTokens(modelID),
		})
	}
	models = prependGeminiAutoGroups(models, caps)
	enrichModelQuota(models, quota)
	return models
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

func quotaBucketsByModel(quota retrieveUserQuotaResponse) map[string]quotaBucket {
	byModel := map[string]quotaBucket{}
	for _, bucket := range quota.Buckets {
		rawID := normalizeModelID(bucket.ModelID)
		if rawID == "" {
			continue
		}
		putQuotaBucket(byModel, rawID, bucket)
		putQuotaBucket(byModel, canonicalGeminiModelID(rawID), bucket)
	}
	return byModel
}

func putQuotaBucket(byModel map[string]quotaBucket, modelID string, bucket quotaBucket) {
	modelID = normalizeModelID(modelID)
	if modelID == "" {
		return
	}
	if existing, ok := byModel[modelID]; ok && existing.RemainingFraction <= bucket.RemainingFraction {
		return
	}
	byModel[modelID] = bucket
}

func quotaToModelQuota(bucket quotaBucket) *provider.ModelQuota {
	return &provider.ModelQuota{
		RemainingPct: bucket.RemainingFraction * 100,
		Source:       "gemini-codeassist-quota",
		ResetAt:      parseResetTime(bucket.ResetTime),
	}
}

func prependGeminiAutoGroups(models []provider.Model, capabilities []provider.Capability) []provider.Model {
	if len(models) == 0 {
		return models
	}
	available := map[string]struct{}{}
	for _, model := range models {
		available[normalizeModelID(model.ID)] = struct{}{}
	}
	groups := make([]provider.Model, 0, 2)
	if members := availableGeminiGroupMembers(available, []string{"gemini-3.1-pro-preview", "gemini-3-pro-preview", "gemini-3-flash-preview"}); len(members) > 0 {
		groups = append(groups, provider.Model{
			ID:               "auto-gemini-3",
			Aliases:          []string{"gemini-default", "gemini-auto", "Auto Gemini 3", "Auto (Gemini 3)"},
			Capabilities:     capabilities,
			ContextTokens:    geminiContextTokens("auto-gemini-3"),
			MaxContextTokens: geminiContextTokens("auto-gemini-3"),
			MaxOutputTokens:  geminiMaxOutputTokens("auto-gemini-3"),
			Kind:             "group",
			GroupMembers:     members,
		})
	}
	if members := availableGeminiGroupMembers(available, []string{"gemini-2.5-pro", "gemini-2.5-flash"}); len(members) > 0 {
		groups = append(groups, provider.Model{
			ID:               "auto-gemini-2.5",
			Aliases:          []string{"gemini-auto-2.5", "Auto Gemini 2.5", "Auto (Gemini 2.5)"},
			Capabilities:     capabilities,
			ContextTokens:    geminiContextTokens("auto-gemini-2.5"),
			MaxContextTokens: geminiContextTokens("auto-gemini-2.5"),
			MaxOutputTokens:  geminiMaxOutputTokens("auto-gemini-2.5"),
			Kind:             "group",
			GroupMembers:     members,
		})
	}
	if len(groups) == 0 {
		return models
	}
	return append(groups, models...)
}

func availableGeminiGroupMembers(available map[string]struct{}, candidates []string) []string {
	members := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		candidate = normalizeModelID(candidate)
		if _, ok := available[candidate]; ok {
			members = append(members, candidate)
		}
	}
	return members
}

func mergeGeminiModels(configured []provider.Model, discovered []provider.Model) []provider.Model {
	if len(configured) == 0 {
		return cloneModels(discovered)
	}
	out := cloneModels(configured)
	index := make(map[string]int, len(out))
	for i, model := range out {
		index[normalizeModelID(model.ID)] = i
	}
	for _, model := range discovered {
		model.ID = normalizeModelID(model.ID)
		if model.ID == "" {
			continue
		}
		if i, ok := index[model.ID]; ok {
			out[i] = mergeGeminiModel(out[i], model)
			continue
		}
		index[model.ID] = len(out)
		out = append(out, cloneModels([]provider.Model{model})[0])
	}
	return out
}

func mergeGeminiModel(configured provider.Model, discovered provider.Model) provider.Model {
	configured.ID = normalizeModelID(configured.ID)
	configured.Aliases = mergeGeminiStrings(configured.Aliases, discovered.Aliases)
	configured.Capabilities = dedupeCapabilities(append(configured.Capabilities, discovered.Capabilities...))
	if configured.ContextTokens == 0 {
		configured.ContextTokens = discovered.ContextTokens
	}
	if configured.MaxContextTokens == 0 {
		configured.MaxContextTokens = discovered.MaxContextTokens
	}
	if configured.MaxOutputTokens == 0 {
		configured.MaxOutputTokens = discovered.MaxOutputTokens
	}
	if configured.Kind == "" {
		configured.Kind = discovered.Kind
	}
	configured.GroupMembers = mergeGeminiStrings(configured.GroupMembers, discovered.GroupMembers)
	if discovered.Quota != nil {
		quota := *discovered.Quota
		configured.Quota = &quota
	}
	return configured
}

func mergeGeminiStrings(a []string, b []string) []string {
	seen := make(map[string]struct{}, len(a)+len(b))
	out := make([]string, 0, len(a)+len(b))
	for _, value := range append(a, b...) {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		key := strings.ToLower(value)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, value)
	}
	return out
}

func canonicalGeminiModelID(modelID string) string {
	modelID = normalizeModelID(modelID)
	switch modelID {
	case "gemini-3.1-pro":
		return "gemini-3.1-pro-preview"
	case "gemini-3-pro":
		return "gemini-3-pro-preview"
	case "gemini-3-flash":
		return "gemini-3-flash-preview"
	case "gemini-3.1-flash-lite":
		return "gemini-3.1-flash-lite-preview"
	default:
		return modelID
	}
}

func normalizeModelID(modelID string) string {
	return strings.ToLower(strings.TrimSpace(modelID))
}

func geminiModelAliases(rawID string, modelID string) []string {
	rawID = normalizeModelID(rawID)
	modelID = normalizeModelID(modelID)
	var aliases []string
	if rawID != "" && rawID != modelID {
		aliases = append(aliases, rawID)
	}
	if display := humanizeGeminiModelID(modelID); display != "" && display != modelID {
		aliases = append(aliases, display)
	}
	if rawDisplay := humanizeGeminiModelID(rawID); rawDisplay != "" && rawDisplay != humanizeGeminiModelID(modelID) {
		aliases = append(aliases, rawDisplay)
	}
	switch modelID {
	case "gemini-2.5-flash":
		aliases = append(aliases, "flash")
	case "gemini-2.5-flash-lite":
		aliases = append(aliases, "flash-lite")
	}
	return mergeGeminiStrings(nil, aliases)
}

func humanizeGeminiModelID(modelID string) string {
	modelID = normalizeModelID(modelID)
	if modelID == "" {
		return ""
	}
	parts := strings.Split(modelID, "-")
	words := make([]string, 0, len(parts))
	for _, part := range parts {
		switch part {
		case "gemini":
			words = append(words, "Gemini")
		case "gemma":
			words = append(words, "Gemma")
		case "pro":
			words = append(words, "Pro")
		case "flash":
			words = append(words, "Flash")
		case "lite":
			words = append(words, "Lite")
		case "preview":
			words = append(words, "Preview")
		case "it":
			words = append(words, "IT")
		default:
			if strings.HasSuffix(part, "b") {
				words = append(words, strings.TrimSuffix(part, "b")+"B")
			} else {
				words = append(words, part)
			}
		}
	}
	return strings.Join(words, " ")
}

func geminiModelCapabilities(capabilities []provider.Capability) []provider.Capability {
	caps := dedupeCapabilities(capabilities)
	if len(caps) == 0 {
		caps = []provider.Capability{
			provider.CapabilityOpenAIChat,
			provider.CapabilityAnthropicMessages,
			provider.CapabilityGeminiGenerateContent,
			provider.CapabilityStreamSSE,
		}
	}
	return caps
}

func geminiContextTokens(modelID string) int {
	modelID = normalizeModelID(modelID)
	if strings.Contains(modelID, "gemini-") {
		return 1_048_576
	}
	return 0
}

func geminiMaxOutputTokens(modelID string) int {
	modelID = normalizeModelID(modelID)
	if strings.Contains(modelID, "gemini-") {
		return 65_536
	}
	return 0
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
