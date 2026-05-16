// Command ask-go is a small Pangaea route client. It mirrors examples/ask-py:
// prompt in, OpenAI-compatible request out, optional local file tools, and
// terminal-friendly streaming output.
package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/dh-kam/refutils/flagsbinder"
	"github.com/spf13/cobra"
)

const (
	defaultBaseURL = "https://pangaea.example.com/route/public/antigravity-sonnet"
	defaultModel   = "claude-sonnet-4-6"
	defaultAPI     = "responses"
	toolCallStart  = "<tool_call>"
	toolCallEnd    = "</tool_call>"
)

var version = "dev"

type options struct {
	BaseURL            string `flag:"base-url" usage:"Pangaea route base URL"`
	Model              string `flag:"model" usage:"model name to request"`
	API                string `flag:"api" usage:"OpenAI-compatible API shape (responses|chat)"`
	MaxTokens          int    `flag:"max-tokens" usage:"maximum output tokens"`
	Stream             bool   `flag:"stream" usage:"use SSE streaming"`
	Tools              bool   `flag:"tools" usage:"enable local file tools"`
	ToolRoot           string `flag:"tool-root" usage:"directory local file tools may access"`
	ToolTurns          int    `flag:"tool-turns" usage:"maximum tool-call round trips"`
	MarkdownTranslator string `flag:"markdown-translator" usage:"terminal markdown renderer (plain|glamour|glow|rich)"`
	GlamourStyle       string `flag:"glamour-style" usage:"glamour/glow style name"`
}

var rootCmd = &cobra.Command{
	Use:           "ask-go [prompt...]",
	Short:         "Ask through a Pangaea OpenAI-compatible route",
	Args:          cobra.ArbitraryArgs,
	SilenceUsage:  true,
	SilenceErrors: true,
	Version:       version,
}

