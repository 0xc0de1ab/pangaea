package cursoracp

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestParseMCPServersJSON_Array(t *testing.T) {
	raw := `[{"name":"x","command":"mcp"}]`
	got, err := parseMCPServersJSON(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("got %#v", got)
	}
}

func TestParseMCPServersJSON_MapServers(t *testing.T) {
	raw := `{"mcpServers":{"demo":{"command":"npx","args":["-y","mcp"]}}}`
	got, err := parseMCPServersJSON(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("got %#v", got)
	}
	m, ok := got[0].(map[string]any)
	if !ok || m["name"] != "demo" {
		t.Fatalf("got %#v", got[0])
	}
}

func TestParseMCPServersJSON_Empty(t *testing.T) {
	got, err := parseMCPServersJSON("")
	if err != nil || got != nil {
		t.Fatalf("got %#v err=%v", got, err)
	}
}

func TestParseMCPServersJSON_Invalid(t *testing.T) {
	if _, err := parseMCPServersJSON("{"); err == nil {
		t.Fatal("expected error")
	}
}

func TestParseMCPServersJSON_RoundTripGeminiShape(t *testing.T) {
	settings := map[string]any{
		"mcpServers": map[string]any{
			"files": map[string]any{"command": "node", "args": []any{"m.js"}},
		},
	}
	b, err := json.Marshal(settings)
	if err != nil {
		t.Fatal(err)
	}
	got, err := parseMCPServersJSON(string(b))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatal(got)
	}
	m := got[0].(map[string]any)
	if !reflect.DeepEqual(m["args"], []any{"m.js"}) {
		t.Fatalf("args %#v", m["args"])
	}
}
