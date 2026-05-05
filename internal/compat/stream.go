package compat

// EventsFromResponse converts a completed canonical response into the minimal
// canonical event sequence used by providers that do not expose native streaming.
func EventsFromResponse(response Response) ([]Event, error) {
	if err := response.Validate(); err != nil {
		return nil, err
	}
	events := []Event{{
		ResponseID: response.ID,
		Dialect:    response.Dialect,
		Model:      response.Model,
		Type:       EventMessageStart,
		Message: &Message{
			Role: MessageRoleAssistant,
		},
	}}
	for _, part := range response.Message.Content {
		events = append(events, Event{ResponseID: response.ID, Dialect: response.Dialect, Model: response.Model, Type: EventContentDelta, ContentDelta: &ContentPart{
			Type: part.Type,
			Text: part.Text,
		}})
	}
	for _, call := range response.Message.ToolCalls {
		copied := call
		events = append(events, Event{ResponseID: response.ID, Dialect: response.Dialect, Model: response.Model, Type: EventToolCallDelta, ToolCallDelta: &copied})
	}
	if response.Usage != (Usage{}) {
		usage := response.Usage
		events = append(events, Event{ResponseID: response.ID, Dialect: response.Dialect, Model: response.Model, Type: EventUsageDelta, UsageDelta: &usage})
	}
	events = append(events, Event{ResponseID: response.ID, Dialect: response.Dialect, Model: response.Model, Type: EventDone, DoneReason: response.StopReason})
	return events, nil
}

func ResponseFromEvents(dialect APIDialect, model string, id string, events []Event) (Response, error) {
	response := Response{
		ID:      id,
		Dialect: dialect,
		Model:   model,
		Message: Message{Role: MessageRoleAssistant},
	}
	for _, event := range events {
		if err := ApplyEventToResponse(&response, event); err != nil {
			return Response{}, err
		}
	}
	if err := response.Validate(); err != nil {
		return Response{}, err
	}
	return response, nil
}

func ApplyEventToResponse(response *Response, event Event) error {
	if response == nil {
		return ErrInvalidResponse
	}
	if err := event.Validate(); err != nil {
		return err
	}
	if response.Message.Role == "" {
		response.Message.Role = MessageRoleAssistant
	}
	if event.ResponseID != "" {
		response.ID = event.ResponseID
	}
	if event.Dialect != "" {
		response.Dialect = event.Dialect
	}
	if event.Model != "" {
		response.Model = event.Model
	}
	switch event.Type {
	case EventMessageStart:
		if event.Message == nil {
			return ErrInvalidEvent
		}
		response.Message.Role = MessageRoleAssistant
		for _, part := range event.Message.Content {
			appendContentDelta(response, part)
		}
		response.Message.ToolCalls = append(response.Message.ToolCalls, event.Message.ToolCalls...)
	case EventContentDelta:
		appendContentDelta(response, *event.ContentDelta)
	case EventToolCallDelta:
		response.Message.ToolCalls = append(response.Message.ToolCalls, *event.ToolCallDelta)
	case EventUsageDelta:
		response.Usage.InputTokens += event.UsageDelta.InputTokens
		response.Usage.OutputTokens += event.UsageDelta.OutputTokens
		response.Usage.TotalTokens += event.UsageDelta.TotalTokens
	case EventDone:
		response.StopReason = event.DoneReason
	}
	return nil
}

func appendContentDelta(response *Response, part ContentPart) {
	if part.Type == ContentPartText && len(response.Message.Content) > 0 {
		last := &response.Message.Content[len(response.Message.Content)-1]
		if last.Type == ContentPartText {
			last.Text += part.Text
			return
		}
	}
	response.Message.Content = append(response.Message.Content, part)
}
