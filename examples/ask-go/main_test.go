package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
)

func TestApplyAskConfigValuesHonorsPrecedence(t *testing.T) {
	stream := false
	spinner := false
	tools := false
	opts := options{
		BaseURL:            defaultBaseURL,
		APIKey:             "",
		Model:              "",
		API:                defaultAPI,
		MaxTokens:          0,
		Stream:             true,
		Spinner:            true,
		Tools:              true,
		ToolRoot:           ".",
		ToolTurns:          6,
		MarkdownTranslator: "plain",
		GlamourStyle:       "dark",
		RichCodeTheme:      "monokai",
	}
	cfg := askConfig{
		BaseURL:            "https://config.example/route/user/rule",
		APIKey:             "config-key",
		Model:              "config-model",
		API:                "chat",
		System:             "config system",
		Images:             []string{"sample.png"},
		MaxTokens:          4096,
		Stream:             &stream,
		Spinner:            &spinner,
		Tools:              &tools,
		ToolRoot:           "workspace",
		ToolTurns:          3,
		MCPServers:         []string{"/tmp/mcp-fixture"},
		MCPServersJSON:     `{"mcpServers":{}}`,
		MarkdownTranslator: "glow",
		GlamourStyle:       "notty",
		RichCodeTheme:      "dracula",
	}
	changed := map[string]bool{
		"model": true,
	}
	env := map[string]bool{
		"PANGAEA_ASK_BASE_URL": true,
	}

	applyAskConfigValues(&opts, cfg, func(name string) bool {
		return changed[name]
	}, func(names ...string) bool {
		for _, name := range names {
			if env[name] {
				return true
			}
		}
		return false
	})

	if opts.BaseURL != defaultBaseURL {
		t.Fatalf("base URL should keep env/default value, got %q", opts.BaseURL)
	}
	if opts.Model != "" {
		t.Fatalf("model should keep flag value, got %q", opts.Model)
	}
	if opts.APIKey != "config-key" {
		t.Fatalf("api key should come from config, got %q", opts.APIKey)
	}
	if opts.API != "chat" || opts.MaxTokens != 4096 || opts.Stream || opts.Spinner || opts.Tools {
		t.Fatalf("config values were not applied: %+v", opts)
	}
	if opts.System != "config system" || len(opts.ImagePaths) != 1 || opts.ImagePaths[0] != "sample.png" {
		t.Fatalf("system/images config values were not applied: %+v", opts)
	}
	if opts.ToolRoot != "workspace" || opts.ToolTurns != 3 || opts.MarkdownTranslator != "glow" || opts.GlamourStyle != "notty" {
		t.Fatalf("tool/render config values were not applied: %+v", opts)
	}
	if len(opts.MCPServers) != 1 || opts.MCPServers[0] != "/tmp/mcp-fixture" || opts.MCPServersJSON == "" {
		t.Fatalf("mcp config values were not applied: %+v", opts)
	}
	if opts.RichCodeTheme != "dracula" {
		t.Fatalf("rich code theme was not applied: %+v", opts)
	}
}

func TestLoadAskConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ask-config.json")
	if err := os.WriteFile(path, []byte(`{
		"base_url": "https://config.example/route/user/rule",
		"api_key": "config-key",
		"model": "config-model",
		"api": "responses",
		"system": "answer briefly",
		"images": ["sample.png"],
		"mcp_servers": ["/tmp/mcp-fixture"],
		"mcp_servers_json": "{\"mcpServers\":{}}",
		"max_tokens": 2048
	}`), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, loaded, err := loadAskConfig(path, true)
	if err != nil {
		t.Fatal(err)
	}
	if !loaded {
		t.Fatal("expected config to be loaded")
	}
	if cfg.BaseURL != "https://config.example/route/user/rule" || cfg.APIKey != "config-key" || cfg.Model != "config-model" || cfg.MaxTokens != 2048 {
		t.Fatalf("unexpected config: %+v", cfg)
	}
	if cfg.System != "answer briefly" || len(cfg.Images) != 1 || cfg.Images[0] != "sample.png" {
		t.Fatalf("system/images config not loaded: %+v", cfg)
	}
	if len(cfg.MCPServers) != 1 || cfg.MCPServers[0] != "/tmp/mcp-fixture" || cfg.MCPServersJSON == "" {
		t.Fatalf("mcp config not loaded: %+v", cfg)
	}
}

