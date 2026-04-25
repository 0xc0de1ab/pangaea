package codexauth

import "github.com/0xc0de1ab/pangaea/pkg/formats"

const fingerprintShortLen = 12

// Redact returns the log/network-safe Summary view.
//
// SECURITY: this method MUST NOT include access_token, refresh_token, id_token
// or OPENAI_API_KEY in any field. Only the last 4 chars of access_token are
// exposed via TokenTail4 to help operators correlate two events without
// disclosing the secret.
func (f Format) Redact(snap formats.Snapshot) formats.Summary {
	cs, ok := snap.(*snapshot)
	if !ok {
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
		tail4 = cs.accessToken
	}

	extra := map[string]string{}
	if cs.authMode != "" {
		extra["auth_mode"] = cs.authMode
	}
	if cs.chatgptEmail != "" {
		extra["email"] = cs.chatgptEmail
	}
	if cs.accountID != "" {
		extra["account_id"] = cs.accountID
	}
	if !cs.lastRefresh.IsZero() {
		extra["last_refresh"] = cs.lastRefresh.UTC().Format("2006-01-02T15:04:05Z")
	}
	if len(extra) == 0 {
		extra = nil
	}

	return formats.Summary{
		Identity:         cs.identity,
		FingerprintShort: short,
		TokenTail4:       tail4,
		ExpiresAt:        cs.jwtExp,
		Extra:            extra,
	}
}
