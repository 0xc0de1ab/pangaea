package bridge

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/antigravity-compat-proxy/internal/models"
)

func extractToolCallsFromPayload(payload []byte) []models.ToolCall {
	var raw any
	if err := json.Unmarshal(payload, &raw); err != nil {
		return nil
	}
	calls := extractToolCallsFromValue(raw)
	reindexToolCalls(calls)
	return calls
}

func extractToolCallsFromValue(value any) []models.ToolCall {
	switch v := value.(type) {
	case []any:
		var out []models.ToolCall
		for _, item := range v {
			out = append(out, extractToolCallsFromValue(item)...)
		}
		return out
	case map[string]any:
		if tc, ok := toolCallFromMap(v); ok {
			return []models.ToolCall{tc}
		}
		for _, key := range []string{
			"tool_calls", "toolCalls",
			"tool_call", "toolCall",
			"tool_use", "toolUse",
			"function_call", "functionCall",
			"function_calls", "functionCalls",
		} {
			if nested, ok := v[key]; ok {
				if calls := extractToolCallsFromValue(nested); len(calls) > 0 {
					return calls
				}
			}
		}
		var out []models.ToolCall
		for _, key := range []string{"response", "candidate", "candidates", "content", "parts"} {
			if nested, ok := v[key]; ok {
				out = append(out, extractToolCallsFromValue(nested)...)
			}
		}
		return out
	default:
		return nil
	}
}

func toolCallFromMap(raw map[string]any) (models.ToolCall, bool) {
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
	name := firstStringValue(raw, "name", "tool_name", "toolName", "tool", "function_name", "functionName")
	inferred := false
	if name == "" {
		var ok bool
		name, raw, ok = inferToolCallFromMap(raw)
		if !ok {
			return models.ToolCall{}, false
		}
		inferred = true
	}
	args := firstAnyValue(raw, "arguments", "parameters", "input", "args")
	if inferred && args == nil {
		args = raw
	}
	arguments := "{}"
	switch v := args.(type) {
	case nil:
	case string:
		if strings.TrimSpace(v) != "" {
			arguments = v
		}
	default:
		if bytes, err := json.Marshal(v); err == nil {
			arguments = string(bytes)
		}
	}
	id := firstStringValue(raw, "id", "call_id", "callId", "tool_call_id", "toolCallId")
	return models.ToolCall{
		ID:   id,
		Type: "function",
		Function: models.ToolFunction{
			Name:      name,
			Arguments: arguments,
		},
	}, true
}

func inferToolCallFromMap(raw map[string]any) (string, map[string]any, bool) {
	if patch := firstStringValue(raw, "patch"); patch != "" {
		return "apply_patch", raw, true
	}
	if path := firstStringValue(raw, "path"); path != "" {
		if _, ok := raw["content"]; ok {
			return "write_file", raw, true
		}
		if _, ok := raw["cmd"]; !ok {
			if _, ok := raw["command"]; !ok {
				return "read_file", raw, true
			}
		}
	}
	if cmd := firstStringValue(raw, "cmd", "command"); cmd != "" {
		return "exec_command", raw, true
	}
	return "", raw, false
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
			calls[i].ID = fmt.Sprintf("call_%d", i)
		}
	}
}
