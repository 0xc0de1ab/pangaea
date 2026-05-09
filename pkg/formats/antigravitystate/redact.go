package antigravitystate

import (
	"time"

	"github.com/0xc0de1ab/pangaea/pkg/formats"
)

const fingerprintShortLen = 12

func (Format) Redact(snap formats.Snapshot) formats.Summary {
	short := snap.Fingerprint()
	if len(short) > fingerprintShortLen {
		short = short[:fingerprintShortLen]
	}

	out := formats.Summary{
		Identity:         snap.Identity(),
		FingerprintShort: short,
		ExpiresAt:        snap.ExpiresAt(),
	}
	if s, ok := snap.(*snapshot); ok {
		out.TokenTail4 = s.tokenTail4
		extra := map[string]string{}
		if s.account != "" {
			extra["email"] = s.account
		}
		if !s.expiresAt.IsZero() {
			extra["oauth_expiry"] = s.expiresAt.UTC().Format(time.RFC3339)
		}
		if len(extra) > 0 {
			out.Extra = extra
		}
	}
	return out
}