func TestMissingDefaultAskConfigIsIgnored(t *testing.T) {
	cfg, loaded, err := loadAskConfig(filepath.Join(t.TempDir(), "missing.json"), false)
	if err != nil {
		t.Fatal(err)
	}
	if loaded {
		t.Fatalf("missing optional config should not load: %+v", cfg)
	}
}

func TestBuildChatMessagesWithSystemAndImage(t *testing.T) {
	dir := t.TempDir()
	imagePath := filepath.Join(dir, "sample.png")
	pngBytes := []byte{
		0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a,
		0x00, 0x00, 0x00, 0x0d, 0x49, 0x48, 0x44, 0x52,
		0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
		0x08, 0x06, 0x00, 0x00, 0x00, 0x1f, 0x15, 0xc4,
		0x89, 0x00, 0x00, 0x00, 0x0a, 0x49, 0x44, 0x41,
		0x54, 0x78, 0x9c, 0x63, 0x00, 0x01, 0x00, 0x00,
		0x05, 0x00, 0x01, 0x0d, 0x0a, 0x2d, 0xb4, 0x00,
		0x00, 0x00, 0x00, 0x49, 0x45, 0x4e, 0x44, 0xae,
		0x42, 0x60, 0x82,
	}
	if err := os.WriteFile(imagePath, pngBytes, 0o644); err != nil {
		t.Fatal(err)
	}
	messages, err := buildChatMessages(options{System: "prefix", ImagePaths: []string{imagePath}}, "describe", "tools")
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 2 || messages[0]["role"] != "system" || messages[0]["content"] != "prefix\n\ntools" {
		t.Fatalf("unexpected messages: %#v", messages)
	}
	parts, ok := messages[1]["content"].([]map[string]any)
	if !ok || len(parts) != 2 {
		t.Fatalf("expected multimodal content parts, got %#v", messages[1]["content"])
	}
	image, _ := parts[1]["image_url"].(map[string]any)
	if url, _ := image["url"].(string); !strings.HasPrefix(url, "data:image/png;base64,") {
		t.Fatalf("image url not encoded as PNG data URL: %q", url)
	}
}

func TestChatToolsExposeCodexStyleTools(t *testing.T) {
	tools := chatTools()
	names := map[string]bool{}
	for _, tool := range tools {
		fn, _ := tool["function"].(map[string]any)
		name, _ := fn["name"].(string)
		names[name] = true
	}
	for _, name := range []string{"read_file", "write_file", "list_files", "search_files", "exec_command", "apply_patch"} {
		if !names[name] {
			t.Fatalf("missing tool %s in %#v", name, names)
		}
	}
}

func TestApplyMaxTokensOmitsUnsetLimit(t *testing.T) {
	payload := map[string]any{}
	applyMaxTokens(payload, "chat", 0)
	if _, ok := payload["max_tokens"]; ok {
		t.Fatalf("max_tokens should be omitted when unset: %#v", payload)
	}

	applyMaxTokens(payload, "chat", 2048)
	if payload["max_tokens"] != 2048 {
		t.Fatalf("max_tokens was not applied: %#v", payload)
	}

	responsesPayload := map[string]any{}
	applyMaxTokens(responsesPayload, "responses", 4096)
	if responsesPayload["max_output_tokens"] != 4096 {
		t.Fatalf("max_output_tokens was not applied: %#v", responsesPayload)
	}
}

func TestApplyModelOmitsUnsetModel(t *testing.T) {
	payload := map[string]any{}
	applyModel(payload, "")
	if _, ok := payload["model"]; ok {
		t.Fatalf("model should be omitted when unset: %#v", payload)
	}
	applyModel(payload, "  claude-sonnet-4-6  ")
	if payload["model"] != "claude-sonnet-4-6" {
		t.Fatalf("model should be trimmed and applied: %#v", payload)
	}
}

func TestTextToolCallFilterPreservesInvalidToolCallText(t *testing.T) {
	filter := &toolCallTextFilter{}
	visible := filter.Feed("before <tool_call>not json</tool_call> after")
	tail, err := filter.Finish()
	if err != nil {
		t.Fatal(err)
	}
	visible += tail
	if visible != "before <tool_call>not json</tool_call> after" {
		t.Fatalf("invalid text tool call should be preserved, got %q", visible)
	}
	if len(filter.ToolCalls) != 0 {
		t.Fatalf("invalid text tool call should not produce calls: %#v", filter.ToolCalls)
	}
}

