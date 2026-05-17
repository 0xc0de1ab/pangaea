// Command ask is a small Pangaea route client. It mirrors examples/ask-py:
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
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	bubblesSpinner "github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/dh-kam/refutils/flagsbinder"
	"github.com/muesli/cancelreader"
	"github.com/spf13/cobra"
)

const (
	defaultBaseURL    = "https://pangaea.example.com/route/public/antigravity-sonnet"
	defaultAPI        = "responses"
	defaultConfigName = "ask-config.json"
	defaultToolTurns  = 0
	toolLoopRepeats   = 3
	toolCallStart     = "<tool_call>"
	toolCallEnd       = "</tool_call>"
)

var version = "dev"

var errInterrupted = errors.New("interrupted")

type terminalModeState struct {
	value any
}

var (
	faintStyle          = lipgloss.NewStyle().Faint(true)
	spinnerFrameStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("6"))
	spinnerLabelStyle   = lipgloss.NewStyle().Faint(true)
	spinnerShimmerDim   = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	spinnerShimmerMid   = lipgloss.NewStyle().Foreground(lipgloss.Color("37"))
	spinnerShimmerHot   = lipgloss.NewStyle().Foreground(lipgloss.Color("51")).Bold(true)
	spinnerShimmerTrail = lipgloss.NewStyle().Foreground(lipgloss.Color("123"))
	toolVerbStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("6"))
	toolOKStyle         = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
	toolErrorStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
	toolCommandStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))
)

type options struct {
	ConfigPath         string `flag:"config" usage:"ask config JSON path"`
	BaseURL            string `flag:"base-url" usage:"Pangaea route base URL"`
	APIKey             string `flag:"api-key" usage:"API key for Authorization bearer token"`
	Model              string `flag:"model" usage:"model name to request"`
	API                string `flag:"api" usage:"OpenAI-compatible API shape (responses|chat)"`
	MaxTokens          int    `flag:"max-tokens" usage:"maximum output tokens"`
	Stream             bool   `flag:"stream" usage:"use SSE streaming"`
	Spinner            bool   `flag:"spinner" usage:"show a waiting spinner on interactive terminals"`
	Tools              bool   `flag:"tools" usage:"enable local file tools"`
	ToolRoot           string `flag:"tool-root" usage:"directory local file tools may access"`
	ToolTurns          int    `flag:"tool-turns" usage:"maximum tool-call round trips"`
	MarkdownTranslator string `flag:"markdown-translator" usage:"terminal markdown renderer (plain|glamour|glow|rich)"`
	GlamourStyle       string `flag:"glamour-style" usage:"glamour/glow style name"`
	RichCodeTheme      string `flag:"rich-code-theme" usage:"Rich/Pygments code highlighting theme"`
}

var rootCmd = &cobra.Command{
	Use:           "ask [prompt...]",
	Short:         "Ask through a Pangaea OpenAI-compatible route",
	Args:          cobra.ArbitraryArgs,
	SilenceUsage:  true,
	SilenceErrors: true,
	Version:       version,
}

