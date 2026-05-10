package geminidirect

import (
	"context"
	"fmt"
	"strings"

	"github.com/0xc0de1ab/pangaea/internal/compat"
)

const defaultMaxToolRounds = 4

type ToolDispatcher interface {
	DispatchTool(context.Context, compat.ToolCall) (compat.Message, error)
}

type ToolCatalog interface {
	ToolDefinitions(context.Context) ([]compat.ToolDefinition, error)
}

type closeableToolDispatcher interface {
	Close() error
}

func maxToolRounds(value int) int {
	if value > 0 {
		return value
	}
	return defaultMaxToolRounds
}

func (p *Provider) toolDefinitions(ctx context.Context, requested []compat.ToolDefinition) ([]compat.ToolDefinition, error) {
	out := cloneToolDefinitions(requested)
	if len(out) == 0 {
		return out, nil
	}
	catalog, ok := p.toolDispatcher.(ToolCatalog)
	if !ok || catalog == nil {
		return out, nil
	}
	extra, err := catalog.ToolDefinitions(ctx)
	if err != nil {
		return nil, err
	}
	return mergeToolDefinitions(out, extra), nil
}

func mergeToolDefinitions(base []compat.ToolDefinition, extra []compat.ToolDefinition) []compat.ToolDefinition {
	out := cloneToolDefinitions(base)
	seen := make(map[string]int, len(out))
	for i, tool := range out {
		seen[strings.TrimSpace(tool.Name)] = i
	}
	for _, tool := range extra {
		name := strings.TrimSpace(tool.Name)
		if name == "" {
			continue
		}
		if idx, exists := seen[name]; exists {
			out[idx] = tool
			continue
		}
		seen[name] = len(out)
		out = append(out, tool)
	}
	return out
}

func cloneToolDefinitions(in []compat.ToolDefinition) []compat.ToolDefinition {
	if len(in) == 0 {
		return nil
	}
	out := make([]compat.ToolDefinition, 0, len(in))
	for _, tool := range in {
		copied := tool
		copied.Parameters = cloneMap(tool.Parameters)
		out = append(out, copied)
	}
	return out
}

func cloneMap(in map[string]any) map[string]any {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]any, len(in))
	for key, value := range in {
		switch typed := value.(type) {
		case map[string]any:
			out[key] = cloneMap(typed)
		case []any:
			out[key] = cloneSlice(typed)
		default:
			out[key] = value
		}
	}
	return out
}

func cloneSlice(in []any) []any {
	if len(in) == 0 {
		return nil
	}
	out := make([]any, 0, len(in))
	for _, value := range in {
		switch typed := value.(type) {
		case map[string]any:
			out = append(out, cloneMap(typed))
		case []any:
			out = append(out, cloneSlice(typed))
		default:
			out = append(out, value)
		}
	}
	return out
}

func (p *Provider) dispatchToolCalls(ctx context.Context, calls []compat.ToolCall) ([]compat.Message, error) {
	if p.toolDispatcher == nil || len(calls) == 0 {
		return nil, nil
	}
	results := make([]compat.Message, 0, len(calls))
	for _, call := range calls {
		result, err := p.toolDispatcher.DispatchTool(ctx, call)
		if err != nil {
			return nil, err
		}
		if result.Role == "" {
			result.Role = compat.MessageRoleTool
		}
		if result.Role != compat.MessageRoleTool {
			return nil, fmt.Errorf("%w: tool dispatcher returned role %q", ErrConfig, result.Role)
		}
		if strings.TrimSpace(result.ToolCallID) == "" {
			result.ToolCallID = call.ID
		}
		if strings.TrimSpace(result.Name) == "" {
			result.Name = call.Name
		}
		if err := result.Validate(); err != nil {
			return nil, err
		}
		results = append(results, result)
	}
	return results, nil
}

func appendToolContinuation(request compat.Request, assistant compat.Message, toolResults []compat.Message) compat.Request {
	request.Messages = append(request.Messages, assistant)
	request.Messages = append(request.Messages, toolResults...)
	return request
}

func addUsage(base compat.Usage, delta compat.Usage) compat.Usage {
	total := delta.TotalTokens
	if total == 0 {
		total = delta.InputTokens + delta.OutputTokens
	}
	base.InputTokens += delta.InputTokens
	base.OutputTokens += delta.OutputTokens
	base.TotalTokens += total
	return base
}

type toolStreamRoundEmitter struct {
	emit              func(compat.Event) error
	buffered          []compat.Event
	streamingToClient bool
	internalToolRound bool
}

func (e *toolStreamRoundEmitter) Emit(event compat.Event) error {
	if e == nil || e.emit == nil {
		return nil
	}
	if e.internalToolRound {
		e.buffered = append(e.buffered, event)
		return nil
	}
	switch event.Type {
	case compat.EventMessageStart:
		if e.streamingToClient {
			return e.emit(event)
		}
		e.buffered = append(e.buffered, event)
		return nil
	case compat.EventToolCallDelta:
		if e.streamingToClient {
			return e.emit(event)
		}
		e.internalToolRound = true
		e.buffered = append(e.buffered, event)
		return nil
	default:
		if !e.streamingToClient {
			if err := e.flushBuffered(); err != nil {
				return err
			}
			e.streamingToClient = true
		}
		return e.emit(event)
	}
}

func (e *toolStreamRoundEmitter) flushBuffered() error {
	for _, event := range e.buffered {
		if err := e.emit(event); err != nil {
			return err
		}
	}
	e.buffered = nil
	return nil
}
