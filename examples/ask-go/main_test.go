package main

import (
	"context"
	"encoding/json"
	"errors"
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
		MaxTokens:          4096,
		Stream:             &stream,
		Spinner:            &spinner,
		Tools:              &tools,
		ToolRoot:           "workspace",
		ToolTurns:          3,
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
	if opts.ToolRoot != "workspace" || opts.ToolTurns != 3 || opts.MarkdownTranslator != "glow" || opts.GlamourStyle != "notty" {
		t.Fatalf("tool/render config values were not applied: %+v", opts)
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

func TestTextToolCallFilterReportsIncompleteToolCall(t *testing.T) {
	filter := &toolCallTextFilter{}
	visible := filter.Feed(`before <tool_call>{"name":"write_file","arguments":{"path":"f.html","content":"partial`)
	if visible != "before " {
		t.Fatalf("visible prefix = %q", visible)
	}
	if tail, err := filter.Finish(); err == nil || tail != "" || !strings.Contains(err.Error(), "incomplete text tool call") {
		t.Fatalf("expected incomplete tool call error, tail=%q err=%v", tail, err)
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
	if !isInterruptKey(0x1b) {
		t.Fatal("esc should be treated as interrupt")
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
