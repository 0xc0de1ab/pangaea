package api

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/google/antigravity-compat-proxy/internal/models"
)

// TranscodeMessages converts OpenAI-style messages into a single Antigravity prompt string and media attachments.
func TranscodeMessages(messages []models.ChatMessage) (string, []models.Media) {
	var parts []string
	var media []models.Media

	for _, msg := range messages {
		role := strings.ToLower(msg.Role)
		if role == "" {
			role = "user"
		}

		var subTexts []string
		switch c := msg.Content.(type) {
		case string:
			subTexts = append(subTexts, c)
		case []interface{}:
			for _, blockRaw := range c {
				blockBytes, _ := json.Marshal(blockRaw)
				var part map[string]interface{}
				json.Unmarshal(blockBytes, &part)

				if part["type"] == "text" {
					subTexts = append(subTexts, fmt.Sprintf("%v", part["text"]))
				} else if part["type"] == "image_url" {
					urlObj, _ := part["image_url"].(map[string]interface{})
					url, _ := urlObj["url"].(string)
					if strings.HasPrefix(url, "data:") {
						commaIdx := strings.Index(url, ",")
						if commaIdx != -1 {
							b64 := url[commaIdx+1:]
							mime := "image/jpeg"
							semicolonIdx := strings.Index(url, ";")
							if semicolonIdx != -1 {
								mime = url[5:semicolonIdx]
							}
							// Embed in prompt using discovered tag
							subTexts = append(subTexts, fmt.Sprintf("<image>%s</image>", b64))
							// Also keep in media field for double safety
							media = append(media, models.Media{
								Image: &models.ImageData{
									Base64Data: b64,
									MimeType:   mime,
								},
							})
						}
					}
				}
			}
		}

		contentStr := strings.Join(subTexts, "\n")

		switch role {
		case "system":
			parts = append(parts, fmt.Sprintf("[System]\n%s\n", contentStr))
		case "user":
			parts = append(parts, fmt.Sprintf("[User]\n%s\n", contentStr))
		case "assistant":
			var tcTexts []string
			for _, tc := range msg.ToolCalls {
				tcTexts = append(tcTexts, fmt.Sprintf("<tool_call>%s</tool_call>", tc.Function.Arguments))
			}
			parts = append(parts, fmt.Sprintf("[Assistant]\n%s%s\n", contentStr, strings.Join(tcTexts, "\n")))
		case "tool", "function":
			contentStr = strings.ReplaceAll(contentStr, "<", "&lt;")
			contentStr = strings.ReplaceAll(contentStr, ">", "&gt;")
			parts = append(parts, fmt.Sprintf("<observation>\n[Tool Result: %s]\n%s\n</observation>\n", msg.Name, contentStr))
		default:
			parts = append(parts, fmt.Sprintf("[%s]\n%s\n", strings.Title(role), contentStr))
		}
	}
	return strings.Join(parts, "\n"), media
}

// TranscodeAnthropicMessages converts Anthropic-style messages into a single Antigravity prompt string and media.
func TranscodeAnthropicMessages(req models.AnthropicRequest) (string, []models.Media) {
	var parts []string
	var media []models.Media

	if req.System != "" {
		parts = append(parts, fmt.Sprintf("[System]\n%s\n", req.System))
	}

	for _, msg := range req.Messages {
		role := strings.ToLower(msg.Role)
		prefix := "[User]"
		if role == "assistant" {
			prefix = "[Assistant]"
		}

		switch content := msg.Content.(type) {
		case string:
			parts = append(parts, fmt.Sprintf("%s\n%s\n", prefix, content))
		case []interface{}:
			var subTexts []string
			for _, blockRaw := range content {
				blockBytes, _ := json.Marshal(blockRaw)
				var block map[string]interface{}
				json.Unmarshal(blockBytes, &block)

				switch block["type"] {
				case "text":
					subTexts = append(subTexts, fmt.Sprintf("%v", block["text"]))
				case "image":
					source, _ := block["source"].(map[string]interface{})
					if source["type"] == "base64" {
						b64 := fmt.Sprintf("%v", source["data"])
						mime := fmt.Sprintf("%v", source["media_type"])
						subTexts = append(subTexts, fmt.Sprintf("<image>%s</image>", b64))
						media = append(media, models.Media{
							Image: &models.ImageData{
								Base64Data: b64,
								MimeType:   mime,
							},
						})
					}
				case "tool_use":
					inputBytes, _ := json.Marshal(block["input"])
					subTexts = append(subTexts, fmt.Sprintf("<tool_call>%s</tool_call>", string(inputBytes)))
				case "tool_result":
					text := fmt.Sprintf("%v", block["content"])
					text = strings.ReplaceAll(text, "<", "&lt;")
					text = strings.ReplaceAll(text, ">", "&gt;")
					parts = append(parts, fmt.Sprintf("<observation>\n[Tool Result: %s]\n%s\n</observation>\n", block["name"], text))
				}
			}
			if len(subTexts) > 0 {
				parts = append(parts, fmt.Sprintf("%s\n%s\n", prefix, strings.Join(subTexts, "\n")))
			}
		}
	}
	return strings.Join(parts, "\n"), media
}

