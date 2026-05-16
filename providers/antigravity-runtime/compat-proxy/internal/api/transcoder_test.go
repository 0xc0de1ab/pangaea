package api

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/antigravity-compat-proxy/internal/models"
)

func TestTranscoders(t *testing.T) {
	testCases := []struct {
		name       string
		inputFile  string
		expected   string
		transcoder func([]byte) (string, []models.Media, error)
	}{
		{
			name:      "OpenAI",
			inputFile: "openai_request.json",
			expected:  "[System]\nYou are a helpful assistant.\n\n[User]\nHello!\n",
			transcoder: func(data []byte) (string, []models.Media, error) {
				var req models.ChatCompletionRequest
				if err := json.Unmarshal(data, &req); err != nil {
					return "", nil, err
				}
				prompt, media := TranscodeMessages(req.Messages)
				return prompt, media, nil
			},
		},
		{
			name:      "Anthropic",
			inputFile: "anthropic_request.json",
			expected:  "[User]\nHello, Claude!\n",
			transcoder: func(data []byte) (string, []models.Media, error) {
				var req models.AnthropicRequest
				if err := json.Unmarshal(data, &req); err != nil {
					return "", nil, err
				}
				prompt, media := TranscodeAnthropicMessages(req)
				return prompt, media, nil
			},
		},
		{
			name:      "Gemini",
			inputFile: "gemini_request.json",
			expected:  "[System]\nYou are a helpful AI assistant.\n\n[User]\nHello, Gemini!\n",
			transcoder: func(data []byte) (string, []models.Media, error) {
				var req models.GeminiRequest
				if err := json.Unmarshal(data, &req); err != nil {
					return "", nil, err
				}
				prompt, media := TranscodeGeminiMessages(req)
				return prompt, media, nil
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			inputPath := filepath.Join("..", "..", "testdata", "golden", tc.inputFile)
			inputData, err := os.ReadFile(inputPath)
			if err != nil {
				t.Fatalf("failed to read input file: %v", err)
			}

			actual, media, err := tc.transcoder(inputData)
			if err != nil {
				t.Fatalf("transcoder failed: %v", err)
			}
			if len(media) != 0 {
				t.Fatalf("expected no media attachments, got %d", len(media))
			}

			if actual != tc.expected {
				t.Errorf("unexpected prompt\nwant:\n%q\ngot:\n%q", tc.expected, actual)
			}
		})
	}
}

func TestParseToolCallsFromMarkdownJSON(t *testing.T) {
	response := "```json\n{\n  \"tool\": \"get_weather\",\n  \"parameters\": {\n    \"city\": \"Seoul\",\n    \"unit\": \"celsius\"\n  }\n}\n```"
	calls := ParseToolCalls(response)
	if len(calls) != 1 {
		t.Fatalf("expected one tool call, got %#v", calls)
	}
	call := calls[0]
	if call.Function.Name != "get_weather" || call.Function.Arguments != `{"city":"Seoul","unit":"celsius"}` {
		t.Fatalf("unexpected tool call: %#v", call)
	}
}

func TestParseToolCallsFromToolCallsArray(t *testing.T) {
	response := `{"tool_calls":[{"function":{"name":"lookup","arguments":{"id":"42"}}}]}`
	calls := ParseToolCalls(response)
	if len(calls) != 1 {
		t.Fatalf("expected one tool call, got %#v", calls)
	}
	call := calls[0]
	if call.Function.Name != "lookup" || call.Function.Arguments != `{"id":"42"}` {
		t.Fatalf("unexpected tool call: %#v", call)
	}
}
