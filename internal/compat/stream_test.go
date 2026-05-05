package compat

import "testing"

func TestEventsFromResponseRoundTrip(t *testing.T) {
	response := Response{
		ID:      "resp_1",
		Dialect: APIDialectOpenAI,
		Model:   "gpt-test",
		Message: Message{
			Role:    MessageRoleAssistant,
			Content: []ContentPart{{Type: ContentPartText, Text: "hello"}, {Type: ContentPartText, Text: " world"}},
		},
		StopReason: "stop",
		Usage:      Usage{InputTokens: 3, OutputTokens: 4, TotalTokens: 7},
	}
	events, err := EventsFromResponse(response)
	if err != nil {
		t.Fatalf("events from response: %v", err)
	}
	if len(events) != 5 {
		t.Fatalf("expected five events, got %#v", events)
	}
	roundTrip, err := ResponseFromEvents(response.Dialect, response.Model, response.ID, events)
	if err != nil {
		t.Fatalf("response from events: %v", err)
	}
	if roundTrip.Message.Content[0].Text != "hello world" || roundTrip.Usage.TotalTokens != 7 || roundTrip.StopReason != "stop" {
		t.Fatalf("unexpected round trip response: %#v", roundTrip)
	}
}

func TestApplyEventToResponseRejectsInvalidEvent(t *testing.T) {
	response := Response{Dialect: APIDialectOpenAI, Model: "gpt-test", Message: Message{Role: MessageRoleAssistant}}
	err := ApplyEventToResponse(&response, Event{Type: EventContentDelta})
	if err == nil {
		t.Fatalf("expected invalid event error")
	}
}