// TranscodeGeminiMessages converts Gemini-style contents into a single Antigravity prompt string and media.
func TranscodeGeminiMessages(req models.GeminiRequest) (string, []models.Media) {
	var parts []string
	var media []models.Media

	if req.SystemInstruction != nil {
		var content strings.Builder
		for i, part := range req.SystemInstruction.Parts {
			if i > 0 {
				content.WriteString("\n")
			}
			content.WriteString(part.Text)
		}
		parts = append(parts, fmt.Sprintf("[System]\n%s\n", content.String()))
	}

	for _, content := range req.Contents {
		role := strings.ToLower(content.Role)
		prefix := "[User]"
		if role == "model" || role == "assistant" {
			prefix = "[Assistant]"
		}

		var subTexts []string
		for _, part := range content.Parts {
			if part.Text != "" {
				subTexts = append(subTexts, part.Text)
			}
			if part.InlineData != nil {
				subTexts = append(subTexts, fmt.Sprintf("<image>%s</image>", part.InlineData.Data))
				media = append(media, models.Media{
					Image: &models.ImageData{
						Base64Data: part.InlineData.Data,
						MimeType:   part.InlineData.MimeType,
					},
				})
			}
		}

		parts = append(parts, fmt.Sprintf("%s\n%s\n", prefix, strings.Join(subTexts, "\n")))
	}
	return strings.Join(parts, "\n"), media
}

type ToolCallParseResult struct {
	Calls     []models.ToolCall
	Malformed bool
	Reason    string
}

// ParseToolCalls extracts tool calls from the raw response text.
func ParseToolCalls(responseText string) []models.ToolCall {
	return ParseToolCallResult(responseText).Calls
}

func ParseToolCallResult(responseText string) ToolCallParseResult {
	var toolCalls []models.ToolCall
	firstObsIdx := strings.Index(strings.ToLower(responseText), "<observation>")
	parseText := responseText
	if firstObsIdx != -1 {
		parseText = responseText[:firstObsIdx]
	}
	lower := strings.ToLower(parseText)
	re := regexp.MustCompile(`(?is)<tool_call>\s*([\s\S]*?)\s*</tool_call>`)
	matches := re.FindAllStringSubmatch(parseText, -1)
	hasInvalidTaggedCall := false
	for i, match := range matches {
		if len(match) > 1 {
			payload := strings.TrimSpace(match[1])
			if tc, ok := parseToolCallObject(payload, i); ok {
				toolCalls = append(toolCalls, tc)
			} else if calls := parseToolCallPayload(payload); len(calls) > 0 {
				toolCalls = append(toolCalls, calls...)
			} else {
				hasInvalidTaggedCall = true
			}
		}
	}
	openTagCount := strings.Count(lower, "<tool_call")
	closeTagCount := strings.Count(lower, "</tool_call>")
	hasIncompleteTaggedCall := openTagCount != closeTagCount
	if hasInvalidTaggedCall || hasIncompleteTaggedCall || (len(toolCalls) == 0 && (openTagCount > 0 || closeTagCount > 0)) {
		reason := "upstream emitted a malformed tool call"
		if hasIncompleteTaggedCall {
			reason = "upstream emitted an incomplete tool call"
		} else if hasInvalidTaggedCall {
			reason = "upstream emitted a tool call that could not be parsed"
		}
		return ToolCallParseResult{Malformed: true, Reason: reason}
	}
	if len(toolCalls) > 0 {
		reindexToolCalls(toolCalls)
		return ToolCallParseResult{Calls: toolCalls}
	}
	for _, payload := range candidateToolJSONPayloads(parseText) {
		if calls := parseToolCallPayload(payload); len(calls) > 0 {
			return ToolCallParseResult{Calls: calls}
		}
	}
	return ToolCallParseResult{}
}

