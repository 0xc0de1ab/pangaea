package pki

import (
	"crypto/tls"

	"github.com/dh-kam/claude-creds-share/internal/common"
)

// PeerCN extracts the verified peer certificate's Subject CommonName from a
// TLS connection state. Returns common.ErrCNMismatch if the state has no
// verified chain or the leaf has an empty CN.
func PeerCN(state *tls.ConnectionState) (string, error) {
	if state == nil {
		return "", common.Wrap(nil, common.ErrCNMismatch, "no TLS state")
	}
	if len(state.VerifiedChains) == 0 || len(state.VerifiedChains[0]) == 0 {
		return "", common.Wrap(nil, common.ErrCNMismatch, "no verified peer chain")
	}
	cn := state.VerifiedChains[0][0].Subject.CommonName
	if cn == "" {
		return "", common.Wrap(nil, common.ErrCNMismatch, "peer certificate has empty CN")
	}
	return cn, nil
}
