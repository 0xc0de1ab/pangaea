package codexauth

import "github.com/0xc0de1ab/pangaea/pkg/formats"

// Compare orders two snapshots. Only StrategyJWTExpMax is supported.
//
// Ordering: prefer later access-token JWT exp; tiebreak on later last_refresh.
// Mirrors how codex itself decides which on-disk auth state is current — no
// other field carries a monotonic timestamp.
//
// Return values follow the formats.Format convention:
//
//	-1 if a is older than b
//	 0 if equal
//	+1 if a is newer than b
func (Format) Compare(strategy string, a, b formats.Snapshot) int {
	strategyMustBeKnown(strategy)

	as, aOk := a.(*snapshot)
	bs, bOk := b.(*snapshot)

	// Both foreign Snapshot impls — fall back to ExpiresAt only.
	if !aOk || !bOk {
		ae, be := a.ExpiresAt(), b.ExpiresAt()
		switch {
		case ae.After(be):
			return 1
		case ae.Before(be):
			return -1
		default:
			return 0
		}
	}

	switch {
	case as.jwtExp.After(bs.jwtExp):
		return 1
	case as.jwtExp.Before(bs.jwtExp):
		return -1
	}
	switch {
	case as.lastRefresh.After(bs.lastRefresh):
		return 1
	case as.lastRefresh.Before(bs.lastRefresh):
		return -1
	}
	return 0
}
