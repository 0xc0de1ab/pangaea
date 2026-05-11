package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/charmbracelet/glamour"
	"github.com/dh-kam/refutils/flagsbinder"
	"github.com/google/antigravity-compat-proxy/internal/agent"
	"github.com/google/antigravity-compat-proxy/internal/models"
	"github.com/spf13/cobra"
)

type chatOptions struct {
	Addr         string   `flag:"addr" env:"PROXY_ADDR" usage:"Address of the proxy server"`
	APIKey       string   `flag:"api-key" env:"OPENAI_API_KEY" usage:"API key for authentication"`
	Model        string   `flag:"model" usage:"Model to use for chat"`
	ResponseMode string   `flag:"response-mode" usage:"Response mode (stream or buffer)"`
	Translator   string   `flag:"markdown-translator" usage:"Markdown translator (glamour or none)"`
	File         string   `flag:"file" usage:"Path to a file to attach (image or text)"`
	AllowMode    string   `flag:"allow-mode" usage:"Tool execution policy (ask, deny, yolo, deny-write)"`
	MCPServers   []string `flag:"mcp-server" usage:"Paths to MCP server executables"`
}

func newChatCommand() *cobra.Command {
	opts := &chatOptions{}
	binder := flagsbinder.NewViperCobraFlagsBinder().
		String("addr", "http://localhost:8080", "Address of the proxy server").
		String("api-key", "sk-c0de1ab-test", "API key for authentication").
		String("model", "gemini-3-flash", "Model to use for chat").
		String("response-mode", "stream", "Response mode (stream or buffer)").
		String("markdown-translator", "glamour", "Markdown translator (glamour or none)").
		String("file", "", "Path to a file to attach (image or text)").
		String("allow-mode", "ask", "Tool execution policy (ask, deny, yolo, deny-write)").
		StringSlice("mcp-server", []string{}, "Paths to MCP server executables")

	cmd := &cobra.Command{
		Use:   "chat [prompt]",
		Short: "Agentic chat with full tool support (ReAct loop)",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := binder.BindCommand(cmd, opts, args...); err != nil {
				return usageError(cmd, err)
			}
			return runChat(cmd.Context(), opts, strings.Join(args, " "))
		},
	}
	binder.SetTo(cmd.Flags())
	return cmd
}

const reactSystemPrompt = `You are an autonomous senior software engineer agent.
You operate using a Reason-Action-Observation (ReAct) loop to solve complex tasks.

For every step:
1. **Thought**: Explain your plan and reasoning.
2. **Action**: Use a tool if you need to interact with the system.
3. **Observation**: Review the tool output.
4. Repeat until the task is complete.

Output your actions in this XML format:
<tool_call>
{"name": "tool_name", "arguments": {"arg1": "value1"}}
</tool_call>

Available Standard Tools:
- ls(path: string): Lists files and directories.
- read_file(path: string): Reads file content.
- write_file(path: string, content: string): Creates or overwrites a file.
- replace_text(path: string, old: string, new: string): Surgical search and replace in a file.
- grep_search(pattern: string, path: string): Search for a regex pattern in files.
- shell_execute(command: string): Executes a bash command and returns output.
`

func runChat(ctx context.Context, opts *chatOptions, prompt string) error {
	client := &http.Client{}

	if !strings.HasPrefix(opts.Addr, "http") {
		opts.Addr = "http://" + opts.Addr
	}

	tm := agent.NewToolManager(agent.AllowMode(opts.AllowMode))

	messages := []models.ChatMessage{
		{Role: "system", Content: reactSystemPrompt},
	}

	if opts.File != "" {
		content, err := os.ReadFile(opts.File)
		if err != nil {
			return fmt.Errorf("failed to read file %s: %w", opts.File, err)
		}
		ext := strings.ToLower(filepath.Ext(opts.File))
		if ext == ".jpg" || ext == ".jpeg" || ext == ".png" {
			b64 := base64.StdEncoding.EncodeToString(content)
			mime := "image/jpeg"
			if ext == ".png" {
				mime = "image/png"
			}
			messages = append(messages, models.ChatMessage{
				Role: "user",
				Content: []interface{}{
					map[string]interface{}{"type": "text", "text": "Context file: " + opts.File},
					map[string]interface{}{
						"type":      "image_url",
						"image_url": map[string]string{"url": fmt.Sprintf("data:%s;base64,%s", mime, b64)},
					},
				},
			})
		} else {
			messages = append(messages, models.ChatMessage{
				Role:    "user",
				Content: fmt.Sprintf("Context file (%s) content:\n\n%s", opts.File, string(content)),
			})
		}
	}

	messages = append(messages, models.ChatMessage{Role: "user", Content: prompt})

	harnessTools := []models.Tool{
		{Type: "function", Function: models.FunctionDefinition{Name: "ls", Description: "List directory contents", Parameters: map[string]interface{}{"type": "object", "properties": map[string]interface{}{"path": map[string]string{"type": "string"}}, "required": []string{"path"}}}},
		{Type: "function", Function: models.FunctionDefinition{Name: "read_file", Description: "Read file content", Parameters: map[string]interface{}{"type": "object", "properties": map[string]interface{}{"path": map[string]string{"type": "string"}}, "required": []string{"path"}}}},
		{Type: "function", Function: models.FunctionDefinition{Name: "write_file", Description: "Write content to file", Parameters: map[string]interface{}{"type": "object", "properties": map[string]interface{}{"path": map[string]string{"type": "string"}, "content": map[string]string{"type": "string"}}, "required": []string{"path", "content"}}}},
		{Type: "function", Function: models.FunctionDefinition{Name: "replace_text", Description: "Surgical search and replace", Parameters: map[string]interface{}{"type": "object", "properties": map[string]interface{}{"path": map[string]string{"type": "string"}, "old": map[string]string{"type": "string"}, "new": map[string]string{"type": "string"}}, "required": []string{"path", "old", "new"}}}},
		{Type: "function", Function: models.FunctionDefinition{Name: "grep_search", Description: "Search for pattern", Parameters: map[string]interface{}{"type": "object", "properties": map[string]interface{}{"pattern": map[string]string{"type": "string"}, "path": map[string]string{"type": "string"}}, "required": []string{"pattern", "path"}}}},
		{Type: "function", Function: models.FunctionDefinition{Name: "shell_execute", Description: "Run bash command", Parameters: map[string]interface{}{"type": "object", "properties": map[string]interface{}{"command": map[string]string{"type": "string"}}, "required": []string{"command"}}}},
	}

	for turnNum := 0; turnNum < 10; turnNum++ {
		fmt.Printf("\n\033[1;35m--- Turn %d ---\033[0m\n", turnNum+1)

		var fullContent string
		var toolCalls []models.ToolCall
		var err error

		if opts.ResponseMode == "stream" {
			fullContent, toolCalls, err = handleStreamingRequest(ctx, client, opts, messages, harnessTools)
		} else {
			fullContent, toolCalls, err = handleBufferedRequest(ctx, client, opts, messages, harnessTools)
		}
		if err != nil {
			return err
		}

		if len(toolCalls) == 0 {
			if opts.Translator == "glamour" {
				renderMarkdown(fullContent)
			} else {
				fmt.Println(fullContent)
			}
			break
		}

		messages = append(messages, models.ChatMessage{Role: "assistant", Content: fullContent})

		// Use ToolManager for parallel/sequential execution and visual feedback
		toolResults := tm.ExecuteCalls(ctx, toolCalls)
		messages = append(messages, toolResults...)
	}
	return nil
}

