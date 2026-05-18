// Package quota contains router-side quota reservation and settlement
// primitives. The initial implementation is an in-memory ledger for tests and
// simulator-backed routing; persistent stores can implement the same contract.
package quota

import (
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"
)

var (
	ErrInvalidRequest      = errors.New("invalid quota request")
	ErrQuotaExceeded       = errors.New("quota exceeded")
	ErrReservationNotFound = errors.New("reservation not found")
	ErrReservationClosed   = errors.New("reservation closed")
)

type Scope struct {
	TenantID string `json:"tenant_id,omitempty"`
	UserID   string `json:"user_id,omitempty"`
	APIKeyID string `json:"api_key_id,omitempty"`
	Model    string `json:"model,omitempty"`
}

type Limit struct {
	MaxTokens   int64 `json:"max_tokens,omitempty"`
	MaxRequests int64 `json:"max_requests,omitempty"`
}

type Usage struct {
	Tokens   int64 `json:"tokens,omitempty"`
	Requests int64 `json:"requests,omitempty"`
}

type ReservationStatus string

const (
	ReservationReserved  ReservationStatus = "reserved"
	ReservationCommitted ReservationStatus = "committed"
	ReservationReleased  ReservationStatus = "released"
)

type ReservationRequest struct {
	RequestID string `json:"request_id"`
	Scope     Scope  `json:"scope"`
	Estimate  Usage  `json:"estimate"`
}

type Reservation struct {
	RequestID string            `json:"request_id"`
	Scope     Scope             `json:"scope"`
	Estimate  Usage             `json:"estimate"`
	Actual    Usage             `json:"actual,omitempty"`
	Status    ReservationStatus `json:"status"`
	CreatedAt time.Time         `json:"created_at"`
	ClosedAt  time.Time         `json:"closed_at,omitempty"`
}

type SnapshotRecord struct {
	Scope     Scope `json:"scope"`
	Limit     Limit `json:"limit,omitempty"`
	Committed Usage `json:"committed,omitempty"`
	Reserved  Usage `json:"reserved,omitempty"`
}

type Ledger struct {
	mu           sync.Mutex
	now          func() time.Time
	limits       map[Scope]Limit
	committed    map[Scope]Usage
	reserved     map[Scope]Usage
	reservations map[string]Reservation
}

func NewLedger() *Ledger {
	return NewLedgerWithClock(time.Now)
}

func NewLedgerWithClock(now func() time.Time) *Ledger {
	if now == nil {
		now = time.Now
	}
	return &Ledger{
		now:          now,
		limits:       make(map[Scope]Limit),
		committed:    make(map[Scope]Usage),
		reserved:     make(map[Scope]Usage),
		reservations: make(map[string]Reservation),
	}
}

