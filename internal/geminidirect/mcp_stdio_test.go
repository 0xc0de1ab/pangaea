package geminidirect

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/0xc0de1ab/pangaea/internal/compat"
)

func TestMCPStdioDispatcherListsAndCallsTools(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	dispatcher, err := NewMCPStdioDispatcher([]MCPStdioServerConfig{{
		Name:    "pangaea-fixture",
		Command: os.Args[0],
		Args:    []string{"-test.run", "TestMCPStdioHelperProcess", "--"},
		Env:     map[string]string{"PANGAEA_GEMINIDIRECT_MCP_HELPER": "1"},
	}})
	if err != nil {
		t.Fatalf("NewMCPStdioDispatcher: %v", err)
	}
	defer dispatcher.Close()
	defs, err := dispatcher.ToolDefinitions(ctx)
	if err != nil {
		t.Fatalf("ToolDefinitions: %v", err)
	}
	if len(defs) != 1 || defs[0].Name != "mcp_pangaea-fixture_fixture_echo" {
		t.Fatalf("tool definitions = %#v", defs)
	}
	properties, _ := defs[0].Parameters["properties"].(map[string]any)
	if _, ok := properties["wait_for_previous"]; !ok {
		t.Fatalf("wait_for_previous missing from schema: %#v", defs[0].Parameters)
	}
	msg, err := dispatcher.DispatchTool(ctx, compat.ToolCall{
		ID:        "call-1",
		Type:      compat.ToolCallFunction,
		Name:      "mcp_pangaea-fixture_fixture_echo",
		Arguments: `{"text":"mcp-ok","wait_for_previous":true}`,
	})
	if err != nil {
		t.Fatalf("DispatchTool: %v", err)
	}
	if msg.ToolCallID != "call-1" || msg.Name != "mcp_pangaea-fixture_fixture_echo" {
		t.Fatalf("tool message metadata = %#v", msg)
	}
	if len(msg.Content) != 1 || !strings.Contains(msg.Content[0].Text, "fixture_echo:mcp-ok") {
		t.Fatalf("tool message content = %#v", msg.Content)
	}
}

func TestMCPStdioHelperProcess(t *testing.T) {
	if os.Getenv("PANGAEA_GEMINIDIRECT_MCP_HELPER") != "1" {
		return
	}
	runMCPStdioHelper()
	os.Exit(0)
}

func runMCPStdioHelper() {
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var req struct {
			ID     int             `json:"id,omitempty"`
			Method string          `json:"method,omitempty"`
			Params json.RawMessage `json:"params,omitempty"`
		}
		if err := json.Unmarshal([]byte(line), &req); err != nil {
			continue
		}
		switch req.Method {
		case "initialize":
			sendMCPHelper(req.ID, map[string]any{
				"protocolVersion": "2025-06-18",
				"capabilities":    map[string]any{"tools": map[string]any{}},
				"serverInfo":      map[string]any{"name": "pangaea-fixture-mcp", "version": "0.0.0"},
			})
		case "tools/list":
			sendMCPHelper(req.ID, map[string]any{
				"tools": []map[string]any{{
					"name":        "fixture_echo",
					"description": "Echo fixture input for Pangaea capture tests.",
					"inputSchema": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"text": map[string]any{"type": "string"},
						},
						"required": []string{"text"},
					},
				}},
			})
		case "tools/call":
			var params struct {
				Arguments map[string]any `json:"arguments,omitempty"`
			}
			_ = json.Unmarshal(req.Params, &params)
			text, _ := params.Arguments["text"].(string)
			sendMCPHelper(req.ID, map[string]any{
				"content": []map[string]any{{"type": "text", "text": "fixture_echo:" + text}},
				"isError": false,
			})
		default:
			if req.ID != 0 {
				sendMCPHelper(req.ID, map[string]any{})
			}
		}
	}
}

func sendMCPHelper(id int, result any) {
	_ = json.NewEncoder(os.Stdout).Encode(map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"result":  result,
	})
}