func TestTextToolCallFilterExtractsValidToolCall(t *testing.T) {
	filter := &toolCallTextFilter{}
	visible := filter.Feed(`before <tool_call>{"name":"read_file","arguments":{"path":"b.html"}}</tool_call> after`)
	tail, err := filter.Finish()
	if err != nil {
		t.Fatal(err)
	}
	visible += tail
	if visible != "before  after" {
		t.Fatalf("valid text tool call should be removed from visible text, got %q", visible)
	}
	if len(filter.ToolCalls) != 1 || filter.ToolCalls[0].Function.Name != "read_file" {
		t.Fatalf("valid text tool call was not extracted: %#v", filter.ToolCalls)
	}
	if !strings.Contains(filter.ToolCalls[0].Function.Arguments, "b.html") {
		t.Fatalf("tool call arguments were not preserved: %#v", filter.ToolCalls[0])
	}
}

func TestTextToolCallFilterInfersPatchToolCall(t *testing.T) {
	filter := &toolCallTextFilter{}
	patch := strings.Join([]string{
		"*** Begin Patch",
		"--- f.html",
		"+++ f.html",
		"@@ -1 +1 @@",
		"-old",
		"+new",
		"*** End Patch",
	}, "\n")
	visible := filter.Feed(`before <tool_call>` + mustJSON(map[string]any{
		"intent": "f.html 파일에 텍스처 폴백 로직을 추가합니다.",
		"patch":  patch,
	}) + `</tool_call> after`)
	tail, err := filter.Finish()
	if err != nil {
		t.Fatal(err)
	}
	visible += tail
	if visible != "before  after" {
		t.Fatalf("inferred text tool call should be removed from visible text, got %q", visible)
	}
	if len(filter.ToolCalls) != 1 {
		t.Fatalf("expected inferred tool call, got %#v", filter.ToolCalls)
	}
	if filter.ToolCalls[0].Function.Name != "apply_patch" {
		t.Fatalf("inferred tool name = %q", filter.ToolCalls[0].Function.Name)
	}
	if !strings.Contains(filter.ToolCalls[0].Function.Arguments, "텍스처 폴백") ||
		!strings.Contains(filter.ToolCalls[0].Function.Arguments, "--- f.html") {
		t.Fatalf("inferred tool arguments were not preserved: %#v", filter.ToolCalls[0])
	}
}

func TestTextToolCallFilterSummarizesIncompleteToolCallText(t *testing.T) {
	filter := &toolCallTextFilter{}
	raw := `<tool_call>{"name":"write_file","arguments":{"path":"f.html","intent":"update texture loading","content":"partial`
	visible := filter.Feed(`before ` + raw)
	if visible != "before " {
		t.Fatalf("visible prefix = %q", visible)
	}
	tail, err := filter.Finish()
	if err != nil {
		t.Fatalf("incomplete text tool call should be preserved, not fatal: %v", err)
	}
	if !strings.Contains(tail, "도구 호출이 중간에 끊겨 적용되지 않았습니다") ||
		!strings.Contains(tail, "Write f.html") ||
		!strings.Contains(tail, "update texture loading") {
		t.Fatalf("incomplete text tool call notice = %q", tail)
	}
	if len(filter.ToolCalls) != 0 {
		t.Fatalf("incomplete text tool call should not produce calls: %#v", filter.ToolCalls)
	}
}

func TestTextToolCallFilterRemovesObservationBlock(t *testing.T) {
	filter := &toolCallTextFilter{}
	visible := filter.Feed("before <observation>\n[Tool Result: ]\n{\"ok\":true}\n</observation> after")
	tail, err := filter.Finish()
	if err != nil {
		t.Fatal(err)
	}
	if got := visible + tail; got != "before  after" {
		t.Fatalf("observation block should be hidden, got %q", got)
	}
}

func TestTextToolCallFilterRemovesBracketToolResultJSON(t *testing.T) {
	filter := &toolCallTextFilter{}
	visible := filter.Feed("before [Tool Re")
	visible += filter.Feed("sult: ]\n  {\"ok\":true}")
	visible += filter.Feed("\nafter")
	tail, err := filter.Finish()
	if err != nil {
		t.Fatal(err)
	}
	if got := visible + tail; got != "before \nafter" {
		t.Fatalf("tool result protocol text should be hidden, got %q", got)
	}
}

