package geminidirect

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strings"
	"sync"

	"github.com/0xc0de1ab/pangaea/internal/compat"
)

type MCPStdioServerConfig struct {
	Name    string            `json:"name,omitempty"`
	Command string            `json:"command"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
	Dir     string            `json:"dir,omitempty"`
	CWD     string            `json:"cwd,omitempty"`
}

type MCPStdioDispatcher struct {
	configs []MCPStdioServerConfig

	mu          sync.Mutex
	initialized bool
	servers     map[string]*mcpStdioServer
	tools       map[string]mcpToolRef
}

type mcpToolRef struct {
	server     *mcpStdioServer
	serverName string
	toolName   string
}

type mcpStdioServer struct {
	config MCPStdioServerConfig

	mu      sync.Mutex
	nextID  int
	pending map[int]chan mcpRPCResponse
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	tools   map[string]mcpToolDefinition
}

type mcpRPCResponse struct {
	JSONRPC string          `json:"jsonrpc,omitempty"`
	ID      int             `json:"id,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *mcpRPCError    `json:"error,omitempty"`
}

type mcpRPCError struct {
	Code    int    `json:"code,omitempty"`
	Message string `json:"message,omitempty"`
}

type mcpToolsListResult struct {
	Tools []mcpToolDefinition `json:"tools,omitempty"`
}

type mcpToolDefinition struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	InputSchema map[string]any `json:"inputSchema,omitempty"`
}

type mcpToolCallResult struct {
	Content []mcpToolContent `json:"content,omitempty"`
	IsError bool             `json:"isError,omitempty"`
}

type mcpToolContent struct {
	Type string `json:"type,omitempty"`
	Text string `json:"text,omitempty"`
}

func NewMCPStdioDispatcher(configs []MCPStdioServerConfig) (*MCPStdioDispatcher, error) {
	normalized := make([]MCPStdioServerConfig, 0, len(configs))
	for _, cfg := range configs {
		cfg.Name = strings.TrimSpace(cfg.Name)
		cfg.Command = strings.TrimSpace(cfg.Command)
		if cfg.Command == "" {
			return nil, fmt.Errorf("%w: mcp stdio server command is required", ErrConfig)
		}
		if cfg.Name == "" {
			cfg.Name = cfg.Command
		}
		normalized = append(normalized, cfg)
	}
	if len(normalized) == 0 {
		return nil, fmt.Errorf("%w: at least one mcp stdio server is required", ErrConfig)
	}
	return &MCPStdioDispatcher{configs: normalized}, nil
}

func NewMCPStdioDispatcherFromJSON(raw string) (*MCPStdioDispatcher, error) {
	configs, err := parseMCPStdioServersJSON(raw)
	if err != nil {
		return nil, err
	}
	return NewMCPStdioDispatcher(configs)
}

func parseMCPStdioServersJSON(raw string) ([]MCPStdioServerConfig, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, fmt.Errorf("%w: mcp servers json is empty", ErrConfig)
	}
	var configs []MCPStdioServerConfig
	if err := json.Unmarshal([]byte(raw), &configs); err == nil {
		return configs, nil
	}
	var root struct {
		Servers    []MCPStdioServerConfig          `json:"servers,omitempty"`
		MCPServers map[string]MCPStdioServerConfig `json:"mcpServers,omitempty"`
	}
	if err := json.Unmarshal([]byte(raw), &root); err != nil {
		return nil, fmt.Errorf("%w: decode mcp servers json: %v", ErrConfig, err)
	}
	if len(root.Servers) > 0 {
		configs = append(configs, root.Servers...)
	}
	for name, cfg := range root.MCPServers {
		if strings.TrimSpace(cfg.Name) == "" {
			cfg.Name = name
		}
		configs = append(configs, cfg)
	}
	if len(configs) == 0 {
		return nil, fmt.Errorf("%w: mcp servers json did not define servers", ErrConfig)
	}
	return configs, nil
}

func (d *MCPStdioDispatcher) ToolDefinitions(ctx context.Context) ([]compat.ToolDefinition, error) {
	if d == nil {
		return nil, nil
	}
	if err := d.ensureInitialized(ctx); err != nil {
		return nil, err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	names := make([]string, 0, len(d.tools))
	for name := range d.tools {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]compat.ToolDefinition, 0, len(names))
	for _, name := range names {
		ref := d.tools[name]
		tool, ok := ref.server.cachedTool(name)
		if !ok {
			continue
		}
		out = append(out, compat.ToolDefinition{
			Name:        name,
			Description: tool.Description,
			Parameters:  schemaWithWaitForPrevious(tool.InputSchema),
			Source:      "mcp:" + ref.serverName,
		})
	}
	return out, nil
}

