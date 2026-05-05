package compat

import "testing"

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
