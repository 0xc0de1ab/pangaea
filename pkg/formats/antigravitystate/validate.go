package antigravitystate

import (
	"context"
	"time"

	"github.com/0xc0de1ab/pangaea/pkg/formats"
)

const eagerRefreshThreshold = 5 * time.Minute

func (Format) Validate(_ context.Context, snap formats.Snapshot, opts formats.ValidateOpts) (formats.ValidationResult, error) {
	now := time.Now
	if opts.Clock != nil {
		now = opts.Clock
	}
	checkedAt := now()

	exp := snap.ExpiresAt()
	if exp.IsZero() {
		return formats.ValidationResult{
			Status:    formats.StatusExpired,
			Detail:    "antigravity oauth expiry is absent",
			CheckedAt: checkedAt,
		}, nil
	}
	if !checkedAt.Add(eagerRefreshThreshold).Before(exp) {
		// Antigravity's state.vscdb expiry is advisory for a running sidecar:
		// ls-core can refresh its in-memory token without flushing the updated
		// timestamp back to the SQLite file until shutdown. Treat a recognizable
		// auth snapshot as routeable; actual upstream 401/403 responses still
		// mark the provider auth unavailable through the API provider layer.
		return formats.ValidationResult{
			Status:    formats.StatusOK,
			Detail:    "antigravity oauth expiry is stale in state.vscdb but may be refreshed in ls-core memory",
			CheckedAt: checkedAt,
		}, nil
	}
	return formats.ValidationResult{Status: formats.StatusOK, CheckedAt: checkedAt}, nil
}