func TestReadChatStreamWithToolsReportsLengthFinish(t *testing.T) {
	stream := strings.Join([]string{
		`data: {"choices":[{"delta":{"content":"partial"},"finish_reason":"length"}]}`,
		`data: [DONE]`,
		``,
	}, "\n\n")
	_, _, err := readChatStreamWithTools(strings.NewReader(stream), options{MarkdownTranslator: "plain"}, nil)
	if err == nil || !strings.Contains(err.Error(), "length") {
		t.Fatalf("expected length finish error, got %v", err)
	}
}

func TestReadChatStreamWithToolsSummarizesIncompleteTextToolCall(t *testing.T) {
	chunk, err := json.Marshal(map[string]any{
		"choices": []map[string]any{{
			"delta": map[string]any{
				"content": `before <tool_call>{"name":"write_file","arguments":{"path":"f.html","intent":"update texture loading","content":"partial`,
			},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	stream := strings.Join([]string{
		`data: ` + string(chunk),
		`data: [DONE]`,
		``,
	}, "\n\n")
	content, calls, err := readChatStreamWithTools(strings.NewReader(stream), options{MarkdownTranslator: "plain"}, nil)
	if err != nil {
		t.Fatalf("incomplete text tool call should not fail stream: %v", err)
	}
	if len(calls) != 0 {
		t.Fatalf("incomplete text tool call should not produce calls: %#v", calls)
	}
	if !strings.Contains(content, "before ") ||
		!strings.Contains(content, "도구 호출이 중간에 끊겨 적용되지 않았습니다") ||
		!strings.Contains(content, "Write f.html") {
		t.Fatalf("unexpected stream content: %q", content)
	}
}

func TestReadChatStreamWithToolsRequiresDoneMarker(t *testing.T) {
	stream := `data: {"choices":[{"delta":{"content":"partial"}}]}`
	_, _, err := readChatStreamWithTools(strings.NewReader(stream), options{MarkdownTranslator: "plain"}, nil)
	if err == nil || !strings.Contains(err.Error(), "before [DONE]") {
		t.Fatalf("expected missing DONE error, got %v", err)
	}
}

func TestPrintStreamReportsSSEError(t *testing.T) {
	stream := strings.Join([]string{
		`data: {"error":{"message":"upstream broke"}}`,
		`data: [DONE]`,
		``,
	}, "\n\n")
	err := printStream(strings.NewReader(stream), options{MarkdownTranslator: "plain"}, nil)
	if err == nil || !strings.Contains(err.Error(), "upstream broke") {
		t.Fatalf("expected stream error, got %v", err)
	}
}

func TestInterruptKeys(t *testing.T) {
	if !isInterruptKey(0x03) {
		t.Fatal("ctrl+c should be treated as interrupt")
	}
	if isInterruptKey(0x1b) {
		t.Fatal("esc must be checked separately so terminal escape sequences are not mistaken for interrupts")
	}
	if isInterruptKey('a') {
		t.Fatal("regular keys should not interrupt")
	}
}

func TestNormalizeRunErrorMapsCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := normalizeRunError(ctx, context.Canceled); !errors.Is(err, errInterrupted) {
		t.Fatalf("expected interrupted error, got %v", err)
	}
	if err := normalizeRunError(context.Background(), errors.New("boom")); err == nil || errors.Is(err, errInterrupted) {
		t.Fatalf("non-cancel error should pass through, got %v", err)
	}
}

func TestTransientNoProviderMatchedDetection(t *testing.T) {
	err := &httpStatusError{StatusCode: 409, Body: `{"error":"no provider matched"}`}
	if !isTransientNoProviderMatched(err) {
		t.Fatalf("expected no provider matched 409 to be transient")
	}
	if isTransientNoProviderMatched(&httpStatusError{StatusCode: 409, Body: `{"error":"routing rule not found"}`}) {
		t.Fatalf("routing rule errors should not retry")
	}
	if isTransientNoProviderMatched(&httpStatusError{StatusCode: 401, Body: `{"error":"no provider matched"}`}) {
		t.Fatalf("auth errors should not retry")
	}
}

func TestAskHTTPClientHasNoFixedTimeout(t *testing.T) {
	client := newAskHTTPClient()
	if client.Timeout != 0 {
		t.Fatalf("ask client should rely on request context cancellation, got timeout %s", client.Timeout)
	}
}

func TestSpinnerTokenEstimateAndElapsedFormatting(t *testing.T) {
	if got := estimateOutputTokens(0); got != 0 {
		t.Fatalf("empty output should have no token estimate, got %d", got)
	}
	if got := estimateOutputTokens(1); got != 1 {
		t.Fatalf("short output should round up, got %d", got)
	}
	if got := estimateOutputTokens(17); got != 5 {
		t.Fatalf("token estimate should be byte/4 rounded up, got %d", got)
	}

	if got := formatElapsed(9 * time.Second); got != "9s" {
		t.Fatalf("elapsed seconds = %q", got)
	}
	if got := formatElapsed(65 * time.Second); got != "1:05" {
		t.Fatalf("elapsed minutes = %q", got)
	}
	if got := formatElapsed(2*time.Hour + 3*time.Minute + 4*time.Second); got != "2:03:04" {
		t.Fatalf("elapsed hours = %q", got)
	}
}

func TestSpinnerShimmerKeepsPlainTextAndAnimates(t *testing.T) {
	previousProfile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.ANSI256)
	t.Cleanup(func() {
		lipgloss.SetColorProfile(previousProfile)
	})

	first := shimmerStatusText("Thinking", 0)
	second := shimmerStatusText("Thinking", 3)
	if ansi.Strip(first) != "Thinking" {
		t.Fatalf("shimmer should preserve status text, got %q", ansi.Strip(first))
	}
	if first == second {
		t.Fatalf("shimmer frames should change")
	}
	if !strings.Contains(first, "\x1b[") {
		t.Fatalf("shimmer should render ANSI color sequences, got %q", first)
	}

	label := renderSpinnerLabel("Thinking", []string{"1s", "~3 tok"}, 1)
	if got := ansi.Strip(label); got != "Thinking · 1s · ~3 tok" {
		t.Fatalf("spinner label stripped text = %q", got)
	}
}

func TestSpinnerShimmerUsesMovingHighlightWindow(t *testing.T) {
	previousProfile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.ANSI256)
	t.Cleanup(func() {
		lipgloss.SetColorProfile(previousProfile)
	})

	frames := []string{
		shimmerStatusText("Thinking", 0),
		shimmerStatusText("Thinking", 2),
		shimmerStatusText("Thinking", 4),
	}
	for _, frame := range frames {
		if got := ansi.Strip(frame); got != "Thinking" {
			t.Fatalf("shimmer should preserve text, got %q", got)
		}
	}
	if frames[0] == frames[1] || frames[1] == frames[2] {
		t.Fatalf("shimmer highlight window should move across frames")
	}
}

func TestRenderWithRich(t *testing.T) {
	if firstAvailableExecutable("python3", "python") == "" {
		t.Skip("python is not installed")
	}
	rendered, ok := renderWithRich("### Title\n\n```go\nfmt.Println(\"hi\")\n```", options{
		MarkdownTranslator: "rich",
		RichCodeTheme:      "monokai",
	})
	if !ok {
		t.Skip("python rich is not installed")
	}
	stripped := ansi.Strip(rendered)
	if !strings.Contains(stripped, "Title") || strings.Contains(stripped, "### Title") {
		t.Fatalf("rich renderer did not translate markdown: %q", stripped)
	}
	if !strings.Contains(rendered, "\x1b[") {
		t.Fatalf("rich renderer should include ANSI styling, got %q", rendered)
	}
}

func TestExecuteToolCallExecCommand(t *testing.T) {
	root := t.TempDir()
	call := toolCall{
		ID:   "call_1",
		Type: "function",
		Function: toolFunction{
			Name:      "exec_command",
			Arguments: `{"cmd":"printf hello","intent":"verify shell tool"}`,
		},
	}
	raw := executeToolCall(call, root)
	var result map[string]any
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		t.Fatal(err)
	}
	if ok, _ := result["ok"].(bool); !ok {
		t.Fatalf("exec_command failed: %#v", result)
	}
	if stdout, _ := result["stdout"].(string); stdout != "hello" {
		t.Fatalf("stdout = %q", stdout)
	}
}

