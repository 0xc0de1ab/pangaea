package compat

import (
	"errors"
	"strings"
)

var (
	ErrInvalidRequest  = errors.New("invalid compat request")
	ErrInvalidResponse = errors.New("invalid compat response")
	ErrInvalidEvent    = errors.New("invalid compat event")
)

func (r Request) Validate() error {
	if !r.Dialect.Valid() || blank(r.Model) || len(r.Messages) == 0 {
		return ErrInvalidRequest
	}
	if r.UnsupportedFeatures != "" && !r.UnsupportedFeatures.Valid() {
		return ErrInvalidRequest
	}
	if r.Temperature != nil && *r.Temperature < 0 {
		return ErrInvalidRequest
	}
	if r.MaxOutputTokens < 0 {
		return ErrInvalidRequest
	}
	for _, message := range r.Messages {
		if err := validateMessage(message, false); err != nil {
			return ErrInvalidRequest
		}
	}
	return nil
}

func (r Response) Validate() error {
	if !r.Dialect.Valid() || blank(r.Model) {
		return ErrInvalidResponse
	}
	if r.Message.Role != MessageRoleAssistant {
		return ErrInvalidResponse
	}
	if err := validateMessage(r.Message, false); err != nil {
		return ErrInvalidResponse
	}
	if err := r.Usage.Validate(); err != nil {
		return ErrInvalidResponse
	}
	return nil
}

func (e Event) Validate() error {
	if !e.Type.Valid() {
		return ErrInvalidEvent
	}
	if e.Dialect != "" && !e.Dialect.Valid() {
		return ErrInvalidEvent
	}
	switch e.Type {
	case EventMessageStart:
		if e.Message == nil || e.Message.Role != MessageRoleAssistant {
			return ErrInvalidEvent
		}
		if err := validateMessage(*e.Message, true); err != nil {
			return ErrInvalidEvent
		}
	case EventContentDelta:
		if e.ContentDelta == nil {
			return ErrInvalidEvent
		}
		if err := e.ContentDelta.Validate(); err != nil {
			return ErrInvalidEvent
		}
	case EventToolCallDelta:
		if e.ToolCallDelta == nil {
			return ErrInvalidEvent
		}
		if err := validateToolCallDelta(*e.ToolCallDelta); err != nil {
			return ErrInvalidEvent
		}
	case EventUsageDelta:
		if e.UsageDelta == nil {
			return ErrInvalidEvent
		}
		if err := e.UsageDelta.Validate(); err != nil {
			return ErrInvalidEvent
		}
	case EventError:
		if e.Error == nil || blank(e.Error.Message) {
			return ErrInvalidEvent
		}
	case EventDone:
		return nil
	}
	return nil
}

func (m Message) Validate() error {
	return validateMessage(m, false)
}

func (p ContentPart) Validate() error {
	if !p.Type.Valid() {
		return ErrInvalidRequest
	}
	if p.Type == ContentPartText && blank(p.Text) {
		return ErrInvalidRequest
	}
	return nil
}

func (t ToolCall) Validate() error {
	if !t.Type.Valid() || blank(t.Name) || t.Index < 0 {
		return ErrInvalidRequest
	}
	return nil
}

func (u Usage) Validate() error {
	if u.InputTokens < 0 || u.OutputTokens < 0 || u.TotalTokens < 0 {
		return ErrInvalidResponse
	}
	if u.TotalTokens > 0 && u.TotalTokens < u.InputTokens+u.OutputTokens {
		return ErrInvalidResponse
	}
	return nil
}

func (d APIDialect) Valid() bool {
	switch d {
	case APIDialectOpenAI, APIDialectAnthropic, APIDialectGemini:
		return true
	}
	return false
}

func (p UnsupportedFeaturePolicy) Valid() bool {
	switch p {
	case UnsupportedFeatureReject, UnsupportedFeatureDrop, UnsupportedFeatureApproximate:
		return true
	}
	return false
}

func (r MessageRole) Valid() bool {
	switch r {
	case MessageRoleSystem, MessageRoleDeveloper, MessageRoleUser, MessageRoleAssistant, MessageRoleTool:
		return true
	}
	return false
}

func (t ContentPartType) Valid() bool {
	switch t {
	case ContentPartText:
		return true
	}
	return false
}

func (t ToolCallType) Valid() bool {
	switch t {
	case ToolCallFunction:
		return true
	}
	return false
}

func (t EventType) Valid() bool {
	switch t {
	case EventMessageStart, EventContentDelta, EventToolCallDelta, EventUsageDelta, EventError, EventDone:
		return true
	}
	return false
}

func validateMessage(m Message, allowEmpty bool) error {
	if !m.Role.Valid() {
		return ErrInvalidRequest
	}
	if m.Role == MessageRoleTool && blank(m.ToolCallID) {
		return ErrInvalidRequest
	}
	if !allowEmpty && len(m.Content) == 0 && len(m.ToolCalls) == 0 {
		return ErrInvalidRequest
	}
	for _, part := range m.Content {
		if err := part.Validate(); err != nil {
			return err
		}
	}
	for _, toolCall := range m.ToolCalls {
		if err := toolCall.Validate(); err != nil {
			return err
		}
	}
	return nil
}

func validateToolCallDelta(t ToolCall) error {
	if t.Index < 0 {
		return ErrInvalidEvent
	}
	if t.Type != "" && !t.Type.Valid() {
		return ErrInvalidEvent
	}
	if blank(t.ID) && blank(t.Name) && blank(t.Arguments) {
		return ErrInvalidEvent
	}
	return nil
}

func blank(s string) bool {
	return strings.TrimSpace(s) == ""
}