func (l *Ledger) SetLimit(scope Scope, limit Limit) error {
	if err := validateScope(scope); err != nil {
		return err
	}
	if limit.MaxTokens < 0 || limit.MaxRequests < 0 {
		return fmt.Errorf("%w: negative limit", ErrInvalidRequest)
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.limits[scope] = limit
	return nil
}

func (l *Ledger) Reserve(request ReservationRequest) (Reservation, error) {
	if err := validateReservationRequest(request); err != nil {
		return Reservation{}, err
	}
	l.mu.Lock()
	defer l.mu.Unlock()

	if existing, ok := l.reservations[request.RequestID]; ok {
		if existing.Status != ReservationReleased {
			if existing.Scope != request.Scope {
				return Reservation{}, fmt.Errorf("%w: request_id scope mismatch", ErrInvalidRequest)
			}
			return existing, nil
		}
	}

	if err := l.canReserveLocked(request.Scope, request.Estimate); err != nil {
		return Reservation{}, err
	}
	reservation := Reservation{
		RequestID: request.RequestID,
		Scope:     request.Scope,
		Estimate:  request.Estimate,
		Status:    ReservationReserved,
		CreatedAt: l.now().UTC(),
	}
	l.reservations[request.RequestID] = reservation
	l.reserved[request.Scope] = addUsage(l.reserved[request.Scope], request.Estimate)
	return reservation, nil
}

func (l *Ledger) Commit(requestID string, actual Usage) (Reservation, error) {
	if requestID == "" {
		return Reservation{}, fmt.Errorf("%w: request_id is required", ErrInvalidRequest)
	}
	if err := validateUsage(actual); err != nil {
		return Reservation{}, err
	}
	l.mu.Lock()
	defer l.mu.Unlock()

	reservation, ok := l.reservations[requestID]
	if !ok {
		return Reservation{}, ErrReservationNotFound
	}
	switch reservation.Status {
	case ReservationCommitted:
		return reservation, nil
	case ReservationReleased:
		return Reservation{}, ErrReservationClosed
	}
	scope := reservation.Scope
	l.reserved[scope] = subtractUsage(l.reserved[scope], reservation.Estimate)
	l.committed[scope] = addUsage(l.committed[scope], actual)
	reservation.Actual = actual
	reservation.Status = ReservationCommitted
	reservation.ClosedAt = l.now().UTC()
	l.reservations[requestID] = reservation
	return reservation, nil
}

func (l *Ledger) Release(requestID string) (Reservation, error) {
	if requestID == "" {
		return Reservation{}, fmt.Errorf("%w: request_id is required", ErrInvalidRequest)
	}
	l.mu.Lock()
	defer l.mu.Unlock()

	reservation, ok := l.reservations[requestID]
	if !ok {
		return Reservation{}, ErrReservationNotFound
	}
	switch reservation.Status {
	case ReservationReleased:
		return reservation, nil
	case ReservationCommitted:
		return Reservation{}, ErrReservationClosed
	}
	l.reserved[reservation.Scope] = subtractUsage(l.reserved[reservation.Scope], reservation.Estimate)
	reservation.Status = ReservationReleased
	reservation.ClosedAt = l.now().UTC()
	l.reservations[requestID] = reservation
	return reservation, nil
}

func (l *Ledger) Snapshot(scope Scope) (Limit, Usage, Usage, error) {
	if err := validateScope(scope); err != nil {
		return Limit{}, Usage{}, Usage{}, err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.limits[scope], l.committed[scope], l.reserved[scope], nil
}

func (l *Ledger) Snapshots() []SnapshotRecord {
	l.mu.Lock()
	defer l.mu.Unlock()
	scopes := make(map[Scope]struct{}, len(l.limits)+len(l.committed)+len(l.reserved))
	for scope := range l.limits {
		scopes[scope] = struct{}{}
	}
	for scope := range l.committed {
		scopes[scope] = struct{}{}
	}
	for scope := range l.reserved {
		scopes[scope] = struct{}{}
	}
	out := make([]SnapshotRecord, 0, len(scopes))
	for scope := range scopes {
		out = append(out, SnapshotRecord{
			Scope:     scope,
			Limit:     l.limits[scope],
			Committed: l.committed[scope],
			Reserved:  l.reserved[scope],
		})
	}
	sort.Slice(out, func(i, j int) bool {
		a := out[i].Scope
		b := out[j].Scope
		switch {
		case a.TenantID != b.TenantID:
			return a.TenantID < b.TenantID
		case a.UserID != b.UserID:
			return a.UserID < b.UserID
		case a.APIKeyID != b.APIKeyID:
			return a.APIKeyID < b.APIKeyID
		default:
			return a.Model < b.Model
		}
	})
	return out
}

func (l *Ledger) RestoreSnapshots(records []SnapshotRecord) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.limits = make(map[Scope]Limit, len(records))
	l.committed = make(map[Scope]Usage, len(records))
	l.reserved = make(map[Scope]Usage, len(records))
	l.reservations = make(map[string]Reservation)
	for _, record := range records {
		if err := validateScope(record.Scope); err != nil {
			return err
		}
		if record.Limit.MaxTokens < 0 || record.Limit.MaxRequests < 0 {
			return fmt.Errorf("%w: negative limit", ErrInvalidRequest)
		}
		if err := validateUsage(record.Committed); err != nil {
			return err
		}
		if err := validateUsage(record.Reserved); err != nil {
			return err
		}
		if record.Limit != (Limit{}) {
			l.limits[record.Scope] = record.Limit
		}
		if record.Committed != (Usage{}) {
			l.committed[record.Scope] = record.Committed
		}
		if record.Reserved != (Usage{}) {
			l.reserved[record.Scope] = record.Reserved
		}
	}
	return nil
}

func (l *Ledger) canReserveLocked(scope Scope, estimate Usage) error {
	limit := l.limits[scope]
	if limit == (Limit{}) {
		return nil
	}
	current := addUsage(l.committed[scope], l.reserved[scope])
	next := addUsage(current, estimate)
	if limit.MaxTokens > 0 && next.Tokens > limit.MaxTokens {
		return fmt.Errorf("%w: tokens %d > %d", ErrQuotaExceeded, next.Tokens, limit.MaxTokens)
	}
	if limit.MaxRequests > 0 && next.Requests > limit.MaxRequests {
		return fmt.Errorf("%w: requests %d > %d", ErrQuotaExceeded, next.Requests, limit.MaxRequests)
	}
	return nil
}

func validateReservationRequest(request ReservationRequest) error {
	if request.RequestID == "" {
		return fmt.Errorf("%w: request_id is required", ErrInvalidRequest)
	}
	if err := validateScope(request.Scope); err != nil {
		return err
	}
	if err := validateUsage(request.Estimate); err != nil {
		return err
	}
	if request.Estimate == (Usage{}) {
		return fmt.Errorf("%w: estimate is required", ErrInvalidRequest)
	}
	return nil
}

func validateScope(scope Scope) error {
	if scope.TenantID == "" && scope.UserID == "" && scope.APIKeyID == "" {
		return fmt.Errorf("%w: tenant_id, user_id, or api_key_id is required", ErrInvalidRequest)
	}
	if scope.Model == "" {
		return fmt.Errorf("%w: model is required", ErrInvalidRequest)
	}
	return nil
}

func validateUsage(usage Usage) error {
	if usage.Tokens < 0 || usage.Requests < 0 {
		return fmt.Errorf("%w: negative usage", ErrInvalidRequest)
	}
	return nil
}

func addUsage(a, b Usage) Usage {
	return Usage{Tokens: a.Tokens + b.Tokens, Requests: a.Requests + b.Requests}
}

func subtractUsage(a, b Usage) Usage {
	return Usage{Tokens: max64(0, a.Tokens-b.Tokens), Requests: max64(0, a.Requests-b.Requests)}
}

func max64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