func init() {
	opts := &options{}
	binder := flagsbinder.NewViperCobraFlagsBinder().
		String("config", envString("PANGAEA_ASK_CONFIG", defaultAskConfigPath()), "ask config JSON path").
		String("base-url", envString("PANGAEA_ASK_BASE_URL", defaultBaseURL), "Pangaea route base URL").
		String("api-key", "", "API key for Authorization bearer token").
		String("model", envString("PANGAEA_ASK_MODEL", ""), "model name to request; omit to let the route choose").
		String("api", envString("PANGAEA_ASK_API", defaultAPI), "OpenAI-compatible API shape (responses|chat)").
		Int("max-tokens", envInt("PANGAEA_ASK_MAX_TOKENS", 0), "maximum output tokens").
		Bool("stream", envBool("PANGAEA_ASK_STREAM", true), "use SSE streaming").
		Bool("spinner", envBool("PANGAEA_ASK_SPINNER", true), "show a waiting spinner on interactive terminals").
		Bool("tools", envBool("PANGAEA_ASK_TOOLS", true), "enable local file tools").
		String("tool-root", envString("PANGAEA_ASK_TOOL_ROOT", "."), "directory local file tools may access").
		Int("tool-turns", envInt("PANGAEA_ASK_TOOL_TURNS", defaultToolTurns), "maximum tool-call round trips; 0 disables the fixed cap").
		String("markdown-translator", envString("PANGAEA_ASK_MARKDOWN_TRANSLATOR", "plain"), "terminal markdown renderer (plain|glamour|glow|rich)").
		String("glamour-style", envString("PANGAEA_ASK_GLAMOUR_STYLE", "dark"), "glamour/glow style name").
		String("rich-code-theme", envString("PANGAEA_ASK_RICH_CODE_THEME", "monokai"), "Rich/Pygments code highlighting theme")

	rootCmd.PreRunE = func(cmd *cobra.Command, args []string) error {
		if err := binder.BindCommand(cmd, opts, args...); err != nil {
			_ = cmd.Usage()
			return fmt.Errorf("binding failed: %w", err)
		}
		if err := applyAskConfig(cmd, opts); err != nil {
			return err
		}
		if strings.TrimSpace(opts.BaseURL) == "" {
			_ = cmd.Usage()
			return fmt.Errorf("--base-url is required")
		}
		if strings.TrimSpace(opts.APIKey) == "" {
			return fmt.Errorf("API key is required; set --api-key, PANGAEA_ASK_API_KEY, OPENAI_API_KEY, or %s", opts.ConfigPath)
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
		if opts.ToolTurns < 0 {
			_ = cmd.Usage()
			return fmt.Errorf("--tool-turns must be >= 0")
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
		clearStatusLine()
		if errors.Is(err, errInterrupted) {
			fmt.Fprintln(os.Stderr, faintStyle.Render("Interrupted."))
			os.Exit(130)
		}
		fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, opts options, prompt string) error {
	ctx, stopSignals := signal.NotifyContext(ctx, os.Interrupt)
	defer stopSignals()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stopKeys := startInteractiveCancelWatcher(ctx, opts, cancel)
	defer stopKeys()

	key := strings.TrimSpace(opts.APIKey)
	if key == "" {
		return fmt.Errorf("API key is required")
	}
	client := &http.Client{Timeout: 5 * time.Minute}
	if opts.Tools {
		return normalizeRunError(ctx, runToolLoop(ctx, client, opts, key, prompt))
	}
	payload := map[string]any{
		"stream": opts.Stream,
	}
	applyModel(payload, opts.Model)
	if opts.API == "responses" {
		payload["input"] = prompt
		applyMaxTokens(payload, opts.API, opts.MaxTokens)
	} else {
		payload["messages"] = []map[string]any{{"role": "user", "content": prompt}}
		applyMaxTokens(payload, opts.API, opts.MaxTokens)
	}
	spin := newStatusSpinner(opts, "Thinking")
	spin.Start()
	resp, err := postAPIWithRetry(ctx, client, opts.BaseURL, key, opts.API, payload)
	if err != nil {
		spin.Stop()
		return normalizeRunError(ctx, err)
	}
	defer resp.Body.Close()
	if opts.Stream {
		return normalizeRunError(ctx, printStream(resp.Body, opts, spin))
	}
	data, err := io.ReadAll(resp.Body)
	spin.Stop()
	if err != nil {
		return normalizeRunError(ctx, err)
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
	tools := newToolRunner(root, opts)
	loopDetector := newToolLoopDetector(toolLoopRepeats)
	for turn := 0; opts.ToolTurns == 0 || turn < opts.ToolTurns; turn++ {
		payload := map[string]any{
			"messages":    messages,
			"tools":       chatTools(),
			"tool_choice": "auto",
			"stream":      opts.Stream,
		}
		applyModel(payload, opts.Model)
		applyMaxTokens(payload, "chat", opts.MaxTokens)
		spin := newStatusSpinner(opts, "Thinking")
		spin.Start()
		resp, err := postAPIWithRetry(ctx, client, opts.BaseURL, key, "chat", payload)
		if err != nil {
			spin.Stop()
			return normalizeRunError(ctx, err)
		}
		var content string
		var calls []toolCall
		if opts.Stream {
			content, calls, err = readChatStreamWithTools(resp.Body, opts, spin)
		} else {
			content, calls, err = readChatBufferedWithTools(resp.Body)
			spin.Stop()
			if err == nil && len(calls) == 0 && content != "" {
				printOutput(content, opts)
			}
		}
		closeErr := resp.Body.Close()
		if err != nil {
			return normalizeRunError(ctx, err)
		}
		if closeErr != nil {
			return normalizeRunError(ctx, closeErr)
		}
		if len(calls) == 0 {
			return nil
		}
		if err := loopDetector.Observe(calls, tools.Generation()); err != nil {
			return err
		}
		messages = append(messages, map[string]any{
			"role":       "assistant",
			"content":    content,
			"tool_calls": calls,
		})
		results := tools.ExecuteCalls(ctx, calls)
		if ctx.Err() != nil {
			return normalizeRunError(ctx, ctx.Err())
		}
		for _, result := range results {
			messages = append(messages, map[string]any{
				"role":         "tool",
				"tool_call_id": result.Call.ID,
				"content":      result.Content,
			})
		}
	}
	return fmt.Errorf("tool call limit reached after %d turns", opts.ToolTurns)
}

func normalizeRunError(ctx context.Context, err error) error {
	if err == nil {
		if ctx != nil && ctx.Err() != nil {
			return errInterrupted
		}
		return nil
	}
	if ctx != nil && ctx.Err() != nil {
		return errInterrupted
	}
	return err
}

func startInteractiveCancelWatcher(ctx context.Context, opts options, cancel context.CancelFunc) func() {
	if cancel == nil || !opts.Spinner || !isInteractiveTerminal(os.Stdin) || !isInteractiveTerminal(os.Stderr) {
		return func() {}
	}
	fd := os.Stdin.Fd()
	oldState, err := makeInputCancelMode(fd)
	if err != nil {
		return func() {}
	}
	reader, err := cancelreader.NewReader(os.Stdin)
	if err != nil {
		_ = restoreInputCancelMode(fd, oldState)
		return func() {}
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		buf := []byte{0}
		for {
			n, err := reader.Read(buf)
			if err != nil {
				return
			}
			if n > 0 && isInterruptKey(buf[0]) {
				cancel()
				return
			}
		}
	}()
	var once sync.Once
	return func() {
		once.Do(func() {
			_ = reader.Cancel()
			_ = reader.Close()
			_ = restoreInputCancelMode(fd, oldState)
			select {
			case <-done:
			case <-ctx.Done():
			case <-time.After(200 * time.Millisecond):
			}
		})
	}
}

func isInterruptKey(key byte) bool {
	return key == 0x03 || key == 0x1b
}

type httpStatusError struct {
	StatusCode int
	Body       string
}

func (e *httpStatusError) Error() string {
	if e == nil {
		return ""
	}
	return fmt.Sprintf("HTTP %d: %s", e.StatusCode, e.Body)
}

func postAPIWithRetry(ctx context.Context, client *http.Client, baseURL string, key string, api string, payload map[string]any) (*http.Response, error) {
	delays := []time.Duration{750 * time.Millisecond, 1500 * time.Millisecond, 3 * time.Second, 5 * time.Second}
	var lastErr error
	for attempt := 0; attempt <= len(delays); attempt++ {
		resp, err := postAPI(ctx, client, baseURL, key, api, payload)
		if err == nil {
			return resp, nil
		}
		lastErr = err
		if attempt >= len(delays) || !isTransientNoProviderMatched(err) {
			return nil, err
		}
		fmt.Fprintln(os.Stderr, faintStyle.Render(fmt.Sprintf("Provider route is warming up; retrying in %s...", formatElapsed(delays[attempt]))))
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(delays[attempt]):
		}
	}
	return nil, lastErr
}

func isTransientNoProviderMatched(err error) bool {
	var statusErr *httpStatusError
	if !errors.As(err, &statusErr) {
		return false
	}
	return statusErr.StatusCode == http.StatusConflict && strings.Contains(strings.ToLower(statusErr.Body), "no provider matched")
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
		return nil, &httpStatusError{StatusCode: resp.StatusCode, Body: strings.TrimSpace(string(data))}
	}
	return resp, nil
}

func applyMaxTokens(payload map[string]any, api string, maxTokens int) {
	if maxTokens <= 0 {
		return
	}
	if api == "responses" {
		payload["max_output_tokens"] = maxTokens
		return
	}
	payload["max_tokens"] = maxTokens
}

func applyModel(payload map[string]any, model string) {
	model = strings.TrimSpace(model)
	if model == "" {
		return
	}
	payload["model"] = model
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
		FinishReason string `json:"finish_reason,omitempty"`
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

func readChatStreamWithTools(r io.Reader, opts options, spin *statusSpinner) (string, []toolCall, error) {
	reader := bufio.NewReader(r)
	filter := &toolCallTextFilter{}
	renderer := newRenderer(opts)
	pieces := map[int]*toolCall{}
	var content strings.Builder
	emitted := false
	seenDone := false
	finishReason := ""
	for {
		line, err := reader.ReadString('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			stopSpinner(&spin)
			return "", nil, err
		}
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "data:") {
			data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			if data == "[DONE]" {
				seenDone = true
				break
			}
			if message := streamEventError([]byte(data)); message != "" {
				stopSpinner(&spin)
				return "", nil, fmt.Errorf("stream error: %s", message)
			}
			var chunk chatStreamChunk
			if json.Unmarshal([]byte(data), &chunk) == nil && len(chunk.Choices) > 0 {
				text := chunk.Choices[0].Delta.Content
				if text != "" {
					visible := filter.Feed(text)
					if visible != "" {
						markStreamProgress(opts, &spin, visible)
						emitted = true
						content.WriteString(visible)
						renderer.Feed(visible)
					}
				}
				if len(chunk.Choices[0].Delta.ToolCalls) > 0 {
					stopSpinner(&spin)
					mergeToolDeltas(pieces, chunk.Choices[0].Delta.ToolCalls)
				}
				if reason := strings.TrimSpace(chunk.Choices[0].FinishReason); reason != "" {
					finishReason = reason
				}
			}
		}
		if errors.Is(err, io.EOF) {
			break
		}
	}
	tail, filterErr := filter.Finish()
	if filterErr != nil {
		stopSpinner(&spin)
		if emitted {
			renderer.Finish()
		}
		return content.String(), nil, filterErr
	}
	if tail != "" {
		markStreamProgress(opts, &spin, tail)
		emitted = true
		content.WriteString(tail)
		renderer.Feed(tail)
	}
	stopSpinner(&spin)
	if emitted {
		renderer.Finish()
	}
	calls := normalizedStreamToolCalls(pieces)
	calls = append(calls, filter.ToolCalls...)
	if !seenDone {
		return content.String(), calls, fmt.Errorf("stream ended before [DONE]")
	}
	if err := finishReasonError(finishReason); err != nil {
		return content.String(), calls, err
	}
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
	Buffer               string
	InToolCall           bool
	ToolCallBuffer       string
	InObservation        bool
	InBracketToolResult  bool
	BracketResultBuffer  string
	SkipToolResultObject bool
	ToolResultBuffer     string
	ToolCalls            []toolCall
}

func (f *toolCallTextFilter) Feed(text string) string {
	f.Buffer += text
	var visible strings.Builder
	for f.Buffer != "" {
		if f.SkipToolResultObject {
			if done := f.consumeToolResultObject(); !done {
				break
			}
			continue
		}
		if f.InBracketToolResult {
			if done := f.consumeBracketToolResultHeader(); !done {
				break
			}
			continue
		}
		if f.InObservation {
			end := strings.Index(f.Buffer, "</observation>")
			if end == -1 {
				hold := partialTagSuffixLen(f.Buffer, "</observation>")
				if hold > 0 {
					f.Buffer = f.Buffer[len(f.Buffer)-hold:]
				} else {
					f.Buffer = ""
				}
				break
			}
			f.InObservation = false
			f.Buffer = f.Buffer[end+len("</observation>"):]
			continue
		}
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
			payload := f.ToolCallBuffer + f.Buffer[:end]
			if !f.appendToolCall(payload) {
				visible.WriteString(toolCallStart)
				visible.WriteString(payload)
				visible.WriteString(toolCallEnd)
			}
			f.ToolCallBuffer = ""
			f.InToolCall = false
			f.Buffer = f.Buffer[end+len(toolCallEnd):]
			continue
		}
		start, marker := firstToolProtocolMarker(f.Buffer)
		if start == -1 {
			hold := partialToolProtocolSuffixLen(f.Buffer)
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
		switch marker {
		case toolCallStart:
			f.Buffer = f.Buffer[start+len(toolCallStart):]
			f.InToolCall = true
		case "<observation>":
			f.Buffer = f.Buffer[start+len("<observation>"):]
			f.InObservation = true
		case "[Tool Result:":
			f.Buffer = f.Buffer[start+len("[Tool Result:"):]
			f.InBracketToolResult = true
		default:
			f.Buffer = f.Buffer[start:]
			visible.WriteString(f.Buffer)
			f.Buffer = ""
		}
	}
	return visible.String()
}

func (f *toolCallTextFilter) Finish() (string, error) {
	if f.SkipToolResultObject {
		if !f.consumeToolResultObjectAtFinish() {
			f.ToolResultBuffer = ""
		}
	}
	if f.InBracketToolResult {
		f.InBracketToolResult = false
		f.BracketResultBuffer = ""
	}
	if f.InObservation {
		f.InObservation = false
		f.Buffer = ""
	}
	if f.InToolCall {
		payload := f.ToolCallBuffer + f.Buffer
		if f.appendToolCall(payload) {
			f.Buffer = ""
			f.ToolCallBuffer = ""
			f.InToolCall = false
			return "", nil
		}
		f.Buffer = ""
		f.ToolCallBuffer = ""
		f.InToolCall = false
		return "", fmt.Errorf("stream ended with incomplete text tool call")
	}
	visible := f.Buffer
	f.Buffer = ""
	return visible, nil
}

func (f *toolCallTextFilter) consumeBracketToolResultHeader() bool {
	newline := strings.IndexByte(f.Buffer, '\n')
	if newline == -1 {
		f.BracketResultBuffer += f.Buffer
		f.Buffer = ""
		return false
	}
	f.BracketResultBuffer += f.Buffer[:newline+1]
	f.Buffer = f.Buffer[newline+1:]
	f.InBracketToolResult = false
	f.BracketResultBuffer = ""
	f.SkipToolResultObject = true
	return f.consumeToolResultObject()
}

func (f *toolCallTextFilter) consumeToolResultObject() bool {
	f.ToolResultBuffer += f.Buffer
	f.Buffer = ""
	remaining, complete := stripLeadingJSONObject(f.ToolResultBuffer)
	if !complete {
		return false
	}
	f.ToolResultBuffer = ""
	f.Buffer = remaining
	f.SkipToolResultObject = false
	return true
}

func (f *toolCallTextFilter) consumeToolResultObjectAtFinish() bool {
	remaining, complete := stripLeadingJSONObject(f.ToolResultBuffer + f.Buffer)
	if !complete {
		return false
	}
	f.ToolResultBuffer = ""
	f.Buffer = remaining
	f.SkipToolResultObject = false
	return true
}

func (f *toolCallTextFilter) appendToolCall(payload string) bool {
	call, err := textToolCallToOpenAI(payload, len(f.ToolCalls))
	if err != nil {
		return false
	}
	f.ToolCalls = append(f.ToolCalls, call)
	return true
}

func extractTextToolCalls(content string) (string, []toolCall) {
	filter := &toolCallTextFilter{}
	visible := filter.Feed(content)
	tail, err := filter.Finish()
	if err != nil {
		return content, nil
	}
	visible += tail
	return visible, filter.ToolCalls
}

func firstToolProtocolMarker(text string) (int, string) {
	markers := []string{toolCallStart, "<observation>", "[Tool Result:"}
	best := -1
	bestMarker := ""
	for _, marker := range markers {
		index := strings.Index(text, marker)
		if index == -1 {
			continue
		}
		if best == -1 || index < best {
			best = index
			bestMarker = marker
		}
	}
	return best, bestMarker
}

func partialToolProtocolSuffixLen(text string) int {
	markers := []string{toolCallStart, "<observation>", "[Tool Result:"}
	best := 0
	for _, marker := range markers {
		if n := partialTagSuffixLen(text, marker); n > best {
			best = n
		}
	}
	return best
}

func stripLeadingJSONObject(text string) (string, bool) {
	trimmed := strings.TrimLeft(text, " \t\r\n")
	consumedPrefix := len(text) - len(trimmed)
	if trimmed == "" {
		return "", false
	}
	if trimmed[0] != '{' {
		return text[consumedPrefix:], true
	}
	inString := false
	escaped := false
	depth := 0
	for i, r := range trimmed {
		if escaped {
			escaped = false
			continue
		}
		if r == '\\' {
			escaped = true
			continue
		}
		if r == '"' {
			inString = !inString
			continue
		}
		if inString {
			continue
		}
		switch r {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return trimmed[i+1:], true
			}
		}
	}
	return "", false
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

func executeToolCall(call toolCall, root string, opts ...options) string {
	return executeToolCallContext(context.Background(), call, root, opts...)
}

func executeToolCallContext(ctx context.Context, call toolCall, root string, opts ...options) string {
	return newToolRunner(root, firstOptions(opts...)).Execute(ctx, call)
}

type toolRunner struct {
	root       string
	opts       options
	generation int
	cache      map[string]string
	mu         sync.Mutex
}

func newToolRunner(root string, opts options) *toolRunner {
	return &toolRunner{
		root:  root,
		opts:  opts,
		cache: make(map[string]string),
	}
}

func firstOptions(opts ...options) options {
	if len(opts) == 0 {
		return options{}
	}
	return opts[0]
}

type preparedToolCall struct {
	Call      toolCall
	Name      string
	Args      map[string]any
	ReadOnly  bool
	Cacheable bool
	CacheKey  string
	Content   string
	Result    map[string]any
	Ready     bool
}

type toolExecution struct {
	Call    toolCall
	Name    string
	Args    map[string]any
	Content string
	Result  map[string]any
}

func (r *toolRunner) Execute(ctx context.Context, call toolCall) string {
	results := r.ExecuteCalls(ctx, []toolCall{call})
	if len(results) == 0 {
		return mustJSON(map[string]any{"ok": false, "error": "tool execution returned no result"})
	}
	return results[0].Content
}

func (r *toolRunner) Generation() int {
	if r == nil {
		return 0
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.generation
}

func (r *toolRunner) ExecuteCalls(ctx context.Context, calls []toolCall) []toolExecution {
	if len(calls) == 0 {
		return nil
	}
	if r == nil {
		out := make([]toolExecution, len(calls))
		for i, call := range calls {
			result := executeToolCallDirectResult(ctx, call, "", options{}, nil)
			printToolCallBatchStatus([]toolExecution{{Call: call, Name: call.Function.Name, Args: nil, Result: result, Content: mustJSON(result)}})
			out[i] = toolExecution{Call: call, Name: call.Function.Name, Content: mustJSON(result), Result: result}
		}
		return out
	}
	prepared := make([]preparedToolCall, len(calls))
	for i, call := range calls {
		prepared[i] = r.prepare(ctx, call)
	}
	results := make([]toolExecution, len(prepared))
	for i := 0; i < len(prepared); {
		if prepared[i].Ready {
			exec := prepared[i].execution()
			printToolCallBatchStatus([]toolExecution{exec})
			results[i] = exec
			i++
			continue
		}
		end := i + 1
		if prepared[i].ReadOnly {
			for end < len(prepared) && prepared[end].ReadOnly && !prepared[end].Ready {
				end++
			}
		}
		batch := r.executePreparedBatch(ctx, prepared[i:end])
		for offset, exec := range batch {
			results[i+offset] = exec
		}
		i = end
	}
	return results
}

type toolLoopDetector struct {
	repeatLimit int
	lastKey     string
	count       int
}

func newToolLoopDetector(repeatLimit int) *toolLoopDetector {
	if repeatLimit < 2 {
		repeatLimit = 2
	}
	return &toolLoopDetector{repeatLimit: repeatLimit}
}

func (d *toolLoopDetector) Observe(calls []toolCall, generation int) error {
	if d == nil || len(calls) == 0 {
		return nil
	}
	signature := toolCallBatchSignature(calls)
	key := strconv.Itoa(generation) + "\x00" + signature
	if key == d.lastKey {
		d.count++
	} else {
		d.lastKey = key
		d.count = 1
	}
	if d.count < d.repeatLimit {
		return nil
	}
	return fmt.Errorf(
		"possible tool-call loop detected: identical tool batch repeated %d times without file changes (%s)",
		d.count,
		compactPreview(describeToolBatch(calls), 240),
	)
}

func toolCallBatchSignature(calls []toolCall) string {
	parts := make([]string, 0, len(calls))
	for _, call := range calls {
		parts = append(parts, toolCallSignature(call))
	}
	return strings.Join(parts, "\n")
}

func toolCallSignature(call toolCall) string {
	name, args, ok := normalizedToolCallForLoop(call)
	if !ok {
		return strings.TrimSpace(call.Function.Name) + "\x00invalid:" + call.Function.Arguments
	}
	return name + "\x00" + mustJSON(args)
}

func describeToolBatch(calls []toolCall) string {
	parts := make([]string, 0, len(calls))
	for _, call := range calls {
		name, args, ok := normalizedToolCallForLoop(call)
		if !ok {
			parts = append(parts, strings.TrimSpace(call.Function.Name)+" invalid arguments")
			continue
		}
		if argText := formatToolArgs(args); argText != "{}" {
			parts = append(parts, name+" "+argText)
		} else {
			parts = append(parts, name)
		}
	}
	return strings.Join(parts, "; ")
}

func normalizedToolCallForLoop(call toolCall) (string, map[string]any, bool) {
	name := strings.TrimSpace(call.Function.Name)
	args := map[string]any{}
	if err := json.Unmarshal([]byte(call.Function.Arguments), &args); err != nil {
		return name, nil, false
	}
	if isExecTool(name) {
		if path, ok := simpleCatPath(args); ok {
			name = "read_file"
			args = map[string]any{"path": path}
		}
	}
	return name, canonicalToolLoopArgs(args), true
}

func canonicalToolLoopArgs(args map[string]any) map[string]any {
	out := make(map[string]any, len(args))
	for key, value := range args {
		if key == "intent" {
			continue
		}
		out[key] = value
	}
	return out
}

func (p preparedToolCall) execution() toolExecution {
	return toolExecution{
		Call:    p.Call,
		Name:    p.Name,
		Args:    p.Args,
		Content: p.Content,
		Result:  p.Result,
	}
}

func (r *toolRunner) prepare(ctx context.Context, call toolCall) preparedToolCall {
	args := map[string]any{}
	if err := json.Unmarshal([]byte(call.Function.Arguments), &args); err != nil {
		result := map[string]any{"ok": false, "error": err.Error()}
		return preparedToolCall{
			Call:    call,
			Name:    call.Function.Name,
			Args:    args,
			Content: mustJSON(result),
			Result:  result,
			Ready:   true,
		}
	}
	if ctx != nil && ctx.Err() != nil {
		result := map[string]any{"ok": false, "error": ctx.Err().Error()}
		return preparedToolCall{Call: call, Name: call.Function.Name, Args: args, Content: mustJSON(result), Result: result, Ready: true}
	}
	prepared := preparedToolCall{
		Call: call,
		Name: call.Function.Name,
		Args: args,
	}
	r.mu.Lock()
	if key, resultArgs, ok := r.cacheKey(call.Function.Name, args); ok {
		if cached, found := r.cache[key]; found {
			result := decodeToolResult(cached)
			r.mu.Unlock()
			return preparedToolCall{Call: call, Name: "read_file", Args: resultArgs, ReadOnly: true, Cacheable: true, CacheKey: key, Content: cached, Result: result, Ready: true}
		}
		r.mu.Unlock()
		prepared.Name = "read_file"
		prepared.Args = resultArgs
		prepared.ReadOnly = true
		prepared.Cacheable = true
		prepared.CacheKey = key
		return prepared
	}
	if key, ok := r.cacheKeyOnly(call.Function.Name, args); ok {
		if cached, found := r.cache[key]; found {
			result := decodeToolResult(cached)
			r.mu.Unlock()
			return preparedToolCall{Call: call, Name: call.Function.Name, Args: args, ReadOnly: true, Cacheable: true, CacheKey: key, Content: cached, Result: result, Ready: true}
		}
		r.mu.Unlock()
		prepared.ReadOnly = true
		prepared.Cacheable = true
		prepared.CacheKey = key
		return prepared
	}
	r.mu.Unlock()
	prepared.ReadOnly = toolIsReadOnly(call.Function.Name)
	return prepared
}

func (r *toolRunner) executePreparedBatch(ctx context.Context, batch []preparedToolCall) []toolExecution {
	if len(batch) == 0 {
		return nil
	}
	progress := newToolBatchProgress(r.opts, batch)
	progress.Start()
	defer progress.StopAndPrint()

	results := make([]toolExecution, len(batch))
	leaderFor := make([]int, len(batch))
	leaderByKey := map[string]int{}
	for i := range batch {
		leaderFor[i] = i
		if !batch[i].Cacheable || batch[i].CacheKey == "" {
			continue
		}
		if leader, ok := leaderByKey[batch[i].CacheKey]; ok {
			leaderFor[i] = leader
			continue
		}
		leaderByKey[batch[i].CacheKey] = i
	}

	var wg sync.WaitGroup
	for i, item := range batch {
		if leaderFor[i] != i {
			continue
		}
		wg.Add(1)
		go func(index int, prepared preparedToolCall) {
			defer wg.Done()
			progress.MarkRunning(index)
			call := prepared.Call
			call.Function.Name = prepared.Name
			result := executeToolCallDirectResult(ctx, call, r.root, r.opts, prepared.Args)
			content := mustJSON(result)
			exec := toolExecution{Call: prepared.Call, Name: prepared.Name, Args: prepared.Args, Content: content, Result: result}
			results[index] = exec
			if prepared.Cacheable && prepared.CacheKey != "" {
				r.storeCached(prepared.CacheKey, content)
			}
			if toolMayMutate(prepared.Name) {
				r.invalidateCache()
			}
			progress.MarkDone(index, result)
		}(i, item)
	}
	wg.Wait()
	for i, leader := range leaderFor {
		if i == leader {
			continue
		}
		results[i] = results[leader]
		results[i].Call = batch[i].Call
		results[i].Name = batch[i].Name
		results[i].Args = batch[i].Args
		progress.MarkCached(i, results[i].Result)
	}
	return results
}

func (r *toolRunner) invalidateCache() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.generation++
	r.cache = make(map[string]string)
}

func (r *toolRunner) storeCached(key string, result string) {
	if r == nil || key == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.cache[key] = result
}

func (r *toolRunner) cacheKey(name string, args map[string]any) (string, map[string]any, bool) {
	if r == nil {
		return "", nil, false
	}
	if isExecTool(name) {
		if path, ok := simpleCatPath(args); ok {
			readArgs := map[string]any{"path": path}
			if intent := toolCallIntent(name, args); intent != "" {
				readArgs["intent"] = intent
			}
			key, ok := r.readFileCacheKey(readArgs)
			return key, readArgs, ok
		}
	}
	return "", nil, false
}

func (r *toolRunner) cacheKeyOnly(name string, args map[string]any) (string, bool) {
	switch name {
	case "read_file":
		return r.readFileCacheKey(args)
	case "list_files":
		path := firstStringArg(args, "path")
		if path == "" {
			path = "."
		}
		return r.pathCacheKey("list_files", path)
	case "search_files", "grep_search", "rg":
		path := firstStringArg(args, "path", "dir", "workdir")
		if path == "" {
			path = "."
		}
		target, err := resolveToolPath(r.root, path)
		if err != nil {
			return "", false
		}
		rel := relativeToolPath(r.root, target)
		parts := []string{
			fmt.Sprintf("gen=%d", r.generation),
			"search_files",
			rel,
			firstStringArg(args, "pattern", "query"),
			firstStringArg(args, "glob"),
			strconv.Itoa(intArg(args, "limit", 200)),
		}
		return strings.Join(parts, "\x00"), true
	default:
		return "", false
	}
}

func (r *toolRunner) readFileCacheKey(args map[string]any) (string, bool) {
	path := firstStringArg(args, "path")
	if strings.TrimSpace(path) == "" {
		return "", false
	}
	return r.pathCacheKey("read_file", path)
}

func (r *toolRunner) pathCacheKey(kind string, path string) (string, bool) {
	target, err := resolveToolPath(r.root, path)
	if err != nil {
		return "", false
	}
	return fmt.Sprintf("gen=%d\x00%s\x00%s", r.generation, kind, relativeToolPath(r.root, target)), true
}

func simpleCatPath(args map[string]any) (string, bool) {
	command := strings.TrimSpace(firstStringArg(args, "cmd", "command"))
	if command == "" {
		return "", false
	}
	fields := strings.Fields(command)
	if len(fields) == 3 && fields[0] == "cat" && fields[1] == "--" {
		fields = []string{fields[0], fields[2]}
	}
	if len(fields) != 2 || fields[0] != "cat" || strings.HasPrefix(fields[1], "-") {
		return "", false
	}
	path := strings.Trim(fields[1], `"'`)
	if path == "" || strings.ContainsAny(path, "|;&<>`$") {
		return "", false
	}
	if workdir := firstStringArg(args, "workdir", "cwd"); workdir != "" && !filepath.IsAbs(path) {
		path = filepath.Join(workdir, path)
	}
	return path, true
}

func isExecTool(name string) bool {
	return name == "exec_command" || name == "shell" || name == "run_shell"
}

func toolIsReadOnly(name string) bool {
	switch name {
	case "read_file", "list_files", "search_files", "grep_search", "rg":
		return true
	default:
		return false
	}
}

func toolMayMutate(name string) bool {
	return name == "write_file" || name == "apply_patch" || isExecTool(name)
}

func executeToolCallDirect(ctx context.Context, call toolCall, root string, opts options, args map[string]any) string {
	if args == nil {
		args = map[string]any{}
		if err := json.Unmarshal([]byte(call.Function.Arguments), &args); err != nil {
			result := map[string]any{"ok": false, "error": err.Error()}
			printToolCallStatus(call.Function.Name, args, result)
			return mustJSON(result)
		}
	}
	result := executeToolCallDirectResult(ctx, call, root, opts, args)
	printToolCallStatus(call.Function.Name, args, result)
	return mustJSON(result)
}

func executeToolCallDirectResult(ctx context.Context, call toolCall, root string, opts options, args map[string]any) map[string]any {
	if args == nil {
		args = map[string]any{}
		if err := json.Unmarshal([]byte(call.Function.Arguments), &args); err != nil {
			return map[string]any{"ok": false, "error": err.Error()}
		}
	}
	if root == "" {
		return map[string]any{"ok": false, "error": "tool root is required"}
	}
	result := map[string]any{}
	switch call.Function.Name {
	case "write_file":
		result = toolWriteFile(root, args)
	case "read_file":
		result = toolReadFile(root, args)
	case "list_files":
		result = toolListFiles(root, args)
	case "search_files", "grep_search", "rg":
		result = toolSearchFiles(ctx, root, args)
	case "exec_command", "shell", "run_shell":
		result = toolExecCommand(ctx, root, args)
	case "apply_patch":
		result = toolApplyPatch(ctx, root, args)
	default:
		result = map[string]any{"ok": false, "error": "unknown tool: " + call.Function.Name}
	}
	return result
}

func decodeToolResult(raw string) map[string]any {
	result := map[string]any{}
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		return map[string]any{"ok": false, "error": "invalid cached tool result: " + err.Error()}
	}
	return result
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

func toolSearchFiles(ctx context.Context, root string, args map[string]any) map[string]any {
	pattern := firstStringArg(args, "pattern", "query")
	path := firstStringArg(args, "path", "dir", "workdir")
	if path == "" {
		path = "."
	}
	if strings.TrimSpace(pattern) == "" {
		return map[string]any{"ok": false, "error": "search_files.pattern is required"}
	}
	target, err := resolveToolPath(root, path)
	if err != nil {
		return map[string]any{"ok": false, "error": err.Error()}
	}
	rgPath, err := exec.LookPath("rg")
	if err != nil {
		return map[string]any{"ok": false, "error": "rg is required for search_files"}
	}
	limit := intArg(args, "limit", 200)
	if limit < 1 {
		limit = 200
	}
	if limit > 1000 {
		limit = 1000
	}
	cmdArgs := []string{"--line-number", "--no-heading", "--color=never", "--hidden"}
	if glob := firstStringArg(args, "glob"); glob != "" {
		cmdArgs = append(cmdArgs, "--glob", glob)
	}
	cmdArgs = append(cmdArgs, pattern, target)
	ctx, cancel := context.WithTimeout(ctx, time.Duration(intArg(args, "timeout_ms", 30_000))*time.Millisecond)
	defer cancel()
	cmd := exec.CommandContext(ctx, rgPath, cmdArgs...)
	out, err := cmd.CombinedOutput()
	exitCode := commandExitCode(err)
	text, truncated := truncateText(string(out), 128*1024)
	lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
	if len(lines) > limit {
		lines = lines[:limit]
		truncated = true
	}
	if exitCode == 1 {
		return map[string]any{"ok": true, "path": relativeToolPath(root, target), "matches": []string{}, "truncated": false}
	}
	return map[string]any{"ok": exitCode == 0, "path": relativeToolPath(root, target), "matches": nonEmptyLines(lines), "exit_code": exitCode, "truncated": truncated}
}

func toolExecCommand(ctx context.Context, root string, args map[string]any) map[string]any {
	command := firstStringArg(args, "cmd", "command")
	if strings.TrimSpace(command) == "" {
		return map[string]any{"ok": false, "error": "exec_command.cmd is required"}
	}
	workdir := firstStringArg(args, "workdir", "cwd")
	if workdir == "" {
		workdir = "."
	}
	cwd, err := resolveToolPath(root, workdir)
	if err != nil {
		return map[string]any{"ok": false, "error": err.Error()}
	}
	timeout := intArg(args, "timeout_ms", 120_000)
	if timeout < 1 {
		timeout = 120_000
	}
	if timeout > 600_000 {
		timeout = 600_000
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Millisecond)
	defer cancel()
	start := time.Now()
	cmd := exec.CommandContext(ctx, "/bin/sh", "-lc", command)
	cmd.Dir = cwd
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err = cmd.Run()
	exitCode := commandExitCode(err)
	if ctx.Err() == context.DeadlineExceeded {
		exitCode = -1
	}
	stdoutText, stdoutTruncated := truncateText(stdout.String(), 128*1024)
	stderrText, stderrTruncated := truncateText(stderr.String(), 128*1024)
	return map[string]any{
		"ok":          err == nil,
		"cmd":         command,
		"workdir":     relativeToolPath(root, cwd),
		"exit_code":   exitCode,
		"duration_ms": time.Since(start).Milliseconds(),
		"stdout":      stdoutText,
		"stderr":      stderrText,
		"truncated":   stdoutTruncated || stderrTruncated,
	}
}

func toolApplyPatch(ctx context.Context, root string, args map[string]any) map[string]any {
	patch := firstStringArg(args, "patch", "input")
	if strings.TrimSpace(patch) == "" {
		return map[string]any{"ok": false, "error": "apply_patch.patch is required"}
	}
	applyPatchPath, err := exec.LookPath("apply_patch")
	if err != nil {
		return toolGitApplyPatch(ctx, root, args, patch)
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(intArg(args, "timeout_ms", 120_000))*time.Millisecond)
	defer cancel()
	start := time.Now()
	cmd := exec.CommandContext(ctx, applyPatchPath)
	cmd.Dir = root
	cmd.Stdin = strings.NewReader(patch)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err = cmd.Run()
	stdoutText, stdoutTruncated := truncateText(stdout.String(), 128*1024)
	stderrText, stderrTruncated := truncateText(stderr.String(), 128*1024)
	return map[string]any{
		"ok":          err == nil,
		"exit_code":   commandExitCode(err),
		"duration_ms": time.Since(start).Milliseconds(),
		"stdout":      stdoutText,
		"stderr":      stderrText,
		"patch_lines": len(strings.Split(strings.TrimRight(patch, "\n"), "\n")),
		"truncated":   stdoutTruncated || stderrTruncated,
	}
}

func toolGitApplyPatch(ctx context.Context, root string, args map[string]any, patch string) map[string]any {
	if !strings.Contains(patch, "diff --git") && !strings.HasPrefix(strings.TrimSpace(patch), "--- ") {
		return map[string]any{"ok": false, "error": "apply_patch command is not available"}
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(intArg(args, "timeout_ms", 120_000))*time.Millisecond)
	defer cancel()
	start := time.Now()
	cmd := exec.CommandContext(ctx, "git", "apply", "--whitespace=nowarn", "-")
	cmd.Dir = root
	cmd.Stdin = strings.NewReader(patch)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	stdoutText, stdoutTruncated := truncateText(stdout.String(), 128*1024)
	stderrText, stderrTruncated := truncateText(stderr.String(), 128*1024)
	return map[string]any{
		"ok":          err == nil,
		"exit_code":   commandExitCode(err),
		"duration_ms": time.Since(start).Milliseconds(),
		"stdout":      stdoutText,
		"stderr":      stderrText,
		"patch_lines": len(strings.Split(strings.TrimRight(patch, "\n"), "\n")),
		"fallback":    "git apply",
		"truncated":   stdoutTruncated || stderrTruncated,
	}
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

type toolProgressState string

const (
	toolProgressPending toolProgressState = "pending"
	toolProgressRunning toolProgressState = "running"
	toolProgressDone    toolProgressState = "done"
	toolProgressFailed  toolProgressState = "failed"
	toolProgressCached  toolProgressState = "cached"
)

type toolProgressItem struct {
	Name   string
	Args   map[string]any
	State  toolProgressState
	Result map[string]any
}

type toolBatchProgress struct {
	opts      options
	items     []toolProgressItem
	enabled   bool
	frames    []string
	interval  time.Duration
	startedAt time.Time
	stopCh    chan struct{}
	doneCh    chan struct{}
	running   bool
	mu        sync.Mutex
}

func newToolBatchProgress(opts options, batch []preparedToolCall) *toolBatchProgress {
	frames := bubblesSpinner.MiniDot.Frames
	if len(frames) == 0 {
		frames = []string{"|", "/", "-", "\\"}
	}
	items := make([]toolProgressItem, len(batch))
	for i, item := range batch {
		items[i] = toolProgressItem{
			Name:  item.Name,
			Args:  item.Args,
			State: toolProgressPending,
		}
	}
	return &toolBatchProgress{
		opts:     opts,
		items:    items,
		enabled:  opts.Spinner && isInteractiveTerminal(os.Stderr),
		frames:   frames,
		interval: 100 * time.Millisecond,
	}
}

func (p *toolBatchProgress) Start() {
	if p == nil || !p.enabled {
		return
	}
	p.mu.Lock()
	if p.running {
		p.mu.Unlock()
		return
	}
	p.startedAt = time.Now()
	p.stopCh = make(chan struct{})
	p.doneCh = make(chan struct{})
	p.running = true
	p.mu.Unlock()

	go func() {
		defer close(p.doneCh)
		ticker := time.NewTicker(p.interval)
		defer ticker.Stop()
		frame := 0
		p.render(frame)
		for {
			select {
			case <-ticker.C:
				frame++
				p.render(frame)
			case <-p.stopCh:
				clearStatusLine()
				return
			}
		}
	}()
}

func (p *toolBatchProgress) MarkRunning(index int) {
	p.setState(index, toolProgressRunning, nil)
}

func (p *toolBatchProgress) MarkDone(index int, result map[string]any) {
	state := toolProgressDone
	if ok, _ := result["ok"].(bool); !ok {
		state = toolProgressFailed
	}
	p.setState(index, state, result)
}

func (p *toolBatchProgress) MarkCached(index int, result map[string]any) {
	p.setState(index, toolProgressCached, result)
}

func (p *toolBatchProgress) setState(index int, state toolProgressState, result map[string]any) {
	if p == nil || index < 0 || index >= len(p.items) {
		return
	}
	p.mu.Lock()
	p.items[index].State = state
	p.items[index].Result = result
	p.mu.Unlock()
}

func (p *toolBatchProgress) StopAndPrint() {
	if p == nil {
		return
	}
	if p.enabled {
		p.mu.Lock()
		if p.running {
			stopCh := p.stopCh
			doneCh := p.doneCh
			p.running = false
			p.mu.Unlock()
			close(stopCh)
			<-doneCh
		} else {
			p.mu.Unlock()
		}
	}
	p.mu.Lock()
	execs := make([]toolExecution, len(p.items))
	for i, item := range p.items {
		result := item.Result
		if result == nil {
			result = map[string]any{"ok": false, "error": "tool result was not recorded"}
		}
		execs[i] = toolExecution{Name: item.Name, Args: item.Args, Result: result, Content: mustJSON(result)}
	}
	p.mu.Unlock()
	printToolCallBatchStatus(execs)
}

func (p *toolBatchProgress) render(frame int) {
	p.mu.Lock()
	items := append([]toolProgressItem(nil), p.items...)
	startedAt := p.startedAt
	frames := append([]string(nil), p.frames...)
	p.mu.Unlock()
	if len(frames) == 0 {
		return
	}
	done := 0
	running := 0
	for _, item := range items {
		switch item.State {
		case toolProgressDone, toolProgressFailed, toolProgressCached:
			done++
		case toolProgressRunning:
			running++
		}
	}
	label := fmt.Sprintf("Tools %d/%d", done, len(items))
	meta := []string{}
	if !startedAt.IsZero() {
		meta = append(meta, formatElapsed(time.Since(startedAt)))
	}
	if running > 0 {
		meta = append(meta, fmt.Sprintf("%d running", running))
	}
	if names := compactToolProgressNames(items); names != "" {
		meta = append(meta, names)
	}
	meta = append(meta, "Esc/Ctrl+C to stop")
	fmt.Fprintf(os.Stderr, "\r%s%s %s", ansi.EraseLineRight, spinnerFrameStyle.Render(frames[frame%len(frames)]), renderSpinnerLabel(label, meta, frame))
}

func compactToolProgressNames(items []toolProgressItem) string {
	if len(items) == 0 {
		return ""
	}
	parts := make([]string, 0, len(items))
	for _, item := range items {
		prefix := "○"
		switch item.State {
		case toolProgressRunning:
			prefix = "…"
		case toolProgressDone:
			prefix = "✓"
		case toolProgressFailed:
			prefix = "!"
		case toolProgressCached:
			prefix = "↺"
		}
		parts = append(parts, prefix+" "+plainToolTitle(item.Name, item.Args))
	}
	text := strings.Join(parts, ", ")
	if len([]rune(text)) > 96 {
		return string([]rune(text)[:93]) + "..."
	}
	return text
}

func printToolCallBatchStatus(execs []toolExecution) {
	if len(execs) == 0 {
		return
	}
	if len(execs) == 1 {
		printToolCallStatus(execs[0].Name, execs[0].Args, execs[0].Result)
		return
	}
	done, failed := 0, 0
	for _, exec := range execs {
		if ok, _ := exec.Result["ok"].(bool); ok {
			done++
		} else {
			failed++
		}
	}
	status := toolOKStyle.Render(fmt.Sprintf("%d done", done))
	if failed > 0 {
		status = toolErrorStyle.Render(fmt.Sprintf("%d failed", failed))
	}
	fmt.Fprintf(os.Stderr, "%s %s %s\n", faintStyle.Render("•"), toolVerbStyle.Render("Tools"), status)
	grouped := groupToolExecutions(execs)
	for i, group := range grouped {
		branch := "├"
		detailPrefix := "│ "
		if i == len(grouped)-1 {
			branch = "└"
			detailPrefix = "  "
		}
		count := ""
		if group.Count > 1 {
			count = fmt.Sprintf(" x%d", group.Count)
		}
		ok, _ := group.Result["ok"].(bool)
		outcome := toolOKStyle.Render("done")
		if !ok {
			outcome = toolErrorStyle.Render("failed")
		}
		fmt.Fprintf(os.Stderr, "  %s %s%s %s %s\n", faintStyle.Render(branch), toolVerbStyle.Render(toolVerb(group.Name)), count, toolSummary(group.Name, group.Args, group.Result), outcome)
		if intent := toolCallIntent(group.Name, group.Args); intent != "" {
			fmt.Fprintf(os.Stderr, "  %s %s\n", faintStyle.Render(detailPrefix+" intent:"), intent)
		}
		fmt.Fprintf(os.Stderr, "  %s %s\n", faintStyle.Render(detailPrefix+" result:"), toolResultSummary(group.Result))
	}
}

type groupedToolExecution struct {
	Name   string
	Args   map[string]any
	Result map[string]any
	Count  int
}

func groupToolExecutions(execs []toolExecution) []groupedToolExecution {
	groups := []groupedToolExecution{}
	seen := map[string]int{}
	for _, exec := range execs {
		key := exec.Name + "\x00" + formatToolArgs(exec.Args) + "\x00" + toolResultSummary(exec.Result)
		if index, ok := seen[key]; ok {
			groups[index].Count++
			continue
		}
		seen[key] = len(groups)
		groups = append(groups, groupedToolExecution{Name: exec.Name, Args: exec.Args, Result: exec.Result, Count: 1})
	}
	return groups
}

func plainToolTitle(name string, args map[string]any) string {
	title := strings.TrimSpace(toolVerb(name) + " " + ansi.Strip(toolSummary(name, args, nil)))
	if title == "" {
		return name
	}
	return title
}

func printToolCallStatus(name string, args map[string]any, result map[string]any) {
	ok, _ := result["ok"].(bool)
	status := toolErrorStyle.Render("failed")
	if ok {
		status = toolOKStyle.Render("done")
	}
	fmt.Fprintf(os.Stderr, "%s %s %s\n", faintStyle.Render("•"), toolVerbStyle.Render(toolVerb(name)), toolSummary(name, args, result))
	if intent := toolCallIntent(name, args); intent != "" {
		fmt.Fprintf(os.Stderr, "  %s %s\n", faintStyle.Render("└ intent:"), intent)
	}
	fmt.Fprintf(os.Stderr, "  %s %s %s\n", faintStyle.Render("└"), status, toolResultSummary(result))
	if argText := formatToolArgs(args); argText != "{}" {
		fmt.Fprintf(os.Stderr, "  %s %s\n", faintStyle.Render("└ args:"), argText)
	}
}

func toolCallIntent(name string, args map[string]any) string {
	if intent, _ := args["intent"].(string); strings.TrimSpace(intent) != "" {
		return strings.TrimSpace(intent)
	}
	path := firstStringArg(args, "path", "workdir", "cwd")
	if path != "" {
		switch name {
		case "write_file":
			return "write requested file " + path
		case "read_file":
			return "inspect file " + path
		case "list_files":
			return "list files under " + path
		case "search_files", "grep_search", "rg":
			return "search files under " + path
		}
	}
	if cmd := firstStringArg(args, "cmd", "command"); cmd != "" {
		return "run shell command"
	}
	if name == "apply_patch" {
		return "apply requested patch"
	}
	return "run " + name
}

func toolVerb(name string) string {
	switch name {
	case "write_file":
		return "Write"
	case "read_file":
		return "Read"
	case "list_files":
		return "List"
	case "search_files", "grep_search", "rg":
		return "Search"
	case "exec_command", "shell", "run_shell":
		return "Run"
	case "apply_patch":
		return "Patch"
	default:
		return "Tool"
	}
}

func toolSummary(name string, args map[string]any, result map[string]any) string {
	switch name {
	case "write_file", "read_file":
		return dim(firstStringArg(args, "path"))
	case "list_files":
		path := firstStringArg(args, "path")
		if path == "" {
			path = "."
		}
		return dim(path)
	case "search_files", "grep_search", "rg":
		pattern := firstStringArg(args, "pattern", "query")
		path := firstStringArg(args, "path", "dir", "workdir")
		if path == "" {
			path = "."
		}
		return fmt.Sprintf("%s in %s", shellQuote(pattern), dim(path))
	case "exec_command", "shell", "run_shell":
		return shellQuote(firstStringArg(args, "cmd", "command"))
	case "apply_patch":
		if n, ok := numberToInt(result["patch_lines"]); ok && n > 0 {
			return dim(fmt.Sprintf("%d lines", n))
		}
		return dim("patch")
	default:
		return dim(name)
	}
}

func toolSpinnerLabel(name string, args map[string]any) string {
	label := toolVerb(name)
	if command := firstStringArg(args, "cmd", "command"); command != "" {
		return label + " " + compactPreview(command, 72)
	}
	if pattern := firstStringArg(args, "pattern", "query"); pattern != "" {
		path := firstStringArg(args, "path", "dir", "workdir")
		if path == "" {
			path = "."
		}
		return label + " " + compactPreview(pattern+" in "+path, 72)
	}
	if path := firstStringArg(args, "path", "workdir", "cwd"); path != "" {
		return label + " " + compactPreview(path, 72)
	}
	return label
}

func toolResultSummary(result map[string]any) string {
	if errText, _ := result["error"].(string); errText != "" {
		return dim(errText)
	}
	parts := []string{}
	if code, ok := numberToInt(result["exit_code"]); ok {
		parts = append(parts, fmt.Sprintf("exit=%d", code))
	}
	if n, ok := numberToInt(result["bytes"]); ok {
		parts = append(parts, formatBytes(n))
	}
	if n, ok := numberToInt(result["duration_ms"]); ok {
		parts = append(parts, formatDuration(time.Duration(n)*time.Millisecond))
	}
	if len(parts) == 0 {
		return ""
	}
	return dim(strings.Join(parts, " "))
}

func formatDuration(duration time.Duration) string {
	if duration < time.Second {
		return fmt.Sprintf("%dms", duration.Milliseconds())
	}
	return fmt.Sprintf("%.1fs", duration.Seconds())
}

func dim(text string) string {
	if text == "" {
		return ""
	}
	return faintStyle.Render(text)
}

func shellQuote(text string) string {
	if text == "" {
		return dim("(empty)")
	}
	return toolCommandStyle.Render(text)
}

func formatToolArgs(args map[string]any) string {
	display := map[string]any{}
	for key, value := range args {
		if key == "intent" {
			continue
		}
		if key == "content" || key == "patch" {
			content, _ := value.(string)
			display[key+"_bytes"] = len([]byte(content))
			if preview := compactPreview(content, 96); preview != "" {
				display[key+"_preview"] = preview
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

func firstStringArg(args map[string]any, names ...string) string {
	for _, name := range names {
		if value, _ := args[name].(string); strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func intArg(args map[string]any, name string, fallback int) int {
	if value, ok := numberToInt(args[name]); ok {
		return value
	}
	return fallback
}

func commandExitCode(err error) int {
	if err == nil {
		return 0
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode()
	}
	return -1
}

func truncateText(value string, limit int) (string, bool) {
	if limit <= 0 || len(value) <= limit {
		return value, false
	}
	return value[:limit] + "\n... truncated ...", true
}

func nonEmptyLines(lines []string) []string {
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		if line != "" {
			out = append(out, line)
		}
	}
	return out
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

func printStream(r io.Reader, opts options, spin *statusSpinner) error {
	reader := bufio.NewReader(r)
	renderer := newRenderer(opts)
	emitted := false
	seenDone := false
	finishReason := ""
	for {
		line, err := reader.ReadString('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			stopSpinner(&spin)
			return err
		}
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "data:") {
			data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			if data == "[DONE]" {
				seenDone = true
				break
			}
			if message := streamEventError([]byte(data)); message != "" {
				stopSpinner(&spin)
				return fmt.Errorf("stream error: %s", message)
			}
			if reason := streamFinishReason([]byte(data)); reason != "" {
				finishReason = reason
			}
			if text := streamEventText([]byte(data)); text != "" {
				markStreamProgress(opts, &spin, text)
				emitted = true
				renderer.Feed(text)
			}
		}
		if errors.Is(err, io.EOF) {
			break
		}
	}
	stopSpinner(&spin)
	if emitted {
		renderer.Finish()
	}
	if !seenDone {
		return fmt.Errorf("stream ended before [DONE]")
	}
	if err := finishReasonError(finishReason); err != nil {
		return err
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

func streamFinishReason(data []byte) string {
	var chunk chatStreamChunk
	if json.Unmarshal(data, &chunk) == nil && len(chunk.Choices) > 0 {
		if reason := strings.TrimSpace(chunk.Choices[0].FinishReason); reason != "" {
			return reason
		}
	}
	var raw map[string]any
	if json.Unmarshal(data, &raw) != nil {
		return ""
	}
	if response, _ := raw["response"].(map[string]any); response != nil {
		if status, _ := response["status"].(string); status == "incomplete" {
			if details, _ := response["incomplete_details"].(map[string]any); details != nil {
				if reason, _ := details["reason"].(string); strings.TrimSpace(reason) != "" {
					return reason
				}
			}
			return "incomplete"
		}
	}
	return ""
}

func streamEventError(data []byte) string {
	var raw map[string]any
	if json.Unmarshal(data, &raw) != nil {
		return ""
	}
	errorValue := raw["error"]
	if errorValue == nil && raw["type"] == "error" {
		errorValue = raw["message"]
	}
	switch value := errorValue.(type) {
	case string:
		return strings.TrimSpace(value)
	case map[string]any:
		if message, _ := value["message"].(string); strings.TrimSpace(message) != "" {
			return strings.TrimSpace(message)
		}
		data, _ := json.Marshal(value)
		return string(data)
	}
	return ""
}

func finishReasonError(reason string) error {
	reason = strings.ToLower(strings.TrimSpace(reason))
	switch reason {
	case "", "stop", "end_turn", "tool_calls", "tool_use":
		return nil
	case "length", "max_tokens", "max_output_tokens":
		return fmt.Errorf("stream stopped before completion: %s", reason)
	default:
		return fmt.Errorf("stream stopped before completion: %s", reason)
	}
}

type renderer struct {
	opts   options
	buffer strings.Builder
}

type statusSpinner struct {
	mu          sync.Mutex
	enabled     bool
	label       string
	frames      []string
	interval    time.Duration
	startedAt   time.Time
	outputBytes int
	stopCh      chan struct{}
	doneCh      chan struct{}
	running     bool
}

func newStatusSpinner(opts options, label string) *statusSpinner {
	frames := bubblesSpinner.MiniDot.Frames
	if len(frames) == 0 {
		frames = []string{"|", "/", "-", "\\"}
	}
	interval := bubblesSpinner.MiniDot.FPS
	if interval <= 0 {
		interval = 100 * time.Millisecond
	}
	return &statusSpinner{
		enabled:  opts.Spinner && isInteractiveTerminal(os.Stderr),
		label:    label,
		frames:   frames,
		interval: interval,
	}
}

func (s *statusSpinner) Start() {
	if s == nil || !s.enabled {
		return
	}
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return
	}
	s.stopCh = make(chan struct{})
	s.doneCh = make(chan struct{})
	s.startedAt = time.Now()
	s.outputBytes = 0
	s.running = true
	s.mu.Unlock()

	go func() {
		defer close(s.doneCh)
		ticker := time.NewTicker(s.interval)
		defer ticker.Stop()
		frame := 0
		s.render(frame)
		for {
			select {
			case <-ticker.C:
				frame++
				s.render(frame)
			case <-s.stopCh:
				clearStatusLine()
				return
			}
		}
	}()
}

func (s *statusSpinner) SetLabel(label string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.label = label
	s.mu.Unlock()
}

func (s *statusSpinner) AddText(text string) {
	if s == nil || text == "" {
		return
	}
	s.mu.Lock()
	s.outputBytes += len([]byte(text))
	s.mu.Unlock()
}

func (s *statusSpinner) Stop() {
	if s == nil || !s.enabled {
		return
	}
	s.mu.Lock()
	if !s.running {
		s.mu.Unlock()
		return
	}
	stopCh := s.stopCh
	doneCh := s.doneCh
	s.running = false
	s.mu.Unlock()
	close(stopCh)
	<-doneCh
}

func (s *statusSpinner) render(frame int) {
	s.mu.Lock()
	status := s.label
	frames := append([]string(nil), s.frames...)
	startedAt := s.startedAt
	outputBytes := s.outputBytes
	s.mu.Unlock()
	if len(frames) == 0 {
		return
	}
	meta := []string{}
	if !startedAt.IsZero() {
		meta = append(meta, formatElapsed(time.Since(startedAt)))
	}
	if tokens := estimateOutputTokens(outputBytes); tokens > 0 {
		meta = append(meta, fmt.Sprintf("~%d tok", tokens))
	}
	meta = append(meta, "Esc/Ctrl+C to stop")
	label := renderSpinnerLabel(status, meta, frame)
	fmt.Fprintf(os.Stderr, "\r%s%s %s", ansi.EraseLineRight, spinnerFrameStyle.Render(frames[frame%len(frames)]), label)
}

func renderSpinnerLabel(status string, meta []string, frame int) string {
	parts := make([]string, 0, 2)
	if strings.TrimSpace(status) != "" {
		parts = append(parts, shimmerStatusText(status, frame))
	}
	if len(meta) > 0 {
		parts = append(parts, spinnerLabelStyle.Render(strings.Join(meta, " · ")))
	}
	return strings.Join(parts, spinnerLabelStyle.Render(" · "))
}

func shimmerStatusText(text string, frame int) string {
	runes := []rune(text)
	if len(runes) == 0 {
		return ""
	}
	windowWidth := 6
	cycle := len(runes) + windowWidth*2
	windowStart := positiveModulo(frame, cycle) - windowWidth
	var out strings.Builder
	for i, r := range runes {
		if r == ' ' || r == '\t' {
			out.WriteRune(r)
			continue
		}
		offset := i - windowStart
		switch {
		case offset == 2 || offset == 3:
			out.WriteString(spinnerShimmerHot.Render(string(r)))
		case offset == 1 || offset == 4:
			out.WriteString(spinnerShimmerTrail.Render(string(r)))
		case offset == 0 || offset == 5:
			out.WriteString(spinnerShimmerMid.Render(string(r)))
		default:
			out.WriteString(spinnerShimmerDim.Render(string(r)))
		}
	}
	return out.String()
}

func positiveModulo(value int, modulo int) int {
	if modulo <= 0 {
		return 0
	}
	out := value % modulo
	if out < 0 {
		return out + modulo
	}
	return out
}

func stopSpinner(spin **statusSpinner) {
	if spin == nil || *spin == nil {
		return
	}
	(*spin).Stop()
	*spin = nil
}

func markStreamProgress(opts options, spin **statusSpinner, text string) {
	if spin == nil || *spin == nil {
		return
	}
	(*spin).AddText(text)
	if opts.MarkdownTranslator == "plain" {
		stopSpinner(spin)
		return
	}
	(*spin).SetLabel("Streaming")
}

func estimateOutputTokens(outputBytes int) int {
	if outputBytes <= 0 {
		return 0
	}
	return (outputBytes + 3) / 4
}

func formatElapsed(duration time.Duration) string {
	if duration < 0 {
		duration = 0
	}
	total := int(duration.Round(time.Second).Seconds())
	hours := total / 3600
	minutes := (total % 3600) / 60
	seconds := total % 60
	if hours > 0 {
		return fmt.Sprintf("%d:%02d:%02d", hours, minutes, seconds)
	}
	if minutes > 0 {
		return fmt.Sprintf("%d:%02d", minutes, seconds)
	}
	return fmt.Sprintf("%ds", seconds)
}

func clearStatusLine() {
	fmt.Fprint(os.Stderr, "\r", ansi.EraseLineRight)
}

func isInteractiveTerminal(file *os.File) bool {
	stat, err := file.Stat()
	return err == nil && stat.Mode()&os.ModeCharDevice != 0
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
	rendered, ok = renderWithRich(markdown, opts)
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
		cmd.Env = markdownRendererEnv()
		out, err := cmd.Output()
		if err == nil {
			return string(out), true
		}
	}
	return "", false
}

func renderWithRich(markdown string, opts options) (string, bool) {
	if markdown == "" || opts.MarkdownTranslator != "rich" {
		return "", false
	}
	python := firstAvailableExecutable("python3", "python")
	if python == "" {
		return "", false
	}
	width := envInt("COLUMNS", 100)
	script := `
import os
import sys
try:
    from rich.console import Console
    from rich.markdown import Markdown
except Exception:
    sys.exit(7)

markdown = sys.stdin.read()
theme = os.environ.get("PANGAEA_ASK_RICH_CODE_THEME") or "monokai"
width = int(os.environ.get("COLUMNS") or "100")
console = Console(file=sys.stdout, force_terminal=True, color_system="truecolor", no_color=False, width=width)
console.print(Markdown(markdown, code_theme=theme))
`
	cmd := exec.Command(python, "-c", script)
	cmd.Stdin = strings.NewReader(markdown)
	cmd.Env = append(markdownRendererEnv(),
		"COLUMNS="+strconv.Itoa(width),
		"PANGAEA_ASK_RICH_CODE_THEME="+firstNonEmpty(opts.RichCodeTheme, "monokai"),
	)
	out, err := cmd.Output()
	if err != nil {
		return "", false
	}
	return string(out), true
}

func markdownRendererEnv() []string {
	env := os.Environ()
	out := make([]string, 0, len(env)+3)
	for _, item := range env {
		if strings.HasPrefix(item, "NO_COLOR=") {
			continue
		}
		out = append(out, item)
	}
	if os.Getenv("TERM") == "" || os.Getenv("TERM") == "dumb" {
		out = append(out, "TERM=xterm-256color")
	}
	out = append(out, "CLICOLOR_FORCE=1", "FORCE_COLOR=1")
	return out
}

func firstAvailableExecutable(names ...string) string {
	for _, name := range names {
		if path, err := exec.LookPath(name); err == nil {
			return path
		}
	}
	return ""
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
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

type askConfig struct {
	BaseURL            string `json:"base_url"`
	APIKey             string `json:"api_key"`
	Model              string `json:"model"`
	API                string `json:"api"`
	MaxTokens          int    `json:"max_tokens"`
	Stream             *bool  `json:"stream"`
	Spinner            *bool  `json:"spinner"`
	Tools              *bool  `json:"tools"`
	ToolRoot           string `json:"tool_root"`
	ToolTurns          int    `json:"tool_turns"`
	MarkdownTranslator string `json:"markdown_translator"`
	GlamourStyle       string `json:"glamour_style"`
	RichCodeTheme      string `json:"rich_code_theme"`
}

func applyAskConfig(cmd *cobra.Command, opts *options) error {
	opts.ConfigPath = strings.TrimSpace(opts.ConfigPath)
	if opts.ConfigPath == "" {
		opts.ConfigPath = defaultAskConfigPath()
	}
	required := flagChanged(cmd, "config") || envValueSet("PANGAEA_ASK_CONFIG")
	cfg, loaded, err := loadAskConfig(opts.ConfigPath, required)
	if err != nil {
		return err
	}
	if !loaded {
		applyAskEnvSecrets(cmd, opts)
		return nil
	}
	applyAskConfigValues(opts, cfg, func(name string) bool {
		return flagChanged(cmd, name)
	}, envValueSet)
	applyAskEnvSecrets(cmd, opts)
	return nil
}

func applyAskEnvSecrets(cmd *cobra.Command, opts *options) {
	if flagChanged(cmd, "api-key") {
		return
	}
	if key := envString("PANGAEA_ASK_API_KEY", envString("OPENAI_API_KEY", "")); key != "" {
		opts.APIKey = key
	}
}

func applyAskConfigValues(opts *options, cfg askConfig, changed func(string) bool, envSet func(...string) bool) {
	if cfg.BaseURL != "" && !changed("base-url") && !envSet("PANGAEA_ASK_BASE_URL") {
		opts.BaseURL = cfg.BaseURL
	}
	if cfg.APIKey != "" && !changed("api-key") && !envSet("PANGAEA_ASK_API_KEY", "OPENAI_API_KEY") {
		opts.APIKey = cfg.APIKey
	}
	if cfg.Model != "" && !changed("model") && !envSet("PANGAEA_ASK_MODEL") {
		opts.Model = cfg.Model
	}
	if cfg.API != "" && !changed("api") && !envSet("PANGAEA_ASK_API") {
		opts.API = cfg.API
	}
	if cfg.MaxTokens > 0 && !changed("max-tokens") && !envSet("PANGAEA_ASK_MAX_TOKENS") {
		opts.MaxTokens = cfg.MaxTokens
	}
	if cfg.Stream != nil && !changed("stream") && !envSet("PANGAEA_ASK_STREAM") {
		opts.Stream = *cfg.Stream
	}
	if cfg.Spinner != nil && !changed("spinner") && !envSet("PANGAEA_ASK_SPINNER") {
		opts.Spinner = *cfg.Spinner
	}
	if cfg.Tools != nil && !changed("tools") && !envSet("PANGAEA_ASK_TOOLS") {
		opts.Tools = *cfg.Tools
	}
	if cfg.ToolRoot != "" && !changed("tool-root") && !envSet("PANGAEA_ASK_TOOL_ROOT") {
		opts.ToolRoot = cfg.ToolRoot
	}
	if cfg.ToolTurns > 0 && !changed("tool-turns") && !envSet("PANGAEA_ASK_TOOL_TURNS") {
		opts.ToolTurns = cfg.ToolTurns
	}
	if cfg.MarkdownTranslator != "" && !changed("markdown-translator") && !envSet("PANGAEA_ASK_MARKDOWN_TRANSLATOR") {
		opts.MarkdownTranslator = cfg.MarkdownTranslator
	}
	if cfg.GlamourStyle != "" && !changed("glamour-style") && !envSet("PANGAEA_ASK_GLAMOUR_STYLE") {
		opts.GlamourStyle = cfg.GlamourStyle
	}
	if cfg.RichCodeTheme != "" && !changed("rich-code-theme") && !envSet("PANGAEA_ASK_RICH_CODE_THEME") {
		opts.RichCodeTheme = cfg.RichCodeTheme
	}
}

func loadAskConfig(path string, required bool) (askConfig, bool, error) {
	var cfg askConfig
	expanded, err := expandPath(path)
	if err != nil {
		return cfg, false, err
	}
	data, err := os.ReadFile(expanded)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) && !required {
			return cfg, false, nil
		}
		return cfg, false, fmt.Errorf("read ask config %s: %w", expanded, err)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&cfg); err != nil {
		return cfg, false, fmt.Errorf("parse ask config %s: %w", expanded, err)
	}
	return cfg, true, nil
}

func defaultAskConfigPath() string {
	if dir, err := os.UserConfigDir(); err == nil && strings.TrimSpace(dir) != "" {
		return filepath.Join(dir, "pangaea", defaultConfigName)
	}
	return filepath.Join("~", ".config", "pangaea", defaultConfigName)
}

func expandPath(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", fmt.Errorf("config path is empty")
	}
	if path == "~" || strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve home directory: %w", err)
		}
		if path == "~" {
			return home, nil
		}
		return filepath.Join(home, strings.TrimPrefix(path, "~/")), nil
	}
	return path, nil
}

func flagChanged(cmd *cobra.Command, name string) bool {
	flag := cmd.Flags().Lookup(name)
	return flag != nil && flag.Changed
}

func envValueSet(names ...string) bool {
	for _, name := range names {
		if value, ok := os.LookupEnv(name); ok && strings.TrimSpace(value) != "" {
			return true
		}
	}
	return false
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
		"If you need to run commands, use exec_command with a short intent. If you need to edit existing files, prefer apply_patch when possible.\n" +
		"Do not repeat identical read/list/search tool calls unless a previous tool changed the file tree. Use read_file instead of exec_command for commands like cat <file>.\n" +
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
		{
			"type": "function",
			"function": map[string]any{
				"name":        "search_files",
				"description": "Search files under the configured tool root using ripgrep.",
				"parameters": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"pattern":    map[string]any{"type": "string", "description": "Regex or literal pattern to search for."},
						"path":       map[string]any{"type": "string", "description": "Relative directory or file path. Defaults to ."},
						"glob":       map[string]any{"type": "string", "description": "Optional ripgrep glob, for example *.go."},
						"limit":      map[string]any{"type": "integer", "description": "Maximum number of result lines. Defaults to 200."},
						"timeout_ms": map[string]any{"type": "integer", "description": "Timeout in milliseconds. Defaults to 30000."},
						"intent":     intent,
					},
					"required":             []string{"pattern"},
					"additionalProperties": false,
				},
			},
		},
		{
			"type": "function",
			"function": map[string]any{
				"name":        "exec_command",
				"description": "Run a shell command under the configured tool root and return stdout/stderr.",
				"parameters": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"cmd":        map[string]any{"type": "string", "description": "Command to run with /bin/sh -lc."},
						"workdir":    map[string]any{"type": "string", "description": "Relative working directory under the tool root. Defaults to ."},
						"timeout_ms": map[string]any{"type": "integer", "description": "Timeout in milliseconds. Defaults to 120000."},
						"intent":     intent,
					},
					"required":             []string{"cmd"},
					"additionalProperties": false,
				},
			},
		},
		{
			"type": "function",
			"function": map[string]any{
				"name":        "apply_patch",
				"description": "Apply a Codex apply_patch patch under the configured tool root.",
				"parameters": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"patch":      map[string]any{"type": "string", "description": "Patch text beginning with *** Begin Patch."},
						"timeout_ms": map[string]any{"type": "integer", "description": "Timeout in milliseconds. Defaults to 120000."},
						"intent":     intent,
					},
					"required":             []string{"patch"},
					"additionalProperties": false,
				},
			},
		},
	}
}