func TestExecuteToolCallExecCommandRejectsHeredocFileWrite(t *testing.T) {
	root := t.TempDir()
	call := toolCall{
		ID:   "call_1",
		Type: "function",
		Function: toolFunction{
			Name:      "exec_command",
			Arguments: `{"cmd":"cat << 'EOF' > patch.js\nconsole.log('x')\nEOF\nnode patch.js","intent":"write helper script"}`,
		},
	}
	raw := executeToolCall(call, root)
	var result map[string]any
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		t.Fatal(err)
	}
	if ok, _ := result["ok"].(bool); ok {
		t.Fatalf("heredoc file write should be rejected: %#v", result)
	}
	if errText, _ := result["error"].(string); !strings.Contains(errText, "use apply_patch") {
		t.Fatalf("error should direct model to apply_patch/write_file: %#v", result)
	}
	if _, err := os.Stat(filepath.Join(root, "patch.js")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("patch.js should not be created, stat err=%v", err)
	}
}

func TestToolRunnerCachesDuplicateReadOnlyCalls(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "b.html")
	if err := os.WriteFile(path, []byte("first"), 0o644); err != nil {
		t.Fatal(err)
	}
	runner := newToolRunner(root, options{})
	call := toolCall{
		ID:   "call_1",
		Type: "function",
		Function: toolFunction{
			Name:      "read_file",
			Arguments: `{"path":"b.html"}`,
		},
	}
	first := runner.Execute(context.Background(), call)
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	second := runner.Execute(context.Background(), call)
	if second != first {
		t.Fatalf("duplicate read should use cached result\nfirst=%s\nsecond=%s", first, second)
	}
}

