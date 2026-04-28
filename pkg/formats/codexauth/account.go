package codexauth

import (
	"context"

	"github.com/0xc0de1ab/pangaea/pkg/formats"
)

// Account derives a stable per-account identifier from the snapshot. It
// prefers the chatgpt_user_id claim from id_token (survives refreshes),
// falls back to the top-level tokens.account_id field, then to the email
// claim. Returns "" only when none of those are present (e.g. ApiKey-only
// auth, or a stripped id_token) — the server then keeps such reports in a
// shared bucket.
//
// path is unused: codex auth.json is self-contained for account identity.
func (Format) Account(_ context.Context, snap formats.Snapshot, _ string) (string, error) {
	cs, ok := snap.(*snapshot)
	if !ok {
		return "", nil
	}
	if cs.chatgptUserID != "" {
		return cs.chatgptUserID, nil
	}
	if cs.accountID != "" {
		return cs.accountID, nil
	}
	if cs.chatgptEmail != "" {
		return cs.chatgptEmail, nil
	}
	return "", nil
}

func (Format) AccountDisplay(_ context.Context, snap formats.Snapshot, _ string) (string, error) {
	cs, ok := snap.(*snapshot)
	if !ok {
		return "", nil
	}
	if cs.chatgptEmail != "" {
		return cs.chatgptEmail, nil
	}
	return "", nil
}
