package compat

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestAnthropicMessagesRequestToCanonical(t *testing.T) {
	system, _ := json.Marshal("be brief")
	request, err := AnthropicMessagesRequestToCanonical(AnthropicMessagesRequest{
		Model:     "claude-sonnet",
		MaxTokens: 128,
		System:    system,
		Messages: []AnthropicMessage{
			{Role: "user", Content: json.RawMessage(`"hello"`)},
			{Role: "assistant", Content: json.RawMessage(`[{"type":"text","text":"hi"}]`)},
		},
	})
	if err != nil {
		t.Fatalf("expected conversion to succeed: %v", err)
	}

	if request.Dialect != APIDialectAnthropic {
		t.Fatalf("expected anthropic dialect, got %q", request.Dialect)
	}
	if request.MaxOutputTokens != 128 {
		t.Fatalf("expected max output tokens 128, got %d", request.MaxOutputTokens)
	}
	if len(request.Messages) != 3 || request.Messages[0].Role != MessageRoleSystem {
		t.Fatalf("expected system plus two messages, got %#v", request.Messages)
	}
}

func TestAnthropicMessagesRequestToCanonicalToolUse(t *testing.T) {
	request, err := AnthropicMessagesRequestToCanonical(AnthropicMessagesRequest{
		Model: "claude-sonnet",
		Messages: []AnthropicMessage{
			{Role: "assistant", Content: json.RawMessage(`[{"type":"tool_use","id":"toolu_1","name":"lookup","input":{"q":"status"}}]`)},
			{Role: "user", Content: json.RawMessage(`[{"type":"tool_result","tool_use_id":"toolu_1","content":"ok"}]`)},
		},
	})
	if err != nil {
		t.Fatalf("expected conversion to succeed: %v", err)
	}

	if got := request.Messages[0].ToolCalls[0].Name; got != "lookup" {
		t.Fatalf("expected lookup tool call, got %q", got)
	}
	if request.Messages[1].Role != MessageRoleTool || request.Messages[1].ToolCallID != "toolu_1" {
		t.Fatalf("expected tool result message, got %#v", request.Messages[1])
	}
}

func TestAnthropicMessagesResponseFromCanonical(t *testing.T) {
	response, err := AnthropicMessagesResponseFromCanonical(Response{
		ID:      "msg_1",
		Dialect: APIDialectAnthropic,
		Model:   "claude-sonnet",
		Message: Message{
			Role:    MessageRoleAssistant,
			Content: []ContentPart{{Type: ContentPartText, Text: "hello"}},
			ToolCalls: []ToolCall{{
				ID:        "toolu_1",
				Type:      ToolCallFunction,
				Name:      "lookup",
				Arguments: `{"q":"status"}`,
			}},
		},
		StopReason: "tool_calls",
		Usage:      Usage{InputTokens: 5, OutputTokens: 7, TotalTokens: 12},
	})
	if err != nil {
		t.Fatalf("expected conversion to succeed: %v", err)
	}

	if response.Type != "message" || response.Role != "assistant" {
		t.Fatalf("expected Anthropic message response, got %#v", response)
	}
	if response.StopReason != "tool_use" {
		t.Fatalf("expected tool_use stop reason, got %q", response.StopReason)
	}
	if len(response.Content) != 2 || response.Content[1].Type != "tool_use" {
		t.Fatalf("expected text and tool_use blocks, got %#v", response.Content)
	}
}

func TestAnthropicMessagesRequestRejectsUnsupportedBlock(t *testing.T) {
	_, err := AnthropicMessagesRequestToCanonical(AnthropicMessagesRequest{
		Model: "claude-sonnet",
		Messages: []AnthropicMessage{
			{Role: "user", Content: json.RawMessage(`[{"type":"image","source":{"type":"base64"}}]`)},
		},
	})
	if !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("expected ErrInvalidRequest, got %v", err)
	}
}