func init() {
	opts := &options{}
	binder := flagsbinder.NewViperCobraFlagsBinder().
		String("base-url", envString("PANGAEA_ASK_BASE_URL", defaultBaseURL), "Pangaea route base URL").
		String("model", envString("PANGAEA_ASK_MODEL", defaultModel), "model name to request").
		String("api", envString("PANGAEA_ASK_API", defaultAPI), "OpenAI-compatible API shape (responses|chat)").
		Int("max-tokens", envInt("PANGAEA_ASK_MAX_TOKENS", 1024), "maximum output tokens").
		Bool("stream", envBool("PANGAEA_ASK_STREAM", true), "use SSE streaming").
		Bool("tools", envBool("PANGAEA_ASK_TOOLS", true), "enable local file tools").
		String("tool-root", envString("PANGAEA_ASK_TOOL_ROOT", "."), "directory local file tools may access").
		Int("tool-turns", envInt("PANGAEA_ASK_TOOL_TURNS", 6), "maximum tool-call round trips").
		String("markdown-translator", envString("PANGAEA_ASK_MARKDOWN_TRANSLATOR", "plain"), "terminal markdown renderer (plain|glamour|glow|rich)").
		String("glamour-style", envString("PANGAEA_ASK_GLAMOUR_STYLE", "dark"), "glamour/glow style name")

	rootCmd.PreRunE = func(cmd *cobra.Command, args []string) error {
		if err := binder.BindCommand(cmd, opts, args...); err != nil {
			_ = cmd.Usage()
			return fmt.Errorf("binding failed: %w", err)
		}
		if opts.API != "responses" && opts.API != "chat" {
			_ = cmd.Usage()
			return fmt.Errorf("--api must be responses or chat")
		}
		switch opts.MarkdownTranslator {
		case "plain", "glamour", "glow", "rich":
		default:
			_ = cmd.Usage()
			return fmt.Errorf("--markdown-translator must be plain, glamour, glow, or rich")
		}
		if opts.ToolTurns < 1 {
			_ = cmd.Usage()
			return fmt.Errorf("--tool-turns must be >= 1")
		}
		return nil
	}
	rootCmd.RunE = func(cmd *cobra.Command, args []string) error {
		prompt, err := readPrompt(args, cmd.InOrStdin())
		if err != nil {
			return err
		}
		return run(cmd.Context(), *opts, prompt)
	}
	binder.SetTo(rootCmd.Flags())
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, opts options, prompt string) error {
	key := os.Getenv("PANGAEA_ASK_API_KEY")
	if key == "" {
		key = os.Getenv("OPENAI_API_KEY")
	}
	if key == "" {
		return fmt.Errorf("PANGAEA_ASK_API_KEY or OPENAI_API_KEY is required")
	}
	client := &http.Client{Timeout: 5 * time.Minute}
	if opts.Tools {
		return runToolLoop(ctx, client, opts, key, prompt)
	}
	payload := map[string]any{
		"model":  opts.Model,
		"stream": opts.Stream,
	}
	if opts.API == "responses" {
		payload["input"] = prompt
		payload["max_output_tokens"] = opts.MaxTokens
	} else {
		payload["messages"] = []map[string]any{{"role": "user", "content": prompt}}
		payload["max_tokens"] = opts.MaxTokens
	}
	resp, err := postAPI(ctx, client, opts.BaseURL, key, opts.API, payload)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if opts.Stream {
		return printStream(resp.Body, opts)
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	text := responseText(data)
	printOutput(text, opts)
	return nil
}

func runToolLoop(ctx context.Context, client *http.Client, opts options, key string, prompt string) error {
	root, err := filepath.Abs(opts.ToolRoot)
	if err != nil {
		return err
	}
	messages := []map[string]any{
		{"role": "system", "content": toolSystemPrompt(root)},
		{"role": "user", "content": prompt},
	}
	for i := 0; i < opts.ToolTurns; i++ {
		payload := map[string]any{
			"model":       opts.Model,
			"messages":    messages,
			"tools":       chatTools(),
			"tool_choice": "auto",
			"stream":      opts.Stream,
			"max_tokens":  opts.MaxTokens,
		}
		resp, err := postAPI(ctx, client, opts.BaseURL, key, "chat", payload)
		if err != nil {
			return err
		}
		var content string
		var calls []toolCall
		if opts.Stream {
			content, calls, err = readChatStreamWithTools(resp.Body, opts)
		} else {
			content, calls, err = readChatBufferedWithTools(resp.Body)
			if err == nil && len(calls) == 0 && content != "" {
				printOutput(content, opts)
			}
		}
		closeErr := resp.Body.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
		if len(calls) == 0 {
			return nil
		}
		messages = append(messages, map[string]any{
			"role":       "assistant",
			"content":    content,
			"tool_calls": calls,
		})
		for _, call := range calls {
			result := executeToolCall(call, root)
			messages = append(messages, map[string]any{
				"role":         "tool",
				"tool_call_id": call.ID,
				"content":      result,
			})
		}
	}
	return fmt.Errorf("tool call limit reached after %d turns", opts.ToolTurns)
}

func postAPI(ctx context.Context, client *http.Client, baseURL string, key string, api string, payload map[string]any) (*http.Response, error) {
	path := "/v1/responses"
	if api == "chat" {
		path = "/v1/chat/completions"
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(baseURL, "/")+path, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")
	if stream, _ := payload["stream"].(bool); stream {
		req.Header.Set("Accept", "text/event-stream")
	} else {
		req.Header.Set("Accept", "application/json")
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		defer resp.Body.Close()
		data, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(data)))
	}
	return resp, nil
}

type toolCall struct {
	ID       string       `json:"id,omitempty"`
	Type     string       `json:"type"`
	Function toolFunction `json:"function"`
}