func TestToolRunnerInvalidatesReadCacheAfterWrite(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "b.html")
	if err := os.WriteFile(path, []byte("first"), 0o644); err != nil {
		t.Fatal(err)
	}
	runner := newToolRunner(root, options{})
	read := toolCall{
		ID:   "call_1",
		Type: "function",
		Function: toolFunction{
			Name:      "read_file",
			Arguments: `{"path":"b.html"}`,
		},
	}
	_ = runner.Execute(context.Background(), read)
	write := toolCall{
		ID:   "call_2",
		Type: "function",
		Function: toolFunction{
			Name:      "write_file",
			Arguments: `{"path":"b.html","content":"second"}`,
		},
	}
	_ = runner.Execute(context.Background(), write)
	raw := runner.Execute(context.Background(), read)
	var result map[string]any
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		t.Fatal(err)
	}
	if content, _ := result["content"].(string); content != "second" {
		t.Fatalf("read cache was not invalidated after write: %#v", result)
	}
}

func TestToolRunnerMapsSimpleCatToReadCache(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "b.html")
	if err := os.WriteFile(path, []byte("first"), 0o644); err != nil {
		t.Fatal(err)
	}
	runner := newToolRunner(root, options{})
	read := toolCall{
		ID:   "call_1",
		Type: "function",
		Function: toolFunction{
			Name:      "read_file",
			Arguments: `{"path":"b.html"}`,
		},
	}
	first := runner.Execute(context.Background(), read)
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	cat := toolCall{
		ID:   "call_2",
		Type: "function",
		Function: toolFunction{
			Name:      "exec_command",
			Arguments: `{"cmd":"cat b.html"}`,
		},
	}
	second := runner.Execute(context.Background(), cat)
	if second != first {
		t.Fatalf("simple cat should use read_file cache\nfirst=%s\nsecond=%s", first, second)
	}
}

func TestToolRunnerPreparesSimpleCatAsReadOnly(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "b.html"), []byte("first"), 0o644); err != nil {
		t.Fatal(err)
	}
	runner := newToolRunner(root, options{})
	call := toolCall{
		ID:   "call_1",
		Type: "function",
		Function: toolFunction{
			Name:      "exec_command",
			Arguments: `{"cmd":"cat b.html","intent":"inspect file"}`,
		},
	}
	prepared := runner.prepare(context.Background(), call)
	if !prepared.ReadOnly {
		t.Fatalf("simple cat should be read-only: %#v", prepared)
	}
	if prepared.Name != "read_file" {
		t.Fatalf("simple cat should be normalized to read_file, got %q", prepared.Name)
	}
	if got := firstStringArg(prepared.Args, "path"); got != "b.html" {
		t.Fatalf("normalized path = %q", got)
	}
}

func TestToolRunnerExecuteCallsPreservesOrderAndIDs(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("alpha"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "b.txt"), []byte("beta"), 0o644); err != nil {
		t.Fatal(err)
	}
	runner := newToolRunner(root, options{})
	calls := []toolCall{
		{ID: "call_a", Type: "function", Function: toolFunction{Name: "read_file", Arguments: `{"path":"a.txt"}`}},
		{ID: "call_b", Type: "function", Function: toolFunction{Name: "read_file", Arguments: `{"path":"b.txt"}`}},
	}
	results := runner.ExecuteCalls(context.Background(), calls)
	if len(results) != 2 {
		t.Fatalf("results len = %d", len(results))
	}
	if results[0].Call.ID != "call_a" || results[1].Call.ID != "call_b" {
		t.Fatalf("tool call IDs/order not preserved: %#v", results)
	}
	var first map[string]any
	if err := json.Unmarshal([]byte(results[0].Content), &first); err != nil {
		t.Fatal(err)
	}
	if content, _ := first["content"].(string); content != "alpha" {
		t.Fatalf("first content = %q", content)
	}
}