func (d *MCPStdioDispatcher) DispatchTool(ctx context.Context, call compat.ToolCall) (compat.Message, error) {
	if d == nil {
		return compat.Message{}, fmt.Errorf("%w: mcp dispatcher is nil", ErrConfig)
	}
	if err := d.ensureInitialized(ctx); err != nil {
		return compat.Message{}, err
	}
	d.mu.Lock()
	ref, ok := d.tools[strings.TrimSpace(call.Name)]
	d.mu.Unlock()
	if !ok {
		return compat.Message{}, fmt.Errorf("%w: unsupported mcp tool %q", ErrConfig, call.Name)
	}
	var args map[string]any
	if strings.TrimSpace(call.Arguments) != "" {
		if err := json.Unmarshal([]byte(call.Arguments), &args); err != nil {
			return compat.Message{}, fmt.Errorf("%w: decode tool arguments: %v", ErrConfig, err)
		}
	}
	if args == nil {
		args = map[string]any{}
	}
	delete(args, "wait_for_previous")
	raw, err := ref.server.call(ctx, "tools/call", map[string]any{
		"name":      ref.toolName,
		"arguments": args,
	})
	if err != nil {
		return compat.Message{}, err
	}
	var result mcpToolCallResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return compat.Message{}, fmt.Errorf("%w: decode mcp tool result: %v", ErrConfig, err)
	}
	output := mcpToolOutputText(result.Content)
	payload := map[string]any{"output": output}
	if result.IsError {
		payload["error"] = true
	}
	text, err := json.Marshal(payload)
	if err != nil {
		return compat.Message{}, err
	}
	return compat.Message{
		Role:       compat.MessageRoleTool,
		Name:       call.Name,
		ToolCallID: call.ID,
		Content: []compat.ContentPart{{
			Type: compat.ContentPartText,
			Text: string(text),
		}},
	}, nil
}

func (d *MCPStdioDispatcher) Close() error {
	if d == nil {
		return nil
	}
	d.mu.Lock()
	servers := make([]*mcpStdioServer, 0, len(d.servers))
	for _, server := range d.servers {
		servers = append(servers, server)
	}
	d.mu.Unlock()
	var out error
	for _, server := range servers {
		if err := server.close(); err != nil && out == nil {
			out = err
		}
	}
	return out
}

func (d *MCPStdioDispatcher) ensureInitialized(ctx context.Context) error {
	d.mu.Lock()
	if d.initialized {
		d.mu.Unlock()
		return nil
	}
	d.servers = make(map[string]*mcpStdioServer, len(d.configs))
	d.tools = make(map[string]mcpToolRef)
	configs := append([]MCPStdioServerConfig(nil), d.configs...)
	d.mu.Unlock()

	servers := make(map[string]*mcpStdioServer, len(configs))
	tools := map[string]mcpToolRef{}
	for _, cfg := range configs {
		server := &mcpStdioServer{config: cfg, pending: map[int]chan mcpRPCResponse{}, tools: map[string]mcpToolDefinition{}}
		if err := server.start(ctx); err != nil {
			return err
		}
		if err := server.initialize(ctx); err != nil {
			_ = server.close()
			return err
		}
		listed, err := server.listTools(ctx)
		if err != nil {
			_ = server.close()
			return err
		}
		serverName := sanitizeMCPName(cfg.Name)
		servers[serverName] = server
		for _, tool := range listed {
			if strings.TrimSpace(tool.Name) == "" {
				continue
			}
			fullName := "mcp_" + serverName + "_" + sanitizeMCPName(tool.Name)
			server.rememberTool(fullName, tool)
			tools[fullName] = mcpToolRef{
				server:     server,
				serverName: serverName,
				toolName:   tool.Name,
			}
		}
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.initialized {
		for _, server := range servers {
			_ = server.close()
		}
		return nil
	}
	d.servers = servers
	d.tools = tools
	d.initialized = true
	return nil
}

func (s *mcpStdioServer) start(_ context.Context) error {
	if s == nil {
		return ErrConfig
	}
	dir := strings.TrimSpace(s.config.Dir)
	if dir == "" {
		dir = strings.TrimSpace(s.config.CWD)
	}
	cmd := exec.Command(s.config.Command, s.config.Args...)
	cmd.Dir = dir
	cmd.Env = os.Environ()
	for _, key := range sortedEnvKeys(s.config.Env) {
		cmd.Env = append(cmd.Env, key+"="+s.config.Env[key])
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	s.cmd = cmd
	s.stdin = stdin
	go s.readLoop(stdout)
	go io.Copy(io.Discard, stderr)
	return nil
}

func (s *mcpStdioServer) initialize(ctx context.Context) error {
	if _, err := s.call(ctx, "initialize", map[string]any{
		"protocolVersion": "2025-06-18",
		"capabilities":    map[string]any{},
		"clientInfo": map[string]any{
			"name":    "pangaea-gemini-direct",
			"version": "0.0.0",
		},
	}); err != nil {
		return err
	}
	return s.notify("notifications/initialized", map[string]any{})
}

func (s *mcpStdioServer) listTools(ctx context.Context) ([]mcpToolDefinition, error) {
	raw, err := s.call(ctx, "tools/list", map[string]any{})
	if err != nil {
		return nil, err
	}
	var result mcpToolsListResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("%w: decode mcp tools/list: %v", ErrConfig, err)
	}
	return result.Tools, nil
}

func (s *mcpStdioServer) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	if s == nil || s.stdin == nil {
		return nil, ErrConfig
	}
	s.mu.Lock()
	s.nextID++
	id := s.nextID
	ch := make(chan mcpRPCResponse, 1)
	s.pending[id] = ch
	payload, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  method,
		"params":  params,
	})
	if err == nil {
		_, err = s.stdin.Write(append(payload, '\n'))
	}
	if err != nil {
		delete(s.pending, id)
		s.mu.Unlock()
		return nil, err
	}
	s.mu.Unlock()
	select {
	case <-ctx.Done():
		s.mu.Lock()
		delete(s.pending, id)
		s.mu.Unlock()
		return nil, ctx.Err()
	case response := <-ch:
		if response.Error != nil {
			return nil, fmt.Errorf("%w: mcp %s failed: %s", ErrConfig, method, response.Error.Message)
		}
		return response.Result, nil
	}
}

