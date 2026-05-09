package router

import "testing"

func TestEngineAuditEventsNewestFirstAndLimited(t *testing.T) {
	engine, _ := testEngine(t)

	first := engine.RecordAuditEvent(AuditEvent{
		Type:    AuditEventAPIKeyCreate,
		Target:  AuditTarget{APIKeyID: "key_1"},
		Outcome: AuditOutcomeSucceeded,
	})
	second := engine.RecordAuditEvent(AuditEvent{
		Type:    AuditEventProviderDrain,
		Target:  AuditTarget{ProviderInstanceID: "codex-primary-a1"},
		Outcome: AuditOutcomeFailed,
		Error:   "control session not found",
	})

	if first.ID == "" || second.ID == "" || first.ID == second.ID {
		t.Fatalf("expected generated unique audit ids, first=%#v second=%#v", first, second)
	}
	events := engine.AuditEvents(1)
	if len(events) != 1 || events[0].ID != second.ID {
		t.Fatalf("expected newest audit event first, got %#v", events)
	}
	events = engine.AuditEvents(10)
	if len(events) != 2 || events[0].ID != second.ID || events[1].ID != first.ID {
		t.Fatalf("unexpected audit event order: %#v", events)
	}
}

func TestEngineAuditEventsEvictsOldest(t *testing.T) {
	engine, _ := testEngine(t)

	for i := 0; i < defaultAuditEventLimit+3; i++ {
		engine.RecordAuditEvent(AuditEvent{
			Type:    AuditEventAPIKeyCreate,
			Target:  AuditTarget{APIKeyID: "key"},
			Outcome: AuditOutcomeSucceeded,
		})
	}
	events := engine.AuditEvents(0)
	if len(events) != defaultAuditEventLimit {
		t.Fatalf("expected capped audit events, got %d", len(events))
	}
}
