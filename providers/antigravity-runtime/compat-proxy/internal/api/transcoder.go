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

// ParseToolCalls extracts tool calls from the raw response text.
func ParseToolCalls(responseText string) []models.ToolCall {
	var toolCalls []models.ToolCall
	firstObsIdx := strings.Index(strings.ToLower(responseText), "<observation>")
	parseText := responseText
	if firstObsIdx != -1 {
		parseText = responseText[:firstObsIdx]
	}
	re := regexp.MustCompile(`(?i)<tool_call>\s*(\{[\s\S]*?\})\s*</tool_call>`)
	matches := re.FindAllStringSubmatch(parseText, -1)
	for i, match := range matches {
		if len(match) > 1 {
			var tc models.ToolCall
			var raw map[string]interface{}
			if err := json.Unmarshal([]byte(match[1]), &raw); err == nil {
				tc.Index = i
				tc.ID = fmt.Sprintf("call_%d_%d", time.Now().Unix(), i)
				tc.Type = "function"
				name := raw["name"]
				if name == nil { name = raw["tool_name"] }
				tc.Function.Name = fmt.Sprintf("%v", name)
				args := raw["arguments"]
				if args == nil { args = raw["parameters"] }
				switch v := args.(type) {
				case string:
					tc.Function.Arguments = v
				default:
					argsBytes, _ := json.Marshal(v)
					tc.Function.Arguments = string(argsBytes)
				}
				toolCalls = append(toolCalls, tc)
			}
		}
	}
	return toolCalls
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