func TestToolLoopDetectorStopsRepeatedBatchWithoutMutation(t *testing.T) {
	detector := newToolLoopDetector(3)
	calls := []toolCall{
		{ID: "call_1", Type: "function", Function: toolFunction{Name: "read_file", Arguments: `{"path":"f.html","intent":"first"}`}},
	}
	if err := detector.Observe(calls, 0); err != nil {
		t.Fatalf("first observation failed: %v", err)
	}
	if err := detector.Observe(calls, 0); err != nil {
		t.Fatalf("second observation failed: %v", err)
	}
	if err := detector.Observe(calls, 0); err == nil {
		t.Fatal("expected repeated identical batch to be rejected")
	}
}

func TestToolLoopDetectorResetsAfterGenerationChange(t *testing.T) {
	detector := newToolLoopDetector(3)
	calls := []toolCall{
		{ID: "call_1", Type: "function", Function: toolFunction{Name: "read_file", Arguments: `{"path":"f.html"}`}},
	}
	if err := detector.Observe(calls, 0); err != nil {
		t.Fatalf("first observation failed: %v", err)
	}
	if err := detector.Observe(calls, 0); err != nil {
		t.Fatalf("second observation failed: %v", err)
	}
	if err := detector.Observe(calls, 1); err != nil {
		t.Fatalf("generation change should reset repeated batch tracking: %v", err)
	}
	if err := detector.Observe(calls, 1); err != nil {
		t.Fatalf("second observation in new generation failed: %v", err)
	}
}

func TestToolLoopSignatureIgnoresIntentAndNormalizesCat(t *testing.T) {
	readSig := toolCallBatchSignature([]toolCall{
		{ID: "call_a", Type: "function", Function: toolFunction{Name: "read_file", Arguments: `{"path":"f.html","intent":"inspect"}`}},
	})
	catSig := toolCallBatchSignature([]toolCall{
		{ID: "call_b", Type: "function", Function: toolFunction{Name: "exec_command", Arguments: `{"cmd":"cat f.html","intent":"inspect via cat"}`}},
	})
	if readSig != catSig {
		t.Fatalf("expected read_file and simple cat signatures to match:\nread=%q\ncat=%q", readSig, catSig)
	}
}

func TestExecuteToolCallSearchFiles(t *testing.T) {
	if _, err := exec.LookPath("rg"); err != nil {
		t.Skip("rg is not installed")
	}
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "sample.txt"), []byte("alpha\nbeta\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	call := toolCall{
		ID:   "call_1",
		Type: "function",
		Function: toolFunction{
			Name:      "search_files",
			Arguments: `{"pattern":"beta","path":"."}`,
		},
	}
	raw := executeToolCall(call, root)
	var result map[string]any
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		t.Fatal(err)
	}
	if ok, _ := result["ok"].(bool); !ok {
		t.Fatalf("search_files failed: %#v", result)
	}
	encoded, _ := json.Marshal(result["matches"])
	if !strings.Contains(string(encoded), "sample.txt") {
		t.Fatalf("matches did not include sample.txt: %s", encoded)
	}
}

func TestExecuteToolCallApplyPatchUsesBuiltinCodexPatch(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "f.html")
	if err := os.WriteFile(target, []byte("alpha\nbeta\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	patch := strings.Join([]string{
		"*** Begin Patch",
		"*** Update File: f.html",
		"@@",
		" alpha",
		"-beta",
		"+gamma",
		"*** End Patch",
	}, "\n")
	call := toolCall{
		ID:   "call_patch",
		Type: "function",
		Function: toolFunction{
			Name:      "apply_patch",
			Arguments: mustJSON(map[string]any{"patch": patch, "intent": "update fixture"}),
		},
	}
	raw := executeToolCall(call, root)
	var result map[string]any
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		t.Fatal(err)
	}
	if ok, _ := result["ok"].(bool); !ok {
		t.Fatalf("apply_patch failed: %#v", result)
	}
	content, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "alpha\ngamma\n" {
		t.Fatalf("patched content = %q", content)
	}
	files, _ := result["files"].([]any)
	if len(files) != 1 || files[0] != "f.html" {
		t.Fatalf("patched files = %#v", result["files"])
	}
}

