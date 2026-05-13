package copilotacp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
)

type acpClient struct {
	cmd *exec.Cmd

	stdin io.WriteCloser
	mu    sync.Mutex

	pendingMu sync.Mutex
	pending   map[string]chan json.RawMessage

	onNotify func(json.RawMessage)
}

func newACPClient(ctx context.Context, copilotPath string, extraEnv []string, cwd string) (*acpClient, error) {
	copilotPath = strings.TrimSpace(copilotPath)
	if copilotPath == "" {
		copilotPath = "copilot"
	}
	cmd := exec.CommandContext(ctx, copilotPath, "--acp", "--stdio")
	if cwd != "" {
		cmd.Dir = cwd
	}
	cmd.Env = append(os.Environ(), extraEnv...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return nil, err
	}
	cmd.Stderr = os.Stderr

	c := &acpClient{
		cmd:     cmd,
		stdin:   stdin,
		pending: make(map[string]chan json.RawMessage),
	}
	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		return nil, err
	}
	go c.readLoop(stdout)
	return c, nil
}

func (c *acpClient) Close() error {
	if c == nil {
		return nil
	}
	_ = c.stdin.Close()
	if c.cmd != nil {
		if c.cmd.Process != nil {
			_ = c.cmd.Process.Kill()
		}
		_ = c.cmd.Wait()
	}
	return nil
}

func (c *acpClient) setNotifyHandler(fn func(json.RawMessage)) {
	c.onNotify = fn
}

func (c *acpClient) readLoop(r io.Reader) {
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		line := json.RawMessage(sc.Bytes())
		var probe struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Result json.RawMessage `json:"result"`
			Error  json.RawMessage `json:"error"`
		}
		if err := json.Unmarshal(line, &probe); err != nil {
			continue
		}
		idKey := normalizeRPCID(probe.ID)
		if idKey != "" && (len(probe.Result) > 0 || len(probe.Error) > 0) {
			c.pendingMu.Lock()
			ch := c.pending[idKey]
			delete(c.pending, idKey)
			c.pendingMu.Unlock()
			if ch != nil {
				select {
				case ch <- line:
				default:
				}
			}
			continue
		}
		if probe.Method != "" && idKey != "" {
			c.handleInboundRequest(line)
			continue
		}
		if probe.Method != "" && c.onNotify != nil {
			c.onNotify(line)
		}
	}
}

func (c *acpClient) handleInboundRequest(line json.RawMessage) {
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(line, &envelope); err != nil {
		return
	}
	idRaw, ok := envelope["id"]
	if !ok || len(idRaw) == 0 {
		return
	}
	resp := map[string]any{
		"jsonrpc": "2.0",
		"id":      json.RawMessage(idRaw),
		"result": map[string]any{
			"outcome": map[string]any{
				"outcome":  "selected",
				"optionId": "allow-once",
			},
		},
	}
	_ = c.writeJSON(resp)
}

func (c *acpClient) writeJSON(v any) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	payload, err := json.Marshal(v)
	if err != nil {
		return err
	}
	payload = append(payload, '\n')
	_, err = c.stdin.Write(payload)
	return err
}

func (c *acpClient) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	id := nextRPCID()
	ch := make(chan json.RawMessage, 1)
	key := normalizeRPCID(mustJSONMarshal(id))
	c.pendingMu.Lock()
	c.pending[key] = ch
	c.pendingMu.Unlock()
	defer func() {
		c.pendingMu.Lock()
		delete(c.pending, key)
		c.pendingMu.Unlock()
	}()

	req := map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params}
	if err := c.writeJSON(req); err != nil {
		return nil, err
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case resp := <-ch:
		var envelope struct {
			Error *struct {
				Code    int             `json:"code"`
				Message string          `json:"message"`
				Data    json.RawMessage `json:"data,omitempty"`
			} `json:"error"`
			Result json.RawMessage `json:"result"`
		}
		if err := json.Unmarshal(resp, &envelope); err != nil {
			return nil, err
		}
		if envelope.Error != nil {
			return nil, fmt.Errorf("acp error %d: %s", envelope.Error.Code, envelope.Error.Message)
		}
		return envelope.Result, nil
	}
}

