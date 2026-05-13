package cursoracp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"

	"github.com/0xc0de1ab/pangaea/internal/provider"
)

var cursorANSISequence = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]`)

func discoverCursorModels(ctx context.Context, agentPath string, extraEnv []string, workingDir string, capabilities []provider.Capability) ([]provider.Model, error) {
	commands := [][]string{
		{"--list-models"},
		{"models"},
		{"models", "--json"},
		{"models", "--format", "json"},
	}
	var lastErr error
	sawSuccessfulCommand := false
	for _, args := range commands {
		raw, err := runCursorModelCommand(ctx, agentPath, extraEnv, workingDir, args...)
		if err != nil {
			lastErr = err
			continue
		}
		sawSuccessfulCommand = true
		models := parseCursorModels(raw, capabilities)
		if len(models) > 0 {
			return models, nil
		}
	}
	if sawSuccessfulCommand {
		return nil, fmt.Errorf("%w: cursor model discovery returned no parseable models", ErrConfig)
	}
	if lastErr != nil {
		return nil, lastErr
	}
	return nil, fmt.Errorf("%w: cursor model discovery returned no models", ErrConfig)
}

func runCursorModelCommand(ctx context.Context, agentPath string, extraEnv []string, workingDir string, args ...string) ([]byte, error) {
	if strings.TrimSpace(agentPath) == "" {
		return nil, fmt.Errorf("%w: cursor agent path is empty", ErrConfig)
	}
	cmd := exec.CommandContext(ctx, agentPath, args...)
	if strings.TrimSpace(workingDir) != "" {
		cmd.Dir = workingDir
	}
	cmd.Env = append(os.Environ(), extraEnv...)
	raw, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("cursor model discovery %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(raw)))
	}
	return raw, nil
}

func parseCursorModels(raw []byte, capabilities []provider.Capability) []provider.Model {
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil
	}
	if models := parseCursorModelJSON(raw, capabilities); len(models) > 0 {
		return models
	}
	return parseCursorModelText(string(raw), capabilities)
}

func parseCursorModelJSON(raw []byte, capabilities []provider.Capability) []provider.Model {
	var doc any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&doc); err != nil {
		return nil
	}
	return cursorModelsFromJSONValue(doc, capabilities)
}

func cursorModelsFromJSONValue(value any, capabilities []provider.Capability) []provider.Model {
	switch v := value.(type) {
	case []any:
		return cursorModelsFromJSONArray(v, capabilities)
	case map[string]any:
		for _, key := range []string{"models", "availableModels", "available_models", "data", "items"} {
			if raw, ok := v[key]; ok {
				if models := cursorModelsFromJSONValue(raw, capabilities); len(models) > 0 {
					return models
				}
			}
		}
		if model, ok := cursorModelFromJSONObject(v, capabilities); ok {
			return []provider.Model{model}
		}
	case string:
		if id := normalizeCursorModelID(v); id != "" {
			return []provider.Model{newCursorModel(id, "", capabilities)}
		}
	}
	return nil
}

func cursorModelsFromJSONArray(items []any, capabilities []provider.Capability) []provider.Model {
	models := make([]provider.Model, 0, len(items))
	seen := map[string]struct{}{}
	for _, item := range items {
		var model provider.Model
		var ok bool
		switch v := item.(type) {
		case string:
			id := normalizeCursorModelID(v)
			if id == "" {
				continue
			}
			model, ok = newCursorModel(id, "", capabilities), true
		case map[string]any:
			model, ok = cursorModelFromJSONObject(v, capabilities)
		}
		if !ok || model.ID == "" {
			continue
		}
		if _, exists := seen[model.ID]; exists {
			continue
		}
		seen[model.ID] = struct{}{}
		models = append(models, model)
	}
	return models
}

func cursorModelFromJSONObject(obj map[string]any, capabilities []provider.Capability) (provider.Model, bool) {
	id := firstCursorJSONString(obj, "id", "model", "modelId", "model_id", "name", "value")
	id = normalizeCursorModelID(id)
	if id == "" {
		return provider.Model{}, false
	}
	display := firstCursorJSONString(obj, "displayName", "display_name", "label", "title", "name")
	return newCursorModel(id, display, capabilities), true
}

func parseCursorModelText(raw string, capabilities []provider.Capability) []provider.Model {
	lines := strings.Split(cursorANSISequence.ReplaceAllString(raw, ""), "\n")
	models := make([]provider.Model, 0, len(lines))
	seen := map[string]struct{}{}
	for _, line := range lines {
		id, display := cursorModelFromTextLine(line)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		models = append(models, newCursorModel(id, display, capabilities))
	}
	return models
}

func cursorModelFromTextLine(line string) (string, string) {
	line = strings.TrimSpace(line)
	if line == "" {
		return "", ""
	}
	line = strings.TrimLeft(line, "*>-•✓✔●○ \t")
	line = strings.TrimSpace(line)
	lower := strings.ToLower(line)
	if strings.HasPrefix(lower, "available ") || strings.HasPrefix(lower, "model ") || lower == "models" {
		return "", ""
	}
	if start := strings.Index(line, "`"); start >= 0 {
		if end := strings.Index(line[start+1:], "`"); end >= 0 {
			if id := normalizeCursorModelID(line[start+1 : start+1+end]); id != "" {
				display := normalizeCursorModelDisplay(line[start+1+end+1:], id)
				return id, display
			}
		}
	}
	fields := strings.Fields(line)
	if len(fields) == 0 {
		return "", ""
	}
	candidate := strings.Trim(fields[0], "`'\"(),:")
	id := normalizeCursorModelID(candidate)
	if id == "" {
		return "", ""
	}
	display := normalizeCursorModelDisplay(strings.TrimPrefix(line, fields[0]), id)
	return id, display
}

