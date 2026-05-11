package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/briandowns/spinner"
	"github.com/fatih/color"
	"github.com/google/antigravity-compat-proxy/internal/models"
)

type AllowMode string

const (
	AllowAsk       AllowMode = "ask"
	AllowDeny      AllowMode = "deny"
	AllowYolo      AllowMode = "yolo"
	AllowDenyWrite AllowMode = "deny-write"
)

type ToolType int

const (
	ToolReadOnly ToolType = iota
	ToolWrite
)

type ToolDefinition struct {
	Name        string
	Type        ToolType
	Description string
	Execute     func(args map[string]interface{}) (string, int)
}

type ToolManager struct {
	tools     map[string]ToolDefinition
	allowMode AllowMode
	mu        sync.Mutex
}

func NewToolManager(mode AllowMode) *ToolManager {
	tm := &ToolManager{
		tools:     make(map[string]ToolDefinition),
		allowMode: mode,
	}
	tm.registerBuiltinTools()
	return tm
}

func (tm *ToolManager) registerBuiltinTools() {
	tm.RegisterTool(ToolDefinition{
		Name: "ls",
		Type: ToolReadOnly,
		Execute: func(args map[string]interface{}) (string, int) {
			path, _ := args["path"].(string)
			if path == "" {
				path = "."
			}
			files, err := os.ReadDir(path)
			if err != nil {
				return "Error: " + err.Error(), 1
			}
			var res []string
			for _, f := range files {
				mode := "f"
				if f.IsDir() {
					mode = "d"
				}
				res = append(res, fmt.Sprintf("[%s] %s", mode, f.Name()))
			}
			return strings.Join(res, "\n"), 0
		},
	})

	tm.RegisterTool(ToolDefinition{
		Name: "read_file",
		Type: ToolReadOnly,
		Execute: func(args map[string]interface{}) (string, int) {
			path, _ := args["path"].(string)
			data, err := os.ReadFile(path)
			if err != nil {
				return "Error: " + err.Error(), 1
			}
			return string(data), 0
		},
	})

	tm.RegisterTool(ToolDefinition{
		Name: "grep_search",
		Type: ToolReadOnly,
		Execute: func(args map[string]interface{}) (string, int) {
			pattern, _ := args["pattern"].(string)
			path, _ := args["path"].(string)
			cmd := exec.Command("grep", "-rnE", pattern, path)
			out, _ := cmd.CombinedOutput()
			return string(out), 0
		},
	})

	tm.RegisterTool(ToolDefinition{
		Name: "write_file",
		Type: ToolWrite,
		Execute: func(args map[string]interface{}) (string, int) {
			path, _ := args["path"].(string)
			content, _ := args["content"].(string)
			err := os.MkdirAll(filepath.Dir(path), 0755)
			if err != nil {
				return "Error: " + err.Error(), 1
			}
			err = os.WriteFile(path, []byte(content), 0644)
			if err != nil {
				return "Error: " + err.Error(), 1
			}
			return "Successfully wrote to " + path, 0
		},
	})

	tm.RegisterTool(ToolDefinition{
		Name: "replace_text",
		Type: ToolWrite,
		Execute: func(args map[string]interface{}) (string, int) {
			path, _ := args["path"].(string)
			oldTxt, _ := args["old"].(string)
			newTxt, _ := args["new"].(string)
			data, err := os.ReadFile(path)
			if err != nil {
				return "Error: " + err.Error(), 1
			}
			content := string(data)
			if !strings.Contains(content, oldTxt) {
				return "Error: 'old' text not found", 1
			}
			newContent := strings.Replace(content, oldTxt, newTxt, 1)
			os.WriteFile(path, []byte(newContent), 0644)
			return "Successfully replaced text in " + path, 0
		},
	})

	tm.RegisterTool(ToolDefinition{
		Name: "shell_execute",
		Type: ToolWrite,
		Execute: func(args map[string]interface{}) (string, int) {
			command, _ := args["command"].(string)
			cmd := exec.Command("bash", "-c", command)
			out, err := cmd.CombinedOutput()
			code := 0
			if err != nil {
				code = 1
				if exitError, ok := err.(*exec.ExitError); ok {
					code = exitError.ExitCode()
				}
			}
			return string(out), code
		},
	})
}

