package cursoracp

import (
	"encoding/json"
	"fmt"
	"strings"
)

// parseMCPServersJSON converts CLI/settings-style MCP JSON into a JSON array value
// suitable for ACP session/new "mcpServers". Accepts:
//   - a JSON array of server descriptors
//   - an object with an "mcpServers" array
//   - an object with "mcpServers" as a map (e.g. Gemini settings.json), expanded to objects with a "name" field.
func parseMCPServersJSON(raw string) ([]any, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	if strings.HasPrefix(raw, "[") {
		var arr []any
		if err := json.Unmarshal([]byte(raw), &arr); err != nil {
			return nil, fmt.Errorf("%w: parse mcp servers array: %v", ErrConfig, err)
		}
		return arr, nil
	}
	var wrap struct {
		MCPServers json.RawMessage `json:"mcpServers"`
	}
	if err := json.Unmarshal([]byte(raw), &wrap); err != nil {
		return nil, fmt.Errorf("%w: parse mcp servers object: %v", ErrConfig, err)
	}
	if len(wrap.MCPServers) == 0 {
		return nil, nil
	}
	var arr []any
	if err := json.Unmarshal(wrap.MCPServers, &arr); err == nil {
		return arr, nil
	}
	var m map[string]any
	if err := json.Unmarshal(wrap.MCPServers, &m); err != nil {
		return nil, fmt.Errorf("%w: mcpServers must be array or object map: %v", ErrConfig, err)
	}
	out := make([]any, 0, len(m))
	for name, cfg := range m {
		cm, ok := cfg.(map[string]any)
		if !ok {
			continue
		}
		entry := make(map[string]any, len(cm)+2)
		for k, v := range cm {
			entry[k] = v
		}
		if _, ok := entry["name"]; !ok && strings.TrimSpace(name) != "" {
			entry["name"] = name
		}
		out = append(out, entry)
	}
	return out, nil
}
