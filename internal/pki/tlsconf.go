package pki

import (
	"crypto/tls"

	"github.com/dh-kam/claude-creds-share/internal/common"
)

// ServerTLSConfig builds the *tls.Config for the mTLS server endpoint:
// TLS 1.3 only, mandatory client certs, and a ClientCAs pool seeded from
// caCertPath.
func ServerTLSConfig(caCertPath, serverCertPath, serverKeyPath string) (*tls.Config, error) {
	pool, err := LoadCAPool(caCertPath)
	if err != nil {
		return nil, err
	}
	cert, err := tls.LoadX509KeyPair(serverCertPath, serverKeyPath)
	if err != nil {
		return nil, common.Wrap(err, common.ErrTLSHandshake, "load server keypair")
	}
	return &tls.Config{
		MinVersion:   tls.VersionTLS13,
		Certificates: []tls.Certificate{cert},
		ClientCAs:    pool,
		ClientAuth:   tls.RequireAndVerifyClientCert,
	}, nil
}

// ClientTLSConfig builds the *tls.Config used by the client to dial the
// server. ServerName must match a SAN on the server cert.
func ClientTLSConfig(caCertPath, clientCertPath, clientKeyPath, serverName string) (*tls.Config, error) {
	pool, err := LoadCAPool(caCertPath)
	if err != nil {
		return nil, err
	}
	cert, err := tls.LoadX509KeyPair(clientCertPath, clientKeyPath)
	if err != nil {
		return nil, common.Wrap(err, common.ErrTLSHandshake, "load client keypair")
	}
	return &tls.Config{
		MinVersion:   tls.VersionTLS13,
		RootCAs:      pool,
		Certificates: []tls.Certificate{cert},
		ServerName:   serverName,
	}, nil
}
