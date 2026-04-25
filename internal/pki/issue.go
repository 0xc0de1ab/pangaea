package pki

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"net"
	"os"
	"path/filepath"
	"time"

	"github.com/dh-kam/claude-creds-share/internal/common"
)

// SAN bundles Subject Alternative Names for a server certificate. Both lists
// may carry entries; at least one must be non-empty.
type SAN struct {
	IPs []net.IP
	DNS []string
}

const (
	serverCertFile = "server.crt"
	serverKeyFile  = "server.key"
	clientCertFile = "client.crt"
	clientKeyFile  = "client.key"
)

// IssueServer signs a server leaf certificate using ca and writes server.crt
// and server.key into outDir. SAN entries become x509 IP/DNS SANs. An empty
// SAN is rejected to prevent accidentally issuing a certificate that no
// hostname can satisfy.
func IssueServer(ca *CA, outDir, commonName string, san SAN, notAfter time.Time) error {
	if err := validateLeafInputs(ca, commonName, notAfter); err != nil {
		return err
	}
	if len(san.IPs) == 0 && len(san.DNS) == 0 {
		return common.Wrap(nil, common.ErrConfigInvalid, common.MsgSANEmpty)
	}
	if err := os.MkdirAll(outDir, dirMode); err != nil {
		return common.Wrap(err, common.ErrConfigInvalid, "mkdir %s", outDir)
	}

	tmpl, err := baseLeafTemplate(commonName, notAfter)
	if err != nil {
		return err
	}
	tmpl.KeyUsage = x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment
	tmpl.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}
	// Copy SAN fields so callers can't mutate the issued certificate template
	// after the fact.
	tmpl.IPAddresses = append(tmpl.IPAddresses, san.IPs...)
	tmpl.DNSNames = append(tmpl.DNSNames, san.DNS...)

	return signAndWriteLeaf(ca, tmpl, filepath.Join(outDir, serverCertFile), filepath.Join(outDir, serverKeyFile))
}

// IssueClient signs a client leaf certificate. The CN doubles as the node id
// in the protocol; the server's ACL keys off it (specs §4.5).
func IssueClient(ca *CA, outDir, commonName string, notAfter time.Time) error {
	if err := validateLeafInputs(ca, commonName, notAfter); err != nil {
		return err
	}
	if err := os.MkdirAll(outDir, dirMode); err != nil {
		return common.Wrap(err, common.ErrConfigInvalid, "mkdir %s", outDir)
	}

	tmpl, err := baseLeafTemplate(commonName, notAfter)
	if err != nil {
		return err
	}
	tmpl.KeyUsage = x509.KeyUsageDigitalSignature
	tmpl.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}

	return signAndWriteLeaf(ca, tmpl, filepath.Join(outDir, clientCertFile), filepath.Join(outDir, clientKeyFile))
}

func validateLeafInputs(ca *CA, commonName string, notAfter time.Time) error {
	if ca == nil || ca.Cert == nil || ca.Key == nil {
		return common.Wrap(nil, common.ErrConfigInvalid, "CA is required")
	}
	if commonName == "" {
		return common.Wrap(nil, common.ErrConfigInvalid, "common name is required")
	}
	if !notAfter.After(time.Now()) {
		return common.Wrap(nil, common.ErrConfigInvalid, common.MsgNotAfterInPast)
	}
	if notAfter.After(ca.Cert.NotAfter) {
		return common.Wrap(nil, common.ErrConfigInvalid, "leaf notAfter exceeds CA notAfter")
	}
	return nil
}

func baseLeafTemplate(commonName string, notAfter time.Time) (*x509.Certificate, error) {
	serial, err := randomSerial()
	if err != nil {
		return nil, err
	}
	return &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: commonName},
		NotBefore:             time.Now().Add(-1 * time.Minute),
		NotAfter:              notAfter,
		BasicConstraintsValid: true,
		IsCA:                  false,
	}, nil
}

func signAndWriteLeaf(ca *CA, tmpl *x509.Certificate, certPath, keyPath string) error {
	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return common.Wrap(err, common.ErrConfigInvalid, "generate leaf key")
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca.Cert, &leafKey.PublicKey, ca.Key)
	if err != nil {
		return common.Wrap(err, common.ErrConfigInvalid, "sign leaf certificate")
	}
	if err := writeBackupAndPEM(certPath, pemBlockCert, der, certFileMode); err != nil {
		return err
	}
	if err := writePrivateKey(keyPath, leafKey); err != nil {
		return err
	}
	return nil
}