func TestExecuteToolCallApplyPatchAcceptsWrappedUnifiedDiff(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "f.html")
	if err := os.WriteFile(target, []byte("old\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	patch := strings.Join([]string{
		"*** Begin Patch",
		"--- f.html",
		"+++ f.html",
		"@@ -1 +1 @@",
		"-old",
		"+new",
		"*** End Patch",
	}, "\n")
	call := toolCall{
		ID:   "call_patch",
		Type: "function",
		Function: toolFunction{
			Name:      "apply_patch",
			Arguments: mustJSON(map[string]any{"patch": patch, "intent": "update fixture"}),
		},
	}
	raw := executeToolCall(call, root)
	var result map[string]any
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		t.Fatal(err)
	}
	if ok, _ := result["ok"].(bool); !ok {
		t.Fatalf("wrapped unified diff failed: %#v", result)
	}
	content, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "new\n" {
		t.Fatalf("patched content = %q", content)
	}
}

func TestPrintToolCallStatusLeadsWithIntentAndDetails(t *testing.T) {
	previousProfile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.ANSI256)
	defer lipgloss.SetColorProfile(previousProfile)

	patch := strings.Join([]string{
		"*** Begin Patch",
		"*** Update File: f.html",
		"@@",
		"-old",
		"+new",
		"*** End Patch",
	}, "\n")
	out := captureStderr(t, func() {
		printToolCallStatus("apply_patch", map[string]any{
			"intent": "MTL 텍스처 경로 치환 정규표현식을 개선합니다.",
			"patch":  patch,
		}, map[string]any{"ok": false, "error": "patch hunk did not match f.html"})
	})
	stripped := ansi.Strip(out)
	if !strings.HasPrefix(stripped, "• MTL 텍스처 경로 치환 정규표현식을 개선합니다.") {
		t.Fatalf("intent should lead tool rendering:\n%s", stripped)
	}
	if strings.Contains(stripped, "• Patch patch") {
		t.Fatalf("tool rendering should not lead with duplicate patch title:\n%s", stripped)
	}
	if !strings.Contains(stripped, "\n  ├ Patch f.html failed") {
		t.Fatalf("tool detail line missing:\n%s", stripped)
	}
	if !strings.Contains(stripped, "\n  ├ patch: +1 -1") ||
		!strings.Contains(stripped, "\n  │  -old") ||
		!strings.Contains(stripped, "\n  │  +new") {
		t.Fatalf("styled patch preview missing:\n%s", stripped)
	}
	if !strings.Contains(out, "\x1b[") {
		t.Fatalf("patch preview should include ANSI styling:\n%s", out)
	}
	if !strings.Contains(stripped, "\n  └ args:") {
		t.Fatalf("tool args line missing:\n%s", stripped)
	}
}

func TestPrintToolCallStatusShowsFailedCommandOutputAndCompactArgs(t *testing.T) {
	out := captureStderr(t, func() {
		printToolCallStatus("exec_command", map[string]any{
			"intent": "node patch.js 실행",
			"cmd":    "cat << 'EOF' > patch.js\nconst oldStr = `${objKey}`;\nEOF\nnode patch.js",
		}, map[string]any{
			"ok":          false,
			"exit_code":   1,
			"duration_ms": 304,
			"stderr":      "ReferenceError: objKey is not defined\n    at patch.js:2:10",
		})
	})
	stripped := ansi.Strip(out)
	if !strings.Contains(stripped, "\n  ├ stderr: ReferenceError: objKey is not defined") {
		t.Fatalf("stderr preview missing:\n%s", stripped)
	}
	if !strings.Contains(stripped, `"cmd_bytes"`) || !strings.Contains(stripped, `"cmd_preview"`) {
		t.Fatalf("command args should be compacted:\n%s", stripped)
	}
	if strings.Contains(stripped, "\nconst oldStr") {
		t.Fatalf("full heredoc command should not be printed in args:\n%s", stripped)
	}
}

func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stderr
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stderr = writer
	fn()
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	os.Stderr = old
	data, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	if err := reader.Close(); err != nil {
		t.Fatal(err)
	}
	return string(data)
}
