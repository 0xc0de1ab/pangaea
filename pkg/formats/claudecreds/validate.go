package claudecreds

import (
	"context"
	"time"

	"github.com/dh-kam/claude-creds-share/pkg/formats"
)

// Validate runs local checks (expiry vs Clock) and, if opts.LiveCheck is set,
// delegates to the GET-only live check. It does not return errors for
// network/HTTP failures — those surface as Status=unreachable so callers can
// treat them as soft-degraded states rather than fatal.
func (f Format) Validate(ctx context.Context, snap formats.Snapshot, opts formats.ValidateOpts) (formats.ValidationResult, error) {
	now := time.Now
	if opts.Clock != nil {
		now = opts.Clock
	}
	checkedAt := now()

	exp := snap.ExpiresAt()
	if exp.IsZero() || !checkedAt.Before(exp) {
		return formats.ValidationResult{
			Status:    formats.StatusExpired,
			Detail:    "expiresAt has passed",
			CheckedAt: checkedAt,
		}, nil
	}

	if !opts.LiveCheck {
		return formats.ValidationResult{
			Status:    formats.StatusOK,
			CheckedAt: checkedAt,
		}, nil
	}

	cs, ok := snap.(*snapshot)
	if !ok {
		// Defensive: a foreign Snapshot implementation can't be live-checked
		// because we lack the access token. Treat as ok-by-local-checks so we
		// don't falsely report unreachable.
		return formats.ValidationResult{
			Status:    formats.StatusOK,
			Detail:    "live check skipped: foreign Snapshot implementation",
			CheckedAt: checkedAt,
		}, nil
	}

	return liveCheck(ctx, cs, opts, checkedAt), nil
}