func normalizeCursorModelDisplay(value string, id string) string {
	value = strings.TrimSpace(value)
	value = strings.TrimLeft(value, "-:— \t")
	value = strings.TrimSpace(value)
	value = strings.Trim(value, "`'\"")
	if value == "" || value == id {
		return ""
	}
	return value
}

func normalizeCursorModelID(value string) string {
	value = strings.TrimSpace(value)
	value = strings.TrimPrefix(value, "models/")
	value = strings.Trim(value, "`'\"")
	if value == "" || len(value) > 160 || strings.ContainsAny(value, " \t\r\n") {
		return ""
	}
	lower := strings.ToLower(value)
	if lower == "id" || lower == "model" || lower == "name" || lower == "default" {
		return ""
	}
	if strings.ContainsAny(value, "/\\{}<>") {
		return ""
	}
	if !looksLikeCursorModelID(lower) {
		return ""
	}
	return value
}

func looksLikeCursorModelID(lower string) bool {
	for _, marker := range []string{"gpt", "claude", "sonnet", "opus", "haiku", "gemini", "composer", "cursor", "auto", "grok", "o3", "o4"} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	for _, r := range lower {
		if r >= '0' && r <= '9' {
			return true
		}
	}
	return false
}

func newCursorModel(id string, display string, capabilities []provider.Capability) provider.Model {
	model := provider.Model{
		ID:           id,
		Capabilities: cursorModelCapabilities(capabilities),
	}
	display = strings.TrimSpace(display)
	if display != "" && display != id {
		model.Aliases = append(model.Aliases, display)
	}
	if alias := knownCursorModelAlias(id); alias != "" {
		model.Aliases = appendStringUniqueCursor(model.Aliases, alias)
	}
	return model
}

func cursorModelCapabilities(capabilities []provider.Capability) []provider.Capability {
	out := make([]provider.Capability, 0, len(capabilities))
	seen := map[provider.Capability]struct{}{}
	for _, capability := range capabilities {
		switch capability {
		case provider.CapabilityOpenAIChat, provider.CapabilityOpenAIResponses, provider.CapabilityAnthropicMessages, provider.CapabilityGeminiGenerateContent:
		default:
			continue
		}
		if _, ok := seen[capability]; ok {
			continue
		}
		seen[capability] = struct{}{}
		out = append(out, capability)
	}
	return out
}

func knownCursorModelAlias(id string) string {
	switch strings.ToLower(strings.TrimSpace(id)) {
	case "composer-2":
		return "Composer 2"
	case "composer-1.5":
		return "Composer 1.5"
	}
	return ""
}

func firstCursorJSONString(obj map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := obj[key].(string); ok && strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func appendStringUniqueCursor(values []string, value string) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return values
	}
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}
