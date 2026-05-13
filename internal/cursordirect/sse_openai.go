package cursordirect

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/0xc0de1ab/pangaea/internal/compat"
	"github.com/0xc0de1ab/pangaea/internal/provider"
)

type openAIChatStreamChunk struct {
	ID      string                     `json:"id,omitempty"`
	Model   string                     `json:"model,omitempty"`
	Choices []openAIChatStreamChoice   `json:"choices,omitempty"`
	Usage   *compat.OpenAIUsage        `json:"usage,omitempty"`
	Error   *openAIChatStreamErrorBody `json:"error,omitempty"`
}

type openAIChatStreamChoice struct {
	Index        int                   `json:"index"`
	Delta        openAIChatStreamDelta `json:"delta"`
	FinishReason string                `json:"finish_reason,omitempty"`
}

type openAIChatStreamDelta struct {
	Role      string                      `json:"role,omitempty"`
	Content   string                      `json:"content,omitempty"`
	ToolCalls []openAIStreamToolCallPiece `json:"tool_calls,omitempty"`
}

type openAIStreamToolCallPiece struct {
	Index    int                       `json:"index"`
	ID       string                    `json:"id,omitempty"`
	Type     string                    `json:"type,omitempty"`
	Function openAIStreamFunctionPiece `json:"function,omitempty"`
}

type openAIStreamFunctionPiece struct {
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`
}

type openAIChatStreamErrorBody struct {
	Message string `json:"message,omitempty"`
	Type    string `json:"type,omitempty"`
	Code    any    `json:"code,omitempty"`
}

func processSSEPayloads(body io.Reader, handle func(string) (bool, error)) error {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 1024), 1024*1024)
	var dataLines []string
	for scanner.Scan() {
		line := strings.TrimRight(scanner.Text(), "\r")
		if line == "" {
			if len(dataLines) > 0 {
				done, err := handle(strings.Join(dataLines, "\n"))
				if err != nil {
					return err
				}
				if done {
					return nil
				}
				dataLines = dataLines[:0]
			}
			continue
		}
		if strings.HasPrefix(line, "data:") {
			dataLines = append(dataLines, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	if len(dataLines) > 0 {
		_, err := handle(strings.Join(dataLines, "\n"))
		return err
	}
	return nil
}

func applyOpenAIStreamPayload(response *compat.Response, started *bool, payload string, emit func(compat.Event) error) (bool, error) {
	payload = strings.TrimSpace(payload)
	if payload == "" {
		return false, nil
	}
	if payload == "[DONE]" {
		return true, nil
	}
	var chunk openAIChatStreamChunk
	if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
		return false, err
	}
	if chunk.Error != nil && strings.TrimSpace(chunk.Error.Message) != "" {
		errEvent := compat.Event{
			ResponseID: response.ID,
			Dialect:    response.Dialect,
			Model:      response.Model,
			Type:       compat.EventError,
			Error:      &compat.EventErrorPayload{Message: chunk.Error.Message, Code: stringFromAny(chunk.Error.Code)},
		}
		if emit != nil {
			if err := emit(errEvent); err != nil {
				return false, err
			}
		}
		return false, &provider.UpstreamError{Code: stringFromAny(chunk.Error.Code), Message: chunk.Error.Message}
	}
	if chunk.ID != "" {
		response.ID = chunk.ID
	}
	if chunk.Model != "" {
		response.Model = chunk.Model
	}
	if !*started {
		if emit != nil {
			if err := emit(compat.Event{
				ResponseID: response.ID,
				Dialect:    response.Dialect,
				Model:      response.Model,
				Type:       compat.EventMessageStart,
				Message:    &compat.Message{Role: compat.MessageRoleAssistant},
			}); err != nil {
				return false, err
			}
		}
		*started = true
	}
	for _, choice := range chunk.Choices {
		if choice.Delta.Content != "" {
			part := compat.ContentPart{Type: compat.ContentPartText, Text: choice.Delta.Content}
			if err := compat.ApplyEventToResponse(response, compat.Event{Type: compat.EventContentDelta, ContentDelta: &part}); err != nil {
				return false, err
			}
			if emit != nil {
				if err := emit(compat.Event{
					ResponseID:   response.ID,
					Dialect:      response.Dialect,
					Model:        response.Model,
					Type:         compat.EventContentDelta,
					ContentDelta: &part,
				}); err != nil {
					return false, err
				}
			}
		}
		for _, piece := range choice.Delta.ToolCalls {
			idx := piece.Index
			if idx < 0 {
				idx = 0
			}
			tc := ensureAssistantToolCallSlot(&response.Message.ToolCalls, idx)
			mergeOpenAIToolStreamPiece(tc, piece)
			copied := *tc
			ev := compat.Event{
				ResponseID:    response.ID,
				Dialect:       response.Dialect,
				Model:         response.Model,
				Type:          compat.EventToolCallDelta,
				ToolCallDelta: &copied,
			}
			if err := ev.Validate(); err != nil {
				continue
			}
			if emit != nil {
				if err := emit(ev); err != nil {
					return false, err
				}
			}
		}
		if choice.FinishReason != "" {
			response.StopReason = choice.FinishReason
		}
	}
	if chunk.Usage != nil {
		usage := compat.Usage{
			InputTokens:  chunk.Usage.PromptTokens,
			OutputTokens: chunk.Usage.CompletionTokens,
			TotalTokens:  chunk.Usage.TotalTokens,
		}
		if usage.TotalTokens == 0 {
			usage.TotalTokens = usage.InputTokens + usage.OutputTokens
		}
		if err := compat.ApplyEventToResponse(response, compat.Event{Type: compat.EventUsageDelta, UsageDelta: &usage}); err != nil {
			return false, err
		}
		if emit != nil {
			if err := emit(compat.Event{
				ResponseID: response.ID,
				Dialect:    response.Dialect,
				Model:      response.Model,
				Type:       compat.EventUsageDelta,
				UsageDelta: &usage,
			}); err != nil {
				return false, err
			}
		}
	}
	if response.StopReason != "" && emit != nil {
		if err := emit(compat.Event{
			ResponseID: response.ID,
			Dialect:    response.Dialect,
			Model:      response.Model,
			Type:       compat.EventDone,
			DoneReason: response.StopReason,
		}); err != nil {
			return false, err
		}
	}
	return false, nil
}

func ensureAssistantToolCallSlot(calls *[]compat.ToolCall, idx int) *compat.ToolCall {
	if idx < 0 {
		idx = 0
	}
	for len(*calls) <= idx {
		pos := len(*calls)
		*calls = append(*calls, compat.ToolCall{
			Index: pos,
			Type:  compat.ToolCallFunction,
		})
	}
	tc := &(*calls)[idx]
	tc.Index = idx
	if tc.Type == "" {
		tc.Type = compat.ToolCallFunction
	}
	return tc
}

func mergeOpenAIToolStreamPiece(tc *compat.ToolCall, piece openAIStreamToolCallPiece) {
	if piece.ID != "" {
		tc.ID = piece.ID
	}
	if piece.Type != "" {
		tc.Type = compat.ToolCallType(piece.Type)
	}
	if piece.Function.Name != "" {
		tc.Name = piece.Function.Name
	}
	if piece.Function.Arguments != "" {
		tc.Arguments += piece.Function.Arguments
	}
}

func stringFromAny(code any) string {
	switch v := code.(type) {
	case nil:
		return ""
	case string:
		return v
	case fmt.Stringer:
		return v.String()
	case float64:
		return fmt.Sprintf("%.0f", v)
	case int:
		return fmt.Sprintf("%d", v)
	default:
		return fmt.Sprint(v)
	}
}