func candidateToolJSONPayloads(text string) []string {
	var out []string
	fenceRe := regexp.MustCompile("(?is)```(?:json)?\\s*([\\[{][\\s\\S]*?[\\]}])\\s*```")
	for _, match := range fenceRe.FindAllStringSubmatch(text, -1) {
		if len(match) > 1 {
			out = append(out, strings.TrimSpace(match[1]))
		}
	}
	trimmed := strings.TrimSpace(text)
	if strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[") {
		out = append(out, trimmed)
	}
	return out
}

func parseToolCallPayload(payload string) []models.ToolCall {
	var raw any
	if err := json.Unmarshal([]byte(payload), &raw); err != nil {
		return nil
	}
	return parseToolCallValue(raw)
}

func parseToolCallValue(value any) []models.ToolCall {
	switch v := value.(type) {
	case []any:
		var out []models.ToolCall
		for _, item := range v {
			out = append(out, parseToolCallValue(item)...)
		}
		reindexToolCalls(out)
		return out
	case map[string]any:
		if nested, ok := v["tool_calls"]; ok {
			out := parseToolCallValue(nested)
			reindexToolCalls(out)
			return out
		}
		if nested, ok := v["tools"]; ok {
			out := parseToolCallValue(nested)
			reindexToolCalls(out)
			return out
		}
		if tc, ok := toolCallFromMap(v, 0); ok {
			return []models.ToolCall{tc}
		}
	}
	return nil
}

func parseToolCallObject(payload string, index int) (models.ToolCall, bool) {
	var raw map[string]any
	if err := json.Unmarshal([]byte(payload), &raw); err != nil {
		return models.ToolCall{}, false
	}
	return toolCallFromMap(raw, index)
}

func toolCallFromMap(raw map[string]any, index int) (models.ToolCall, bool) {
	if function, ok := raw["function"].(map[string]any); ok {
		merged := map[string]any{}
		for k, v := range raw {
			merged[k] = v
		}
		for k, v := range function {
			merged[k] = v
		}
		raw = merged
	}
	name := firstStringValue(raw, "name", "tool_name", "tool", "function_name")
	if name == "" {
		return models.ToolCall{}, false
	}
	args := firstAnyValue(raw, "arguments", "parameters", "input", "args")
	arguments := "{}"
	switch v := args.(type) {
	case nil:
	case string:
		if strings.TrimSpace(v) != "" {
			arguments = v
		}
	default:
		argsBytes, _ := json.Marshal(v)
		arguments = string(argsBytes)
	}
	id := firstStringValue(raw, "id", "call_id", "tool_call_id")
	if id == "" {
		id = fmt.Sprintf("call_%d_%d", time.Now().Unix(), index)
	}
	return models.ToolCall{
		Index: index,
		ID:    id,
		Type:  "function",
		Function: models.ToolFunction{
			Name:      name,
			Arguments: arguments,
		},
	}, true
}

func firstStringValue(raw map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := raw[key]; ok {
			if text := strings.TrimSpace(fmt.Sprintf("%v", value)); text != "" && text != "<nil>" {
				return text
			}
		}
	}
	return ""
}

func firstAnyValue(raw map[string]any, keys ...string) any {
	for _, key := range keys {
		if value, ok := raw[key]; ok {
			return value
		}
	}
	return nil
}

func reindexToolCalls(calls []models.ToolCall) {
	for i := range calls {
		calls[i].Index = i
		if calls[i].ID == "" {
			calls[i].ID = fmt.Sprintf("call_%d_%d", time.Now().Unix(), i)
		}
	}
}

func TranscodeOpenAITools(tools []models.Tool) []models.ToolDefinition {
	var result []models.ToolDefinition
	for _, t := range tools {
		if t.Type == "function" {
			result = append(result, models.ToolDefinition{
				Name:        t.Function.Name,
				Description: t.Function.Description,
				InputSchema: t.Function.Parameters,
			})
		}
	}
	return result
}

func TranscodeAnthropicTools(tools []models.AnthropicTool) []models.ToolDefinition {
	var result []models.ToolDefinition
	for _, t := range tools {
		result = append(result, models.ToolDefinition{
			Name:        t.Name,
			Description: t.Description,
			InputSchema: t.InputSchema,
		})
	}
	return result
}
