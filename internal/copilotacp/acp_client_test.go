package copilotacp

import (
	"encoding/json"
	"testing"
)

func TestExtractAgentChunk(t *testing.T) {
	raw := json.RawMessage(`{"jsonrpc":"2.0","method":"session/update","params":{"update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"hello"}}}}`)
	if got := extractAgentChunk(raw); got != "hello" {
		t.Fatalf("chunk = %q, want hello", got)
	}
}

func TestExtractPromptReplyText(t *testing.T) {
	raw := json.RawMessage(`{"stopReason":"end_turn","message":{"text":"done"}}`)
	if got := extractPromptReplyText(raw); got != "done" {
		t.Fatalf("reply = %q, want done", got)
	}
}
