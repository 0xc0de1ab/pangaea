package pki

import (
	"crypto/x509"
	"path/filepath"
	"strings"
	"time"

	"github.com/0xc0de1ab/pangaea/internal/common"
)

// VerifyServerCert validates a server leaf against the CA pool and confirms
// serverName is covered by the certificate SANs.
func VerifyServerCert(caCertPath, certPath, serverName string, now time.Time) (*x509.Certificate, error) {
	if serverName == "" {
		return nil, common.Wrap(nil, common.ErrConfigInvalid, "server_name is required")
	}
	cert, err := loadCertFile(certPath)
	if err != nil {
		return nil, err
	}
	pool, err := LoadCAPool(caCertPath)
	if err != nil {
		return nil, err
	}
	opts := x509.VerifyOptions{
		Roots:       pool,
		CurrentTime: now,
		DNSName:     serverName,
		KeyUsages:   []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	if _, err := cert.Verify(opts); err != nil {
		return nil, common.Wrap(err, common.ErrConfigInvalid, "verify server cert %s", certPath)
	}
	return cert, nil
}

// VerifyClientCert validates a client leaf against the CA pool. If expectedCN
// is non-empty, the verified certificate must match it exactly.
func VerifyClientCert(caCertPath, certPath, expectedCN string, now time.Time) (*x509.Certificate, error) {
	cert, err := loadCertFile(certPath)
	if err != nil {
		return nil, err
	}
	pool, err := LoadCAPool(caCertPath)
	if err != nil {
		return nil, err
	}
	opts := x509.VerifyOptions{
		Roots:       pool,
		CurrentTime: now,
		KeyUsages:   []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}
	if _, err := cert.Verify(opts); err != nil {
		return nil, common.Wrap(err, common.ErrConfigInvalid, "verify client cert %s", certPath)
	}
	if expectedCN != "" && cert.Subject.CommonName != expectedCN {
		return nil, common.Wrap(nil, common.ErrCNMismatch, "client CN %q does not match expected %q", cert.Subject.CommonName, expectedCN)
	}
	return cert, nil
}

// DefaultLeafPath returns the conventional leaf path under dir for use by CLI
// commands that operate on the files issued by IssueServer/IssueClient.
func DefaultLeafPath(dir, filename string) string {
	return filepath.Join(strings.TrimSpace(dir), filename)
}