func (tm *ToolManager) RegisterTool(def ToolDefinition) {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	tm.tools[def.Name] = def
}

func (tm *ToolManager) ExecuteCalls(ctx context.Context, calls []models.ToolCall) []models.ChatMessage {
	var results []models.ChatMessage
	var readOnlyCalls []models.ToolCall
	var writeCalls []models.ToolCall

	for _, call := range calls {
		def, ok := tm.tools[call.Function.Name]
		if !ok || def.Type == ToolWrite {
			writeCalls = append(writeCalls, call)
		} else {
			readOnlyCalls = append(readOnlyCalls, call)
		}
	}

	// 1. Parallel execution for Read-Only (Speed)
	if len(readOnlyCalls) > 0 {
		fmt.Printf("⚡ \033[33mParallel Execution: %d tools\033[0m\n", len(readOnlyCalls))
		var wg sync.WaitGroup
		resChan := make(chan models.ChatMessage, len(readOnlyCalls))
		for _, call := range readOnlyCalls {
			wg.Add(1)
			go func(c models.ToolCall) {
				defer wg.Done()
				resChan <- tm.executeWithFeedback(c)
			}(call)
		}
		wg.Wait()
		close(resChan)
		for res := range resChan {
			results = append(results, res)
		}
	}

	// 2. Sequential execution for Write (Safety)
	if len(writeCalls) > 0 {
		fmt.Printf("🔒 \033[33mSequential Execution: %d tools\033[0m\n", len(writeCalls))
		for _, call := range writeCalls {
			results = append(results, tm.executeWithFeedback(call))
		}
	}

	return results
}

func (tm *ToolManager) executeWithFeedback(call models.ToolCall) models.ChatMessage {
	def, ok := tm.tools[call.Function.Name]

	// Permission Check
	if !tm.isAllowed(call.Function.Name, def.Type) {
		color.Yellow("⚠️  Tool call denied by policy: %s", call.Function.Name)
		return models.ChatMessage{
			Role:       "tool",
			Name:       call.Function.Name,
			Content:    "Execution denied by user policy.",
			ToolCallID: call.ID,
		}
	}

	if tm.allowMode == AllowAsk {
		fmt.Printf("\n❓ \033[1;33mAllow %s(%s)? [y/n/yolo]: \033[0m", call.Function.Name, call.Function.Arguments)
		reader := bufio.NewReader(os.Stdin)
		input, _ := reader.ReadString('\n')
		input = strings.TrimSpace(strings.ToLower(input))
		if input == "yolo" {
			tm.allowMode = AllowYolo
		} else if input != "y" {
			return models.ChatMessage{
				Role:       "tool",
				Name:       call.Function.Name,
				Content:    "Execution denied by user.",
				ToolCallID: call.ID,
			}
		}
	}

	s := spinner.New(spinner.CharSets[11], 100*time.Millisecond)
	s.Suffix = fmt.Sprintf(" Executing %s...", color.CyanString(call.Function.Name))
	s.Start()

	var args map[string]interface{}
	json.Unmarshal([]byte(call.Function.Arguments), &args)

	output, code := "Unknown tool", 1
	if ok {
		output, code = def.Execute(args)
	}
	s.Stop()

	// Visual status
	bullet := color.New(color.FgGreen).Sprint("●")
	if code == 1 {
		bullet = color.New(color.FgRed).Sprint("●")
	} else if code > 1 {
		bullet = color.New(color.FgYellow).Sprint("●")
	}

	summary := strings.Split(output, "\n")[0]
	if len(summary) > 100 {
		summary = summary[:97] + "..."
	}

	fmt.Printf("%s %s -> %s\n", bullet, color.CyanString(call.Function.Name), color.WhiteString(summary))

	return models.ChatMessage{
		Role:       "tool",
		Name:       call.Function.Name,
		Content:    output,
		ToolCallID: call.ID,
	}
}

func (tm *ToolManager) isAllowed(name string, t ToolType) bool {
	switch tm.allowMode {
	case AllowDeny:
		return false
	case AllowYolo:
		return true
	case AllowDenyWrite:
		return t == ToolReadOnly
	case AllowAsk:
		return true // Handled in executeWithFeedback
	default:
		return false
	}
}
