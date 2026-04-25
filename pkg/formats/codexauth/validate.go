package codexauth

import (
	"context"
	"time"

	"github.com/0xc0de1ab/pangaea/pkg/formats"
)

// proactiveRefreshInterval mirrors codex's TOKEN_REFRESH_INTERVAL constant
// (codex-rs/login/src/auth/manager.rs): if last_refresh is older than this,
// codex itself proactively refreshes regardless of JWT exp. We treat the same
// threshold as "not fresh enough to share" — pushing this token to a peer
// would have it perform the same proactive refresh on its own next start.
const proactiveRefreshInterval = 8 * 24 * time.Hour

// Validate runs local checks. There is no live check: codex's only validity
// signal is the access-token JWT's exp claim plus the 8-day proactive-refresh
// window. We replicate the same logic here, returning StatusExpired in both
// cases (the wire protocol has no separate "stale" status, and from the peer
// node's perspective both outcomes mean "do not adopt as truth").
func (f Format) Validate(_ context.Context, snap formats.Snapshot, opts formats.ValidateOpts) (formats.ValidationResult, error) {
	now := time.Now
	if opts.Clock != nil {
		now = opts.Clock
	}
	checkedAt := now()

	cs, ok := snap.(*snapshot)
	if !ok {
		exp := snap.ExpiresAt()
		if exp.IsZero() || !checkedAt.Before(exp) {
			return formats.ValidationResult{
				Status:    formats.StatusExpired,
				Detail:    "JWT exp has passed",
				CheckedAt: checkedAt,
			}, nil
		}
		return formats.ValidationResult{Status: formats.StatusOK, CheckedAt: checkedAt}, nil
	}

	if cs.jwtExp.IsZero() {
		// We could not decode the access-token JWT. Codex itself would still
		// try the token at that point, but for a peer we can't make a fresh
		// claim — treat as scope_warn so the mediator may still consider it
		// viable but won't prefer it without other signals.
		return formats.ValidationResult{
			Status:    formats.StatusScopeWarn,
			Detail:    "access_token JWT exp not parseable",
			CheckedAt: checkedAt,
		}, nil
	}
	if !checkedAt.Before(cs.jwtExp) {
		return formats.ValidationResult{
			Status:    formats.StatusExpired,
			Detail:    "access_token JWT exp has passed",
			CheckedAt: checkedAt,
		}, nil
	}
	if !cs.lastRefresh.IsZero() && checkedAt.Sub(cs.lastRefresh) > proactiveRefreshInterval {
		return formats.ValidationResult{
			Status:    formats.StatusExpired,
			Detail:    "last_refresh older than codex proactive-refresh interval (8d)",
			CheckedAt: checkedAt,
		}, nil
	}

	return formats.ValidationResult{
		Status:    formats.StatusOK,
		CheckedAt: checkedAt,
	}, nil
}
