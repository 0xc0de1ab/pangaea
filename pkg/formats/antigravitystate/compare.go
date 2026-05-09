package antigravitystate

import "github.com/0xc0de1ab/pangaea/pkg/formats"

func (Format) Compare(strategy string, a, b formats.Snapshot) int {
	strategyMustBeKnown(strategy)
	ae, be := a.ExpiresAt(), b.ExpiresAt()
	switch {
	case ae.After(be):
		return 1
	case ae.Before(be):
		return -1
	case a.Fingerprint() > b.Fingerprint():
		return 1
	case a.Fingerprint() < b.Fingerprint():
		return -1
	default:
		return 0
	}
}
