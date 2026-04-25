package geminioauth

import "github.com/0xc0de1ab/pangaea/pkg/formats"

// Compare orders two snapshots. Only StrategyExpiryDateMax is supported.
//
// Ordering: prefer later expiry_date (epoch ms). The on-disk schema has no
// other monotonic field — id_token differs each refresh but its claims do
// not carry a useful timestamp for ordering — so a single comparison field
// is the right model.
//
// Return values follow the formats.Format convention:
//
//	-1 if a is older than b
//	 0 if equal
//	+1 if a is newer than b
func (Format) Compare(strategy string, a, b formats.Snapshot) int {
	strategyMustBeKnown(strategy)
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