func handleStreamingRequest(ctx context.Context, client *http.Client, opts *chatOptions, messages []models.ChatMessage, tools []models.Tool) (string, []models.ToolCall, error) {
	payload := models.ChatCompletionRequest{
		Model:    opts.Model,
		Messages: messages,
		Tools:    tools,
		Stream:   true,
	}
	body, _ := json.Marshal(payload)
	req, _ := http.NewRequestWithContext(ctx, "POST", opts.Addr+"/v1/chat/completions", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+opts.APIKey)
	resp, err := client.Do(req)
	if err != nil {
		return "", nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		rb, _ := io.ReadAll(resp.Body)
		return "", nil, fmt.Errorf("proxy error %d: %s", resp.StatusCode, string(rb))
	}
	scanner := bufio.NewScanner(resp.Body)
	var fullText strings.Builder
	fmt.Printf("\033[36mThinking...\033[0m\n")
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")
		if data == "[DONE]" {
			break
		}
		var chunk models.ChatCompletionStreamResponse
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			continue
		}
		if len(chunk.Choices) > 0 {
			content := chunk.Choices[0].Delta.Content
			if s, ok := content.(string); ok {
				fullText.WriteString(s)
				fmt.Print(s)
			}
		}
	}
	fmt.Println()
	text := fullText.String()
	return text, parseXMLToolCalls(text), nil
}

func handleBufferedRequest(ctx context.Context, client *http.Client, opts *chatOptions, messages []models.ChatMessage, tools []models.Tool) (string, []models.ToolCall, error) {
	payload := models.ChatCompletionRequest{
		Model:    opts.Model,
		Messages: messages,
		Tools:    tools,
		Stream:   false,
	}
	body, _ := json.Marshal(payload)
	req, _ := http.NewRequestWithContext(ctx, "POST", opts.Addr+"/v1/chat/completions", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+opts.APIKey)
	resp, err := client.Do(req)
	if err != nil {
		return "", nil, err
	}
	defer resp.Body.Close()
	var oaResp models.ChatCompletionResponse
	if err := json.NewDecoder(resp.Body).Decode(&oaResp); err != nil {
		return "", nil, err
	}
	if len(oaResp.Choices) == 0 {
		return "", nil, fmt.Errorf("no choices")
	}
	msg := oaResp.Choices[0].Message
	content := ""
	if s, ok := msg.Content.(string); ok {
		content = s
	}
	return content, msg.ToolCalls, nil
}

func renderMarkdown(text string) {
	r, _ := glamour.NewTermRenderer(glamour.WithAutoStyle(), glamour.WithWordWrap(80))
	out, err := r.Render(text)
	if err != nil {
		fmt.Println(text)
		return
	}
	fmt.Print(out)
}

var toolCallRegex = regexp.MustCompile(`(?i)<tool_call>\s*(\{[\s\S]*?\})\s*</tool_call>`)

func parseXMLToolCalls(text string) []models.ToolCall {
	matches := toolCallRegex.FindAllStringSubmatch(text, -1)
	var tcs []models.ToolCall
	for i, m := range matches {
		if len(m) > 1 {
			var raw map[string]interface{}
			if err := json.Unmarshal([]byte(m[1]), &raw); err == nil {
				name := raw["name"]
				if name == nil {
					name = raw["tool_name"]
				}
				args := raw["arguments"]
				if args == nil {
					args = raw["parameters"]
				}
				argsStr := ""
				switch v := args.(type) {
				case string:
					argsStr = v
				default:
					b, _ := json.Marshal(v)
					argsStr = string(b)
				}
				tcs = append(tcs, models.ToolCall{
					ID:   fmt.Sprintf("cli_%d", time.Now().UnixNano()+int64(i)),
					Type: "function",
					Function: models.ToolFunction{
						Name:      fmt.Sprintf("%v", name),
						Arguments: argsStr,
					},
				})
			}
		}
	}
	return tcs
}

func truncate(s string, n int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
