package quota

import (
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

func TestLedgerReserveCommitRelease(t *testing.T) {
	ledger := NewLedgerWithClock(func() time.Time { return time.Unix(100, 0) })
	scope := testScope()
	if err := ledger.SetLimit(scope, Limit{MaxTokens: 100, MaxRequests: 10}); err != nil {
		t.Fatalf("set limit: %v", err)
	}

	reservation, err := ledger.Reserve(ReservationRequest{
		RequestID: "req_1",
		Scope:     scope,
		Estimate:  Usage{Tokens: 20, Requests: 1},
	})
	if err != nil {
		t.Fatalf("reserve: %v", err)
	}
	if reservation.Status != ReservationReserved {
		t.Fatalf("expected reserved status, got %q", reservation.Status)
	}
	_, committed, reserved, err := ledger.Snapshot(scope)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if committed != (Usage{}) || reserved.Tokens != 20 {
		t.Fatalf("unexpected snapshot after reserve: committed=%#v reserved=%#v", committed, reserved)
	}

	committedReservation, err := ledger.Commit("req_1", Usage{Tokens: 18, Requests: 1})
	if err != nil {
		t.Fatalf("commit: %v", err)
	}
	if committedReservation.Status != ReservationCommitted {
		t.Fatalf("expected committed status, got %q", committedReservation.Status)
	}
	_, committed, reserved, err = ledger.Snapshot(scope)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if committed.Tokens != 18 || reserved != (Usage{}) {
		t.Fatalf("unexpected snapshot after commit: committed=%#v reserved=%#v", committed, reserved)
	}
}

func TestLedgerRejectsOverReservation(t *testing.T) {
	ledger := NewLedger()
	scope := testScope()
	if err := ledger.SetLimit(scope, Limit{MaxTokens: 10, MaxRequests: 1}); err != nil {
		t.Fatalf("set limit: %v", err)
	}

	_, err := ledger.Reserve(ReservationRequest{
		RequestID: "req_1",
		Scope:     scope,
		Estimate:  Usage{Tokens: 11, Requests: 1},
	})
	if !errors.Is(err, ErrQuotaExceeded) {
		t.Fatalf("expected ErrQuotaExceeded, got %v", err)
	}
}

func TestLedgerReleaseReturnsReservation(t *testing.T) {
	ledger := NewLedger()
	scope := testScope()
	if err := ledger.SetLimit(scope, Limit{MaxTokens: 10, MaxRequests: 1}); err != nil {
		t.Fatalf("set limit: %v", err)
	}
	if _, err := ledger.Reserve(ReservationRequest{RequestID: "req_1", Scope: scope, Estimate: Usage{Tokens: 10, Requests: 1}}); err != nil {
		t.Fatalf("reserve: %v", err)
	}
	if _, err := ledger.Release("req_1"); err != nil {
		t.Fatalf("release: %v", err)
	}
	if _, err := ledger.Reserve(ReservationRequest{RequestID: "req_2", Scope: scope, Estimate: Usage{Tokens: 10, Requests: 1}}); err != nil {
		t.Fatalf("reserve after release should fit: %v", err)
	}
}

func TestLedgerCommitIsIdempotent(t *testing.T) {
	ledger := NewLedger()
	scope := testScope()
	if _, err := ledger.Reserve(ReservationRequest{RequestID: "req_1", Scope: scope, Estimate: Usage{Tokens: 10, Requests: 1}}); err != nil {
		t.Fatalf("reserve: %v", err)
	}
	first, err := ledger.Commit("req_1", Usage{Tokens: 8, Requests: 1})
	if err != nil {
		t.Fatalf("first commit: %v", err)
	}
	second, err := ledger.Commit("req_1", Usage{Tokens: 9, Requests: 1})
	if err != nil {
		t.Fatalf("second commit: %v", err)
	}
	if first.Actual != second.Actual {
		t.Fatalf("expected second commit to return original actual usage, got %#v and %#v", first.Actual, second.Actual)
	}
}

func TestLedgerSnapshotsReturnsLimitsAndUsage(t *testing.T) {
	ledger := NewLedger()
	scope := testScope()
	otherScope := Scope{TenantID: "team-a", UserID: "usr_1", APIKeyID: "key_1", Model: "claude-default"}
	if err := ledger.SetLimit(scope, Limit{MaxTokens: 100, MaxRequests: 10}); err != nil {
		t.Fatalf("set limit: %v", err)
	}
	if _, err := ledger.Reserve(ReservationRequest{RequestID: "req_1", Scope: scope, Estimate: Usage{Tokens: 10, Requests: 1}}); err != nil {
		t.Fatalf("reserve: %v", err)
	}
	if _, err := ledger.Commit("req_1", Usage{Tokens: 8, Requests: 1}); err != nil {
		t.Fatalf("commit: %v", err)
	}
	if err := ledger.SetLimit(otherScope, Limit{MaxTokens: 50, MaxRequests: 5}); err != nil {
		t.Fatalf("set other limit: %v", err)
	}

	snapshots := ledger.Snapshots()
	if len(snapshots) != 2 {
		t.Fatalf("expected two snapshots, got %#v", snapshots)
	}
	for _, snapshot := range snapshots {
		if snapshot.Scope == scope {
			if snapshot.Limit.MaxTokens != 100 || snapshot.Committed.Tokens != 8 || snapshot.Reserved.Tokens != 0 {
				t.Fatalf("unexpected primary snapshot: %#v", snapshot)
			}
			return
		}
	}
	t.Fatalf("primary snapshot missing: %#v", snapshots)
}

func TestLedgerConcurrentReservationsDoNotOverrunQuota(t *testing.T) {
	ledger := NewLedger()
	scope := testScope()
	if err := ledger.SetLimit(scope, Limit{MaxTokens: 100, MaxRequests: 100}); err != nil {
		t.Fatalf("set limit: %v", err)
	}

	var wg sync.WaitGroup
	var mu sync.Mutex
	successes := 0
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := ledger.Reserve(ReservationRequest{
				RequestID: fmt.Sprintf("req_%02d", i),
				Scope:     scope,
				Estimate:  Usage{Tokens: 10, Requests: 1},
			})
			if err == nil {
				mu.Lock()
				successes++
				mu.Unlock()
			} else if !errors.Is(err, ErrQuotaExceeded) {
				t.Errorf("unexpected reserve error: %v", err)
			}
		}(i)
	}
	wg.Wait()

	if successes != 10 {
		t.Fatalf("expected 10 successful reservations, got %d", successes)
	}
	_, _, reserved, err := ledger.Snapshot(scope)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if reserved.Tokens != 100 {
		t.Fatalf("expected exactly 100 reserved tokens, got %d", reserved.Tokens)
	}
}

func testScope() Scope {
	return Scope{
		TenantID: "team-a",
		UserID:   "usr_1",
		APIKeyID: "key_1",
		Model:    "gpt-5-codex",
	}
}
