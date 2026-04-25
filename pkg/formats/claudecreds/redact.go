package claudecreds

import "github.com/0xc0de1ab/pangaea/pkg/formats"

// fingerprintShortLen matches specs §9.5: identity-style short fingerprints
// are 12 hex chars (~6 bytes of entropy) — enough to disambiguate in logs
// without leaking the full hash.
const fingerprintShortLen = 12

// Redact returns the log/network-safe Summary view of the snapshot.
//
// SECURITY: this method MUST NOT include accessToken or refreshToken in any
// field. Only the last 4 chars of accessToken are exposed via TokenTail4 to
// help operators correlate two events without disclosing the secret.
func (f Format) Redact(snap formats.Snapshot) formats.Summary {
	cs, ok := snap.(*snapshot)
	if !ok {
		// Foreign Snapshot — fall back to public interface methods only.
		fp := snap.Fingerprint()
		short := fp
		if len(fp) > fingerprintShortLen {
			short = fp[:fingerprintShortLen]
		}
		return formats.Summary{
			Identity:         snap.Identity(),
			ExpiresAt:        snap.ExpiresAt(),
			FingerprintShort: short,
		}
	}

	short := cs.fingerprint
	if len(short) > fingerprintShortLen {
		short = short[:fingerprintShortLen]
	}

	tail4 := ""
	if n := len(cs.accessToken); n >= 4 {
		tail4 = cs.accessToken[n-4:]
	} else {
		tail4 = cs.accessToken // shorter than 4 chars; still not a secret on its own
	}

	scopesCopy := append([]string(nil), cs.scopes...)

	return formats.Summary{
		Identity:         cs.identity,
		Subscription:     cs.subscriptionType,
		FingerprintShort: short,
		TokenTail4:       tail4,
		ExpiresAt:        cs.expiresAt,
		Scopes:           scopesCopy,
	}
}
