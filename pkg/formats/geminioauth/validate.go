package geminioauth

import (
	"context"
	"time"

	"github.com/0xc0de1ab/pangaea/pkg/formats"
)

// eagerRefreshThreshold mirrors google-auth-library's
// `eagerRefreshThresholdMillis` constant (5 minutes). The library treats a
// token whose expiry_date is within this window as already-expiring and
// kicks off a refresh, so a peer adopting it would do the same. We treat it
// as expired here for the mediator's "viable candidate" decision: there is
// no point pushing a token that would force the receiver to refresh on first
// use.
const eagerRefreshThreshold = 5 * time.Minute

// Validate runs local checks. There is no live check: gemini-cli's only
// freshness signal on disk is expiry_date, and the library applies a 5-minute
// eager-refresh skew. We do the same.
func (f Format) Validate(_ context.Context, snap formats.Snapshot, opts formats.ValidateOpts) (formats.ValidationResult, error) {
	now := time.Now
	if opts.Clock != nil {
		now = opts.Clock
	}
	checkedAt := now()

	exp := snap.ExpiresAt()
	if exp.IsZero() {
		return formats.ValidationResult{
			Status:    formats.StatusExpired,
			Detail:    "expiry_date is absent",
			CheckedAt: checkedAt,
		}, nil
	}
	if !checkedAt.Add(eagerRefreshThreshold).Before(exp) {
		return formats.ValidationResult{
			Status:    formats.StatusExpired,
			Detail:    "expiry_date within 5-minute eager-refresh window",
			CheckedAt: checkedAt,
		}, nil
	}
	return formats.ValidationResult{
		Status:    formats.StatusOK,
		CheckedAt: checkedAt,
	}, nil
}
