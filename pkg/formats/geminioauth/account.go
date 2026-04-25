package geminioauth

import (
	"context"

	"github.com/0xc0de1ab/pangaea/pkg/formats"
)

// Account derives a stable per-account identifier. Prefers Google's `sub`
// claim from id_token (a stable opaque user id), falling back to the email
// claim. Returns "" only if id_token is absent/unparseable — the server
// then keeps such reports in a shared bucket and the operator gets a
// debug-time warning rather than silent cross-account contamination.
//
// path is unused: gemini oauth_creds.json is self-contained for account
// identity (id_token is always issued at refresh time when scope=openid is
// requested, which gemini does).
func (Format) Account(_ context.Context, snap formats.Snapshot, _ string) (string, error) {
	cs, ok := snap.(*snapshot)
	if !ok {
		return "", nil
	}
	if cs.googleSub != "" {
		return cs.googleSub, nil
	}
	if cs.googleEmail != "" {
		return cs.googleEmail, nil
	}
	return "", nil
}
