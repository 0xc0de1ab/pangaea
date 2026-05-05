package codexauth

import (
	"context"
	"time"

	"github.com/0xc0de1ab/pangaea/pkg/formats"
)

// expirySafetySkew mirrors Codex's auth refresh skew: once an access token is
// within this window, Codex refreshes it before making chat requests. Treat it
// as expired for mediation so peers receive a token that is immediately usable.
const expirySafetySkew = 5 * time.Minute

// Validate runs local checks. There is no live check: codex's only validity
// signal is the access-token JWT's exp claim. id_token expiry is deliberately
// ignored here: Codex uses id_token for identity/account metadata, while chat
// requests and refresh decisions are governed by access_token + refresh_token.
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
	if !checkedAt.Add(expirySafetySkew).Before(cs.jwtExp) {
		return formats.ValidationResult{
			Status:    formats.StatusExpired,
			Detail:    "access_token JWT exp has passed or is within refresh safety window",
			CheckedAt: checkedAt,
		}, nil
	}

	return formats.ValidationResult{
		Status:    formats.StatusOK,
		CheckedAt: checkedAt,
	}, nil
}
