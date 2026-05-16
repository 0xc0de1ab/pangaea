package compat

import (
	"encoding/json"
	"testing"
)

func TestOpenAIChatRequestToCanonical(t *testing.T) {
	temperature := 0.7
	request, err := OpenAIChatRequestToCanonical(OpenAIChatRequest{
		Model:               "gpt-5.1",
		Temperature:         &temperature,
		MaxCompletionTokens: 128,
		Stream:              true,
		Messages: []OpenAIChatMessage{
			{Role: "system", Content: "be concise"},
			{Role: "user", Content: "hello"},
		},
	})
	if err != nil {
		t.Fatalf("expected conversion to succeed: %v", err)
	}

	if request.Dialect != APIDialectOpenAI {
		t.Fatalf("expected OpenAI dialect, got %q", request.Dialect)
	}
	if request.MaxOutputTokens != 128 {
		t.Fatalf("expected max output tokens 128, got %d", request.MaxOutputTokens)
	}
	if got := request.Messages[1].Content[0].Text; got != "hello" {
		t.Fatalf("expected user content to round trip, got %q", got)
	}
}

func TestOpenAIChatResponseFromCanonical(t *testing.T) {
	response, err := OpenAIChatResponseFromCanonical(Response{
		ID:      "chatcmpl_1",
		Dialect: APIDialectOpenAI,
		Model:   "gpt-5.1",
		Message: Message{
			Role: MessageRoleAssistant,
			Content: []ContentPart{
				{Type: ContentPartText, Text: "hello"},
				{Type: ContentPartText, Text: " there"},
			},
		},
		StopReason: "stop",
		Usage:      Usage{InputTokens: 2, OutputTokens: 3},
	})
	if err != nil {
		t.Fatalf("expected conversion to succeed: %v", err)
	}

	if response.Object != "chat.completion" {
		t.Fatalf("expected chat.completion object, got %q", response.Object)
	}
	if len(response.Choices) != 1 {
		t.Fatalf("expected one choice, got %d", len(response.Choices))
	}
	if got := response.Choices[0].Message.Content; got != "hello there" {
		t.Fatalf("expected combined content, got %q", got)
	}
	if response.Usage == nil || response.Usage.TotalTokens != 5 {
		t.Fatalf("expected synthesized total usage of 5, got %#v", response.Usage)
	}
}

func TestOpenAIResponsesRequestToCanonicalStringInput(t *testing.T) {
	temperature := 0.2
	request, err := OpenAIResponsesRequestToCanonical(OpenAIResponsesRequest{
		Model:           "gpt-5.2",
		Input:           json.RawMessage(`"hello responses"`),
		Instructions:    "be concise",
		Temperature:     &temperature,
		Reasoning:       &OpenAIResponsesReasoning{Effort: "medium"},
		MaxOutputTokens: 256,
		Stream:          true,
	})
	if err != nil {
		t.Fatalf("expected conversion to succeed: %v", err)
	}
	if request.Dialect != APIDialectOpenAI || request.Model != "gpt-5.2" || !request.Stream {
		t.Fatalf("unexpected canonical request metadata: %#v", request)
	}
	if request.ReasoningEffort != "medium" || request.MaxOutputTokens != 256 {
		t.Fatalf("unexpected generation options: %#v", request)
	}
	if len(request.Messages) != 2 || request.Messages[0].Role != MessageRoleSystem || request.Messages[1].Role != MessageRoleUser {
		t.Fatalf("unexpected canonical messages: %#v", request.Messages)
	}
	if got := request.Messages[1].Content[0].Text; got != "hello responses" {
		t.Fatalf("expected user text to round trip, got %q", got)
	}
}

func TestOpenAIResponsesRequestToCanonicalMessageInput(t *testing.T) {
	request, err := OpenAIResponsesRequestToCanonical(OpenAIResponsesRequest{
		Model: "gpt-5.2",
		Input: json.RawMessage(`[
			{"role":"user","content":[{"type":"input_text","text":"look"},{"type":"input_image","image_url":"data:image/png;base64,abcd"}]}
		]`),
	})
	if err != nil {
		t.Fatalf("expected conversion to succeed: %v", err)
	}
	if len(request.Messages) != 1 || len(request.Messages[0].Content) != 2 {
		t.Fatalf("unexpected canonical messages: %#v", request.Messages)
	}
	if got := request.Messages[0].Content[1]; got.Type != ContentPartImage || got.MIME != "image/png" || got.Data != "abcd" {
		t.Fatalf("unexpected image content part: %#v", got)
	}
}

func TestOpenAIResponsesResponseFromCanonical(t *testing.T) {
	response, err := OpenAIResponsesResponseFromCanonical(Response{
		ID:      "resp_1",
		Dialect: APIDialectOpenAI,
		Model:   "gpt-5.2",
		Message: Message{
			Role: MessageRoleAssistant,
			Content: []ContentPart{
				{Type: ContentPartText, Text: "hello"},
				{Type: ContentPartText, Text: " there"},
			},
		},
		StopReason: "stop",
		Usage:      Usage{InputTokens: 4, OutputTokens: 6},
	})
	if err != nil {
		t.Fatalf("expected conversion to succeed: %v", err)
	}
	if response.Object != "response" || response.Status != "completed" || response.OutputText != "hello there" {
		t.Fatalf("unexpected response metadata: %#v", response)
	}
	if len(response.Output) != 1 || len(response.Output[0].Content) != 1 || response.Output[0].Content[0].Type != "output_text" {
		t.Fatalf("unexpected response output: %#v", response.Output)
	}
	if response.Usage == nil || response.Usage.TotalTokens != 10 {
		t.Fatalf("expected synthesized total usage of 10, got %#v", response.Usage)
	}
}