func (s *mcpStdioServer) notify(method string, params any) error {
	if s == nil || s.stdin == nil {
		return ErrConfig
	}
	payload, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"method":  method,
		"params":  params,
	})
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err = s.stdin.Write(append(payload, '\n'))
	return err
}

func (s *mcpStdioServer) readLoop(stdout io.Reader) {
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		var response mcpRPCResponse
		if err := json.Unmarshal(line, &response); err != nil {
			continue
		}
		s.mu.Lock()
		ch := s.pending[response.ID]
		delete(s.pending, response.ID)
		s.mu.Unlock()
		if ch != nil {
			ch <- response
		}
	}
}

func (s *mcpStdioServer) close() error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	stdin := s.stdin
	cmd := s.cmd
	s.stdin = nil
	s.cmd = nil
	for id, ch := range s.pending {
		delete(s.pending, id)
		close(ch)
	}
	s.mu.Unlock()
	if stdin != nil {
		_ = stdin.Close()
	}
	if cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}
	return nil
}

func (s *mcpStdioServer) rememberTool(name string, tool mcpToolDefinition) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.config.Env == nil {
		s.config.Env = map[string]string{}
	}
	if s.pending == nil {
		s.pending = map[int]chan mcpRPCResponse{}
	}
	if s.tools == nil {
		s.tools = map[string]mcpToolDefinition{}
	}
	s.tools[name] = tool
}

func (s *mcpStdioServer) cachedTool(name string) (mcpToolDefinition, bool) {
	if s == nil {
		return mcpToolDefinition{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tool, ok := s.tools[name]
	return tool, ok
}

func schemaWithWaitForPrevious(schema map[string]any) map[string]any {
	out := cloneMap(schema)
	if out == nil {
		out = map[string]any{"type": "object"}
	}
	properties, _ := out["properties"].(map[string]any)
	if properties == nil {
		properties = map[string]any{}
		out["properties"] = properties
	}
	if _, exists := properties["wait_for_previous"]; !exists {
		properties["wait_for_previous"] = map[string]any{
			"type":        "boolean",
			"description": "Set to true to wait for all previously requested tools in this turn to complete before starting. Set to false (or omit) to run in parallel. Use true when this tool depends on the output of previous tools.",
		}
	}
	if _, exists := out["type"]; !exists {
		out["type"] = "object"
	}
	return out
}

func mcpToolOutputText(content []mcpToolContent) string {
	parts := make([]string, 0, len(content))
	for _, part := range content {
		if strings.EqualFold(part.Type, "text") && part.Text != "" {
			parts = append(parts, part.Text)
		}
	}
	return strings.Join(parts, "\n")
}

func sanitizeMCPName(value string) string {
	value = strings.TrimSpace(value)
	value = strings.ReplaceAll(value, " ", "_")
	return value
}

func sortedEnvKeys(env map[string]string) []string {
	keys := make([]string, 0, len(env))
	for key := range env {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
