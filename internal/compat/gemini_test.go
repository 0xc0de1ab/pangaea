package compat

import (
	"errors"
	"testing"
)

func TestGeminiGenerateContentRequestToCanonical(t *testing.T) {
	temperature := 0.2
	request, err := GeminiGenerateContentRequestToCanonical(GeminiGenerateContentRequest{
		SystemInstruction: &GeminiContent{
			Parts: []GeminiPart{{Text: "be brief"}},
		},
		GenerationConfig: &GeminiGenerationConfig{
			Temperature:     &temperature,
			MaxOutputTokens: 64,
		},
		Contents: []GeminiContent{
			{Role: "user", Parts: []GeminiPart{{Text: "hello"}}},
			{Role: "model", Parts: []GeminiPart{{Text: "hi"}}},
		},
	}, "gemini-2.5-flash")
	if err != nil {
		t.Fatalf("expected conversion to succeed: %v", err)
	}

	if request.Dialect != APIDialectGemini {
		t.Fatalf("expected Gemini dialect, got %q", request.Dialect)
	}
	if request.MaxOutputTokens != 64 {
		t.Fatalf("expected max output tokens 64, got %d", request.MaxOutputTokens)
	}
	if len(request.Messages) != 3 || request.Messages[0].Role != MessageRoleSystem {
		t.Fatalf("expected system plus two messages, got %#v", request.Messages)
	}
}

func TestGeminiGenerateContentRequestToCanonicalToolUse(t *testing.T) {
	request, err := GeminiGenerateContentRequestToCanonical(GeminiGenerateContentRequest{
		Contents: []GeminiContent{
			{Role: "model", Parts: []GeminiPart{{FunctionCall: &GeminiFunctionCall{Name: "lookup", ID: "call_1", Args: map[string]any{"q": "status"}}}}},
			{Role: "user", Parts: []GeminiPart{{FunctionResponse: &GeminiFunctionResponse{ID: "call_1", Response: map[string]any{"ok": true}}}}},
		},
	}, "gemini-2.5-flash")
	if err != nil {
		t.Fatalf("expected conversion to succeed: %v", err)
	}

	if got := request.Messages[0].ToolCalls[0].Name; got != "lookup" {
		t.Fatalf("expected lookup tool call, got %q", got)
	}
	if request.Messages[1].Role != MessageRoleTool || request.Messages[1].ToolCallID != "call_1" {
		t.Fatalf("expected tool result message, got %#v", request.Messages[1])
	}
}

func TestGeminiGenerateContentResponseFromCanonical(t *testing.T) {
	response, err := GeminiGenerateContentResponseFromCanonical(Response{
		ID:      "resp_1",
		Dialect: APIDialectGemini,
		Model:   "gemini-2.5-flash",
		Message: Message{
			Role:    MessageRoleAssistant,
			Content: []ContentPart{{Type: ContentPartText, Text: "hello"}},
		},
		StopReason: "stop",
		Usage:      Usage{InputTokens: 2, OutputTokens: 3},
	})
	if err != nil {
		t.Fatalf("expected conversion to succeed: %v", err)
	}

	if response.ModelVersion != "gemini-2.5-flash" {
		t.Fatalf("expected model version, got %q", response.ModelVersion)
	}
	if got := response.Candidates[0].Content.Parts[0].Text; got != "hello" {
		t.Fatalf("expected text part, got %q", got)
	}
	if response.UsageMetadata == nil || response.UsageMetadata.TotalTokenCount != 5 {
		t.Fatalf("expected synthesized total usage of 5, got %#v", response.UsageMetadata)
	}
}

func TestGeminiGenerateContentRequestRejectsInlineData(t *testing.T) {
	_, err := GeminiGenerateContentRequestToCanonical(GeminiGenerateContentRequest{
		Contents: []GeminiContent{
			{Role: "user", Parts: []GeminiPart{{InlineData: &GeminiInlineData{MIMEType: "image/png", Data: "abc"}}}},
		},
	}, "gemini-2.5-flash")
	if !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("expected ErrInvalidRequest, got %v", err)
	}
}
