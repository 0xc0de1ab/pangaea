package pki

import (
	"crypto/x509"
	"os"

	"github.com/dh-kam/claude-creds-share/internal/common"
)

// LoadCAPool loads a PEM CA bundle from disk into an x509.CertPool. A file
// that contains zero parseable certificates is rejected as invalid config
// rather than silently producing an empty pool.
func LoadCAPool(caCertPath string) (*x509.CertPool, error) {
	raw, err := os.ReadFile(caCertPath)
	if err != nil {
		return nil, common.Wrap(err, common.ErrConfigInvalid, "read CA bundle %s", caCertPath)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(raw) {
		return nil, common.Wrap(nil, common.ErrConfigInvalid, "no usable certificates in %s", caCertPath)
	}
	return pool, nil
}