var acpRPCSeq atomic.Uint64

func nextRPCID() uint64 {
	return acpRPCSeq.Add(1)
}

func mustJSONMarshal(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return json.RawMessage(b)
}

func normalizeRPCID(raw json.RawMessage) string {
	raw = json.RawMessage(strings.TrimSpace(string(raw)))
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var n json.Number
	if err := json.Unmarshal(raw, &n); err == nil {
		return string(n)
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	return strings.Trim(string(raw), `"`)
}

func handshake(ctx context.Context, c *acpClient, cwd string) (string, error) {
	if _, err := c.call(ctx, "initialize", map[string]any{
		"protocolVersion": 1,
		"clientCapabilities": map[string]any{
			"fs":       map[string]bool{"readTextFile": false, "writeTextFile": false},
			"terminal": false,
		},
		"clientInfo": map[string]string{"name": "pangaea-copilot-acp", "version": "0.1"},
	}); err != nil {
		return "", fmt.Errorf("initialize: %w", err)
	}
	if methodID := strings.TrimSpace(os.Getenv("PANGAEA_COPILOT_ACP_AUTH_METHOD")); methodID != "" {
		if _, err := c.call(ctx, "authenticate", map[string]string{"methodId": methodID}); err != nil {
			return "", fmt.Errorf("authenticate: %w", err)
		}
	}
	result, err := c.call(ctx, "session/new", map[string]any{
		"cwd":        cwd,
		"mcpServers": []any{},
	})
	if err != nil {
		return "", fmt.Errorf("session/new: %w", err)
	}
	var parsed struct {
		SessionID string `json:"sessionId"`
	}
	if err := json.Unmarshal(result, &parsed); err != nil {
		return "", fmt.Errorf("session/new decode: %w", err)
	}
	if strings.TrimSpace(parsed.SessionID) == "" {
		return "", errors.New("session/new missing sessionId")
	}
	return parsed.SessionID, nil
}

func sessionPrompt(ctx context.Context, c *acpClient, sessionID, userText string) (stopReason string, reply string, err error) {
	var notifyBuf strings.Builder
	c.setNotifyHandler(func(raw json.RawMessage) {
		if chunk := extractAgentChunk(raw); chunk != "" {
			notifyBuf.WriteString(chunk)
		}
	})
	defer c.setNotifyHandler(nil)

	params := map[string]any{
		"sessionId": sessionID,
		"prompt": []map[string]string{
			{"type": "text", "text": userText},
		},
	}
	result, err := c.call(ctx, "session/prompt", params)
	if err != nil {
		return "", notifyBuf.String(), err
	}
	var envelope struct {
		StopReason string `json:"stopReason"`
	}
	_ = json.Unmarshal(result, &envelope)
	finalText := notifyBuf.String()
	if alt := extractPromptReplyText(result); alt != "" && finalText == "" {
		finalText = alt
	}
	return envelope.StopReason, finalText, nil
}

func extractAgentChunk(raw json.RawMessage) string {
	var root map[string]any
	if err := json.Unmarshal(raw, &root); err != nil {
		return ""
	}
	params, _ := root["params"].(map[string]any)
	if params == nil {
		return ""
	}
	upd, _ := params["update"].(map[string]any)
	if upd == nil {
		return ""
	}
	if upd["sessionUpdate"] != "agent_message_chunk" {
		return ""
	}
	content, _ := upd["content"].(map[string]any)
	if content == nil {
		return ""
	}
	text, _ := content["text"].(string)
	return text
}

func extractPromptReplyText(result json.RawMessage) string {
	var root map[string]any
	if err := json.Unmarshal(result, &root); err != nil {
		return ""
	}
	if msg, ok := root["message"].(map[string]any); ok {
		if text, ok := msg["text"].(string); ok {
			return text
		}
	}
	return ""
}
