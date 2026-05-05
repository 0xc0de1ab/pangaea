package compat

import (
	"errors"
	"testing"
)

func TestRequestValidateRequiresModel(t *testing.T) {
	request := validRequest()
	request.Model = ""

	if err := request.Validate(); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("expected ErrInvalidRequest, got %v", err)
	}
}

func TestRequestValidateAllowsAssistantToolCallWithoutContent(t *testing.T) {
	request := validRequest()
	request.Messages = append(request.Messages, Message{
		Role: MessageRoleAssistant,
		ToolCalls: []ToolCall{
			{
				ID:        "call_1",
				Type:      ToolCallFunction,
				Name:      "lookup",
				Arguments: `{"q":"status"}`,
			},
		},
	})

	if err := request.Validate(); err != nil {
		t.Fatalf("expected valid request: %v", err)
	}
}

func TestResponseValidateRequiresAssistantMessage(t *testing.T) {
	response := validResponse()
	response.Message.Role = MessageRoleUser

	if err := response.Validate(); !errors.Is(err, ErrInvalidResponse) {
		t.Fatalf("expected ErrInvalidResponse, got %v", err)
	}
}

func TestEventValidateRequiresDeltaPayload(t *testing.T) {
	event := Event{Type: EventContentDelta}

	if err := event.Validate(); !errors.Is(err, ErrInvalidEvent) {
		t.Fatalf("expected ErrInvalidEvent, got %v", err)
	}
}

func TestEventValidateAcceptsUsageDelta(t *testing.T) {
	event := Event{
		Type:       EventUsageDelta,
		UsageDelta: &Usage{OutputTokens: 1},
	}

	if err := event.Validate(); err != nil {
		t.Fatalf("expected valid usage delta: %v", err)
	}
}

func validRequest() Request {
	return Request{
		Dialect: APIDialectOpenAI,
		Model:   "gpt-5.1",
		Messages: []Message{
			{
				Role:    MessageRoleUser,
				Content: []ContentPart{{Type: ContentPartText, Text: "hello"}},
			},
		},
		UnsupportedFeatures: UnsupportedFeatureReject,
	}
}

func validResponse() Response {
	return Response{
		Dialect: APIDialectOpenAI,
		Model:   "gpt-5.1",
		Message: Message{
			Role:    MessageRoleAssistant,
			Content: []ContentPart{{Type: ContentPartText, Text: "hi"}},
		},
		Usage: Usage{InputTokens: 4, OutputTokens: 2, TotalTokens: 6},
	}
}
