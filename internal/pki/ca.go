// Package pki builds the small, self-signed PKI used by claude-creds-share:
// a single root CA, server and client leaf certificates, and matching
// *tls.Config builders for the mTLS endpoints. Keys are ECDSA P-256;
// algorithms and parameters track the design in docs/design/specs.md §4.
package pki

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/dh-kam/claude-creds-share/internal/common"
)

// CA bundles a parsed CA certificate with its private signing key. Key is held
// as a crypto.Signer so callers cannot accidentally inspect the raw scalar.
type CA struct {
	Cert *x509.Certificate
	Key  crypto.Signer
}

const (
	caCertFile = "ca.crt"
	caKeyFile  = "ca.key"

	pemBlockCert  = "CERTIFICATE"
	pemBlockECKey = "EC PRIVATE KEY"
	pemBlockPKCS8 = "PRIVATE KEY"

	keyFileMode  = 0o600
	certFileMode = 0o644
	dirMode      = 0o755
)

// LoadCA reads a PEM CA certificate and PEM-encoded ECDSA private key from
// disk. Both files are required.
func LoadCA(certPath, keyPath string) (*CA, error) {
	cert, err := loadCertFile(certPath)
	if err != nil {
		return nil, err
	}
	key, err := loadKeyFile(keyPath)
	if err != nil {
		return nil, err
	}
	if !cert.IsCA {
		return nil, common.Wrap(nil, common.ErrConfigInvalid, "certificate at %s is not a CA", certPath)
	}
	return &CA{Cert: cert, Key: key}, nil
}

// NewCA generates a fresh self-signed root CA in outDir. It writes ca.crt
// (0644) and ca.key (0600). The key is ECDSA P-256. notAfter defines the CA
// validity window; the caller chooses the lifetime (specs §4.1 suggests 10y).
func NewCA(outDir, commonName string, notAfter time.Time) (*CA, error) {
	if commonName == "" {
		return nil, common.Wrap(nil, common.ErrConfigInvalid, "CA common name is required")
	}
	if notAfter.Before(time.Now()) {
		return nil, common.Wrap(nil, common.ErrConfigInvalid, common.MsgNotAfterInPast)
	}
	if err := os.MkdirAll(outDir, dirMode); err != nil {
		return nil, common.Wrap(err, common.ErrConfigInvalid, "mkdir %s", outDir)
	}

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, common.Wrap(err, common.ErrConfigInvalid, "generate CA key")
	}

	serial, err := randomSerial()
	if err != nil {
		return nil, err
	}
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: commonName},
		NotBefore:             time.Now().Add(-1 * time.Minute),
		NotAfter:              notAfter,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLen:            0,
		MaxPathLenZero:        true,
	}

	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, common.Wrap(err, common.ErrConfigInvalid, "create CA certificate")
	}

	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, common.Wrap(err, common.ErrConfigInvalid, "parse generated CA cert")
	}

	if err := writeBackupAndPEM(filepath.Join(outDir, caCertFile), pemBlockCert, der, certFileMode); err != nil {
		return nil, err
	}
	if err := writePrivateKey(filepath.Join(outDir, caKeyFile), key); err != nil {
		return nil, err
	}

	return &CA{Cert: cert, Key: key}, nil
}

func loadCertFile(path string) (*x509.Certificate, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, common.Wrap(err, common.ErrConfigInvalid, "read certificate %s", path)
	}
	block, _ := pem.Decode(raw)
	if block == nil || block.Type != pemBlockCert {
		return nil, common.Wrap(nil, common.ErrConfigInvalid, "no PEM CERTIFICATE block in %s", path)
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, common.Wrap(err, common.ErrConfigInvalid, "parse certificate %s", path)
	}
	return cert, nil
}

func loadKeyFile(path string) (crypto.Signer, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, common.Wrap(err, common.ErrConfigInvalid, "read key %s", path)
	}
	block, _ := pem.Decode(raw)
	if block == nil {
		return nil, common.Wrap(nil, common.ErrConfigInvalid, "no PEM block in %s", path)
	}
	switch block.Type {
	case pemBlockECKey:
		key, err := x509.ParseECPrivateKey(block.Bytes)
		if err != nil {
			return nil, common.Wrap(err, common.ErrConfigInvalid, "parse EC key %s", path)
		}
		return key, nil
	case pemBlockPKCS8:
		anyKey, err := x509.ParsePKCS8PrivateKey(block.Bytes)
		if err != nil {
			return nil, common.Wrap(err, common.ErrConfigInvalid, "parse PKCS8 key %s", path)
		}
		signer, ok := anyKey.(crypto.Signer)
		if !ok {
			return nil, common.Wrap(nil, common.ErrConfigInvalid, "key %s is not a signer", path)
		}
		return signer, nil
	default:
		return nil, common.Wrap(nil, common.ErrConfigInvalid, "unsupported PEM type %q in %s", block.Type, path)
	}
}

func writePrivateKey(path string, key *ecdsa.PrivateKey) error {
	der, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return common.Wrap(err, common.ErrConfigInvalid, "marshal EC private key")
	}
	return writeBackupAndPEM(path, pemBlockECKey, der, keyFileMode)
}

// writeBackupAndPEM serialises der as a PEM block and writes it to path. If
// path already exists it is moved aside to "<path>.bak.<unix>" first so a
// reissue never silently destroys credentials.
func writeBackupAndPEM(path, blockType string, der []byte, mode os.FileMode) error {
	if err := backupIfExists(path); err != nil {
		return err
	}
	buf := pem.EncodeToMemory(&pem.Block{Type: blockType, Bytes: der})
	if buf == nil {
		return common.Wrap(nil, common.ErrConfigInvalid, "encode PEM %s", path)
	}
	if err := os.WriteFile(path, buf, mode); err != nil {
		return common.Wrap(err, common.ErrConfigInvalid, "write %s", path)
	}
	// WriteFile honours umask; reassert mode explicitly so 0600 stays 0600.
	if err := os.Chmod(path, mode); err != nil {
		return common.Wrap(err, common.ErrConfigInvalid, "chmod %s", path)
	}
	return nil
}

func backupIfExists(path string) error {
	if _, err := os.Stat(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return common.Wrap(err, common.ErrConfigInvalid, "stat %s", path)
	}
	// .bak.<unix> per task spec. Use UnixNano so concurrent reissues within a
	// second still produce unique backup names.
	bak := path + ".bak." + strconv.FormatInt(time.Now().UnixNano(), 10)
	if err := os.Rename(path, bak); err != nil {
		return common.Wrap(err, common.ErrConfigInvalid, "backup %s -> %s", path, bak)
	}
	return nil
}

func randomSerial() (*big.Int, error) {
	max := new(big.Int).Lsh(big.NewInt(1), 128)
	n, err := rand.Int(rand.Reader, max)
	if err != nil {
		return nil, common.Wrap(err, common.ErrConfigInvalid, "serial")
	}
	return n, nil
}
