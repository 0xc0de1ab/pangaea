package claudecreds

import "github.com/0xc0de1ab/pangaea/pkg/formats"

// Compare orders two snapshots under the named strategy. Only
// StrategyExpiresAtMax is supported; any other strategy panics with a message
// pointing at the caller's contract violation.
//
// Return values follow the formats.Format convention:
//
//	-1 if a is older than b
//	 0 if equal
//	+1 if a is newer than b
func (Format) Compare(strategy string, a, b formats.Snapshot) int {
	strategyMustBeKnown(strategy)

	ae := a.ExpiresAt()
	be := b.ExpiresAt()
	switch {
	case ae.After(be):
		return 1
	case ae.Before(be):
		return -1
	default:
		return 0
	}
}