type toolFunction struct {
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`
}

type streamToolDelta struct {
	Index    int          `json:"index"`
	ID       string       `json:"id,omitempty"`
	Type     string       `json:"type,omitempty"`
	Function toolFunction `json:"function,omitempty"`
}

type chatStreamChunk struct {
	Choices []struct {
		Delta struct {
			Content   string            `json:"content,omitempty"`
			ToolCalls []streamToolDelta `json:"tool_calls,omitempty"`
		} `json:"delta"`
	} `json:"choices"`
}

type chatResponse struct {
	Choices []struct {
		Message struct {
			Content   any        `json:"content,omitempty"`
			ToolCalls []toolCall `json:"tool_calls,omitempty"`
		} `json:"message"`
	} `json:"choices"`
}

func readChatBufferedWithTools(r io.Reader) (string, []toolCall, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return "", nil, err
	}
	var resp chatResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return "", nil, err
	}
	if len(resp.Choices) == 0 {
		return "", nil, nil
	}
	content := contentToString(resp.Choices[0].Message.Content)
	calls := normalizeToolCalls(resp.Choices[0].Message.ToolCalls)
	visible, textCalls := extractTextToolCalls(content)
	if len(textCalls) > 0 {
		content = visible
		calls = append(calls, textCalls...)
	}
	return content, calls, nil
}

func readChatStreamWithTools(r io.Reader, opts options) (string, []toolCall, error) {
	reader := bufio.NewReader(r)
	filter := &toolCallTextFilter{}
	renderer := newRenderer(opts)
	pieces := map[int]*toolCall{}
	var content strings.Builder
	emitted := false
	for {
		line, err := reader.ReadString('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			return "", nil, err
		}
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "data:") {
			data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			if data == "[DONE]" {
				break
			}
			var chunk chatStreamChunk
			if json.Unmarshal([]byte(data), &chunk) == nil && len(chunk.Choices) > 0 {
				text := chunk.Choices[0].Delta.Content
				if text != "" {
					visible := filter.Feed(text)
					if visible != "" {
						emitted = true
						content.WriteString(visible)
						renderer.Feed(visible)
					}
				}
				mergeToolDeltas(pieces, chunk.Choices[0].Delta.ToolCalls)
			}
		}
		if errors.Is(err, io.EOF) {
			break
		}
	}
	if tail := filter.Finish(); tail != "" {
		emitted = true
		content.WriteString(tail)
		renderer.Feed(tail)
	}
	if emitted {
		renderer.Finish()
	}
	calls := normalizedStreamToolCalls(pieces)
	calls = append(calls, filter.ToolCalls...)
	return content.String(), calls, nil
}

func mergeToolDeltas(pieces map[int]*toolCall, deltas []streamToolDelta) {
	for _, delta := range deltas {
		slot := pieces[delta.Index]
		if slot == nil {
			slot = &toolCall{Type: "function"}
			pieces[delta.Index] = slot
		}
		if delta.ID != "" {
			slot.ID = delta.ID
		}
		if delta.Type != "" {
			slot.Type = delta.Type
		}
		if delta.Function.Name != "" {
			slot.Function.Name += delta.Function.Name
		}
		if delta.Function.Arguments != "" {
			slot.Function.Arguments += delta.Function.Arguments
		}
	}
}

func normalizedStreamToolCalls(pieces map[int]*toolCall) []toolCall {
	indexes := make([]int, 0, len(pieces))
	for index := range pieces {
		indexes = append(indexes, index)
	}
	sort.Ints(indexes)
	calls := make([]toolCall, 0, len(indexes))
	for _, index := range indexes {
		calls = append(calls, *pieces[index])
	}
	return normalizeToolCalls(calls)
}

func normalizeToolCalls(calls []toolCall) []toolCall {
	out := make([]toolCall, 0, len(calls))
	for i, call := range calls {
		if strings.TrimSpace(call.Function.Name) == "" {
			continue
		}
		if call.ID == "" {
			call.ID = fmt.Sprintf("call_%d", i)
		}
		if call.Type == "" {
			call.Type = "function"
		}
		if strings.TrimSpace(call.Function.Arguments) == "" {
			call.Function.Arguments = "{}"
		}
		out = append(out, call)
	}
	return out
}

type toolCallTextFilter struct {
	Buffer         string
	InToolCall     bool
	ToolCallBuffer string
	ToolCalls      []toolCall
}

func (f *toolCallTextFilter) Feed(text string) string {
	f.Buffer += text
	var visible strings.Builder
	for f.Buffer != "" {
		if f.InToolCall {
			end := strings.Index(f.Buffer, toolCallEnd)
			if end == -1 {
				hold := partialTagSuffixLen(f.Buffer, toolCallEnd)
				if hold > 0 {
					f.ToolCallBuffer += f.Buffer[:len(f.Buffer)-hold]
					f.Buffer = f.Buffer[len(f.Buffer)-hold:]
				} else {
					f.ToolCallBuffer += f.Buffer
					f.Buffer = ""
				}
				break
			}
			f.ToolCallBuffer += f.Buffer[:end]
			f.appendToolCall(f.ToolCallBuffer)
			f.ToolCallBuffer = ""
			f.InToolCall = false
			f.Buffer = f.Buffer[end+len(toolCallEnd):]
			continue
		}
		start := strings.Index(f.Buffer, toolCallStart)
		if start == -1 {
			hold := partialTagSuffixLen(f.Buffer, toolCallStart)
			if hold > 0 {
				visible.WriteString(f.Buffer[:len(f.Buffer)-hold])
				f.Buffer = f.Buffer[len(f.Buffer)-hold:]
			} else {
				visible.WriteString(f.Buffer)
				f.Buffer = ""
			}
			break
		}
		visible.WriteString(f.Buffer[:start])
		f.Buffer = f.Buffer[start+len(toolCallStart):]
		f.InToolCall = true
	}
	return visible.String()
}

func (f *toolCallTextFilter) Finish() string {
	if f.InToolCall {
		payload := f.ToolCallBuffer + f.Buffer
		if f.appendToolCall(payload) {
			f.Buffer = ""
			f.ToolCallBuffer = ""
			f.InToolCall = false
			return ""
		}
		f.Buffer = ""
		f.ToolCallBuffer = ""
		f.InToolCall = false
		return toolCallStart + payload
	}
	visible := f.Buffer
	f.Buffer = ""
	return visible
}

func (f *toolCallTextFilter) appendToolCall(payload string) bool {
	call, err := textToolCallToOpenAI(payload, len(f.ToolCalls))
	if err != nil {
		fmt.Fprintln(os.Stderr, "warning: could not parse text tool call:", err)
		return false
	}
	f.ToolCalls = append(f.ToolCalls, call)
	return true
}

func extractTextToolCalls(content string) (string, []toolCall) {
	filter := &toolCallTextFilter{}
	visible := filter.Feed(content) + filter.Finish()
	return visible, filter.ToolCalls
}

func textToolCallToOpenAI(payload string, index int) (toolCall, error) {
	var raw map[string]any
	if err := parseJSONObjectLenient(payload, &raw); err != nil {
		return toolCall{}, err
	}
	name, _ := raw["name"].(string)
	args := raw["arguments"]
	if fn, ok := raw["function"].(map[string]any); ok {
		if v, _ := fn["name"].(string); v != "" {
			name = v
		}
		args = fn["arguments"]
	}
	if strings.TrimSpace(name) == "" {
		return toolCall{}, fmt.Errorf("tool call has no function name")
	}
	rawArgs, err := marshalArguments(args)
	if err != nil {
		return toolCall{}, err
	}
	return toolCall{
		ID:   fmt.Sprintf("text_call_%d", index),
		Type: "function",
		Function: toolFunction{
			Name:      strings.TrimSpace(name),
			Arguments: rawArgs,
		},
	}, nil
}

func parseJSONObjectLenient(raw string, out *map[string]any) error {
	payload := strings.TrimSpace(raw)
	if payload == "" {
		return fmt.Errorf("empty payload")
	}
	if json.Unmarshal([]byte(payload), out) == nil {
		return nil
	}
	escaped := escapeJSONStringControlChars(payload)
	if json.Unmarshal([]byte(escaped), out) == nil {
		return nil
	}
	start := strings.Index(payload, "{")
	end := strings.LastIndex(payload, "}")
	if start >= 0 && end > start {
		if err := json.Unmarshal([]byte(escapeJSONStringControlChars(payload[start:end+1])), out); err == nil {
			return nil
		}
	}
	return fmt.Errorf("invalid JSON object")
}

func escapeJSONStringControlChars(raw string) string {
	var out strings.Builder
	inString := false
	escaped := false
	for _, r := range raw {
		if escaped {
			out.WriteRune(r)
			escaped = false
			continue
		}
		if r == '\\' {
			out.WriteRune(r)
			escaped = true
			continue
		}
		if r == '"' {
			out.WriteRune(r)
			inString = !inString
			continue
		}
		if inString && r == '\n' {
			out.WriteString("\\n")
			continue
		}
		if inString && r == '\r' {
			out.WriteString("\\r")
			continue
		}
		if inString && r == '\t' {
			out.WriteString("\\t")
			continue
		}
		out.WriteRune(r)
	}
	return out.String()
}

func partialTagSuffixLen(text string, tag string) int {
	maxLen := len(text)
	if maxLen > len(tag)-1 {
		maxLen = len(tag) - 1
	}
	for size := maxLen; size > 0; size-- {
		if strings.HasPrefix(tag, text[len(text)-size:]) {
			return size
		}
	}
	return 0
}

func executeToolCall(call toolCall, root string) string {
	args := map[string]any{}
	result := map[string]any{}
	if err := json.Unmarshal([]byte(call.Function.Arguments), &args); err != nil {
		result = map[string]any{"ok": false, "error": err.Error()}
		printToolCallStatus(call.Function.Name, args, result)
		return mustJSON(result)
	}
	switch call.Function.Name {
	case "write_file":
		result = toolWriteFile(root, args)
	case "read_file":
		result = toolReadFile(root, args)
	case "list_files":
		result = toolListFiles(root, args)
	default:
		result = map[string]any{"ok": false, "error": "unknown tool: " + call.Function.Name}
	}
	printToolCallStatus(call.Function.Name, args, result)
	return mustJSON(result)
}

func toolWriteFile(root string, args map[string]any) map[string]any {
	path, _ := args["path"].(string)
	content, ok := args["content"].(string)
	if strings.TrimSpace(path) == "" {
		return map[string]any{"ok": false, "error": "write_file.path is required"}
	}
	if !ok {
		return map[string]any{"ok": false, "error": "write_file.content must be a string"}
	}
	target, err := resolveToolPath(root, path)
	if err != nil {
		return map[string]any{"ok": false, "error": err.Error()}
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return map[string]any{"ok": false, "error": err.Error()}
	}
	if err := os.WriteFile(target, []byte(content), 0o644); err != nil {
		return map[string]any{"ok": false, "error": err.Error()}
	}
	return map[string]any{"ok": true, "path": relativeToolPath(root, target), "bytes": len([]byte(content))}
}

func toolReadFile(root string, args map[string]any) map[string]any {
	path, _ := args["path"].(string)
	if strings.TrimSpace(path) == "" {
		return map[string]any{"ok": false, "error": "read_file.path is required"}
	}
	target, err := resolveToolPath(root, path)
	if err != nil {
		return map[string]any{"ok": false, "error": err.Error()}
	}
	data, err := os.ReadFile(target)
	if err != nil {
		return map[string]any{"ok": false, "error": err.Error()}
	}
	limit := 128 * 1024
	truncated := len(data) > limit
	if truncated {
		data = data[:limit]
	}
	return map[string]any{"ok": true, "path": relativeToolPath(root, target), "content": string(data), "truncated": truncated, "bytes": len(data)}
}

func toolListFiles(root string, args map[string]any) map[string]any {
	path, _ := args["path"].(string)
	if path == "" {
		path = "."
	}
	target, err := resolveToolPath(root, path)
	if err != nil {
		return map[string]any{"ok": false, "error": err.Error()}
	}
	entries, err := os.ReadDir(target)
	if err != nil {
		return map[string]any{"ok": false, "error": err.Error()}
	}
	sort.Slice(entries, func(i, j int) bool { return strings.ToLower(entries[i].Name()) < strings.ToLower(entries[j].Name()) })
	out := make([]map[string]any, 0, len(entries))
	for i, entry := range entries {
		if i >= 200 {
			break
		}
		info, _ := entry.Info()
		kind := "file"
		if entry.IsDir() {
			kind = "directory"
		}
		size := int64(0)
		if info != nil {
			size = info.Size()
		}
		out = append(out, map[string]any{"name": entry.Name(), "path": relativeToolPath(root, filepath.Join(target, entry.Name())), "type": kind, "bytes": size})
	}
	return map[string]any{"ok": true, "path": relativeToolPath(root, target), "entries": out, "truncated": len(entries) > 200}
}

func resolveToolPath(root string, requested string) (string, error) {
	var target string
	if filepath.IsAbs(requested) {
		target = filepath.Clean(requested)
	} else {
		target = filepath.Join(root, requested)
	}
	abs, err := filepath.Abs(target)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(root, abs)
	if err != nil {
		return "", err
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return "", fmt.Errorf("path escapes tool root: %s", requested)
	}
	return abs, nil
}

func relativeToolPath(root string, target string) string {
	rel, err := filepath.Rel(root, target)
	if err != nil {
		return target
	}
	return rel
}

func printToolCallStatus(name string, args map[string]any, result map[string]any) {
	path, _ := args["path"].(string)
	ok, _ := result["ok"].(bool)
	status := "error"
	color := "\033[31m"
	if ok {
		status = "ok"
		color = "\033[32m"
	}
	detail := ""
	if path != "" {
		detail = " \033[2m" + path + "\033[0m"
	}
	if ok {
		if n, ok := numberToInt(result["bytes"]); ok {
			detail += " \033[2m" + formatBytes(n) + "\033[0m"
		}
	} else if errText, _ := result["error"].(string); errText != "" {
		detail += " \033[2m" + errText + "\033[0m"
	}
	fmt.Fprintf(os.Stderr, "\033[36m*\033[0m \033[1mtool\033[0m \033[33m%s\033[0m%s %s%s\033[0m\n", name, detail, color, status)
	fmt.Fprintf(os.Stderr, "  \033[36mintent\033[0m %s\n", toolCallIntent(name, args))
	fmt.Fprintf(os.Stderr, "  \033[36margs\033[0m %s\n", formatToolArgs(args))
}

func toolCallIntent(name string, args map[string]any) string {
	if intent, _ := args["intent"].(string); strings.TrimSpace(intent) != "" {
		return strings.TrimSpace(intent)
	}
	path, _ := args["path"].(string)
	if path != "" {
		switch name {
		case "write_file":
			return "write requested file " + path
		case "read_file":
			return "inspect file " + path
		case "list_files":
			return "list files under " + path
		}
	}
	return "run " + name
}

func formatToolArgs(args map[string]any) string {
	display := map[string]any{}
	for key, value := range args {
		if key == "intent" {
			continue
		}
		if key == "content" {
			content, _ := value.(string)
			display["content_bytes"] = len([]byte(content))
			if preview := compactPreview(content, 96); preview != "" {
				display["content_preview"] = preview
			}
			continue
		}
		display[key] = value
	}
	return mustJSON(display)
}

func compactPreview(value string, limit int) string {
	preview := strings.Join(strings.Fields(value), " ")
	if len(preview) <= limit {
		return preview
	}
	return preview[:limit-1] + "…"
}

func responseText(data []byte) string {
	var raw map[string]any
	if json.Unmarshal(data, &raw) != nil {
		return ""
	}
	if text, _ := raw["output_text"].(string); text != "" {
		return text
	}
	if output, ok := raw["output"].([]any); ok {
		var parts strings.Builder
		for _, item := range output {
			obj, _ := item.(map[string]any)
			content, _ := obj["content"].([]any)
			for _, block := range content {
				blockObj, _ := block.(map[string]any)
				if text, _ := blockObj["text"].(string); text != "" {
					parts.WriteString(text)
				}
			}
		}
		if parts.Len() > 0 {
			return parts.String()
		}
	}
	var chat chatResponse
	if json.Unmarshal(data, &chat) == nil && len(chat.Choices) > 0 {
		return contentToString(chat.Choices[0].Message.Content)
	}
	return ""
}

func printStream(r io.Reader, opts options) error {
	reader := bufio.NewReader(r)
	renderer := newRenderer(opts)
	emitted := false
	for {
		line, err := reader.ReadString('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			return err
		}
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "data:") {
			data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			if data == "[DONE]" {
				break
			}
			if text := streamEventText([]byte(data)); text != "" {
				emitted = true
				renderer.Feed(text)
			}
		}
		if errors.Is(err, io.EOF) {
			break
		}
	}
	if emitted {
		renderer.Finish()
	}
	return nil
}

func streamEventText(data []byte) string {
	var raw map[string]any
	if json.Unmarshal(data, &raw) != nil {
		return ""
	}
	if raw["type"] == "response.output_text.delta" {
		text, _ := raw["delta"].(string)
		return text
	}
	var chunk chatStreamChunk
	if json.Unmarshal(data, &chunk) == nil && len(chunk.Choices) > 0 {
		return chunk.Choices[0].Delta.Content
	}
	return ""
}

type renderer struct {
	opts   options
	buffer strings.Builder
}

func newRenderer(opts options) *renderer {
	return &renderer{opts: opts}
}

func (r *renderer) Feed(text string) {
	if r.opts.MarkdownTranslator == "plain" {
		fmt.Print(text)
		return
	}
	r.buffer.WriteString(text)
}

func (r *renderer) Finish() {
	if r.opts.MarkdownTranslator == "plain" {
		fmt.Println()
		return
	}
	printOutput(r.buffer.String(), r.opts)
}

func printOutput(markdown string, opts options) {
	rendered, ok := renderWithGlamour(markdown, opts)
	if ok {
		fmt.Print(rendered)
		if !strings.HasSuffix(rendered, "\n") {
			fmt.Println()
		}
		return
	}
	fmt.Println(markdown)
}

func renderWithGlamour(markdown string, opts options) (string, bool) {
	if markdown == "" || (opts.MarkdownTranslator != "glamour" && opts.MarkdownTranslator != "glow") {
		return "", false
	}
	width := envInt("COLUMNS", 100)
	commands := [][]string{}
	if path, err := exec.LookPath("glamour"); err == nil {
		commands = append(commands, []string{path, "-s", opts.GlamourStyle, "-w", strconv.Itoa(width)})
	}
	if path, err := exec.LookPath("glow"); err == nil {
		commands = append(commands, []string{path, "-s", opts.GlamourStyle, "-w", strconv.Itoa(width), "-"})
	}
	for _, command := range commands {
		cmd := exec.Command(command[0], command[1:]...)
		cmd.Stdin = strings.NewReader(markdown)
		out, err := cmd.Output()
		if err == nil {
			return string(out), true
		}
	}
	return "", false
}

func readPrompt(args []string, input io.Reader) (string, error) {
	if len(args) > 0 {
		return strings.Join(args, " "), nil
	}
	if stat, err := os.Stdin.Stat(); err == nil && stat.Mode()&os.ModeCharDevice == 0 {
		data, err := io.ReadAll(input)
		if err != nil {
			return "", err
		}
		if strings.TrimSpace(string(data)) != "" {
			return strings.TrimSpace(string(data)), nil
		}
	}
	return "", fmt.Errorf("prompt is required")
}

func contentToString(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case []any:
		var b strings.Builder
		for _, item := range typed {
			obj, _ := item.(map[string]any)
			if text, _ := obj["text"].(string); text != "" {
				b.WriteString(text)
			}
		}
		return b.String()
	default:
		return ""
	}
}

func marshalArguments(value any) (string, error) {
	switch typed := value.(type) {
	case nil:
		return "{}", nil
	case string:
		if strings.TrimSpace(typed) == "" {
			return "{}", nil
		}
		return typed, nil
	default:
		data, err := json.Marshal(typed)
		if err != nil {
			return "", err
		}
		return string(data), nil
	}
}

func mustJSON(value any) string {
	data, err := json.Marshal(value)
	if err != nil {
		return `{"ok":false,"error":"json marshal failed"}`
	}
	return string(data)
}

func numberToInt(value any) (int, bool) {
	switch typed := value.(type) {
	case int:
		return typed, true
	case int64:
		return int(typed), true
	case float64:
		return int(typed), true
	default:
		return 0, false
	}
}

func formatBytes(value int) string {
	if value < 1024 {
		return fmt.Sprintf("%dB", value)
	}
	if value < 1024*1024 {
		return fmt.Sprintf("%.1fKB", float64(value)/1024)
	}
	return fmt.Sprintf("%.1fMB", float64(value)/(1024*1024))
}

func envString(name string, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func envBool(name string, fallback bool) bool {
	value := strings.ToLower(strings.TrimSpace(os.Getenv(name)))
	if value == "" {
		return fallback
	}
	switch value {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return fallback
	}
}

func envInt(name string, fallback int) int {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		if parsed, err := strconv.Atoi(value); err == nil {
			return parsed
		}
	}
	return fallback
}

func toolSystemPrompt(root string) string {
	return "You can use local tools when they are necessary to complete the user's request.\n" +
		"If the user asks you to create, edit, inspect, or list files, call the appropriate tool instead of only explaining the steps.\n" +
		"Only write files that are directly requested by the user. Keep paths relative unless the user explicitly asks otherwise.\n" +
		"When you call a tool, include a short Korean or English `intent` argument that explains why this tool call is needed.\n" +
		"Local tool root: " + root
}

func chatTools() []map[string]any {
	intent := map[string]any{"type": "string", "description": "Short human-readable reason for this tool call."}
	return []map[string]any{
		{
			"type": "function",
			"function": map[string]any{
				"name":        "write_file",
				"description": "Create or overwrite a UTF-8 text file under the configured tool root.",
				"parameters": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"path":    map[string]any{"type": "string", "description": "Relative path to write, for example a.yaml."},
						"content": map[string]any{"type": "string", "description": "Complete UTF-8 file content."},
						"intent":  intent,
					},
					"required":             []string{"path", "content"},
					"additionalProperties": false,
				},
			},
		},
		{
			"type": "function",
			"function": map[string]any{
				"name":        "read_file",
				"description": "Read a UTF-8 text file under the configured tool root.",
				"parameters": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"path":   map[string]any{"type": "string", "description": "Relative path to read."},
						"intent": intent,
					},
					"required":             []string{"path"},
					"additionalProperties": false,
				},
			},
		},
		{
			"type": "function",
			"function": map[string]any{
				"name":        "list_files",
				"description": "List files under a directory in the configured tool root.",
				"parameters": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"path":   map[string]any{"type": "string", "description": "Relative directory path. Defaults to ."},
						"intent": intent,
					},
					"additionalProperties": false,
				},
			},
		},
	}
}
