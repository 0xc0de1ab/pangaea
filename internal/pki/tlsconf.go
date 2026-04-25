package pki

import (
	"crypto/tls"

	"github.com/0xc0de1ab/pangaea/internal/common"
)

// ServerTLSConfig builds the *tls.Config for the TLS server endpoint.
// When requireClientCert is true, peer certificates are mandatory and
// verified against caCertPath. Otherwise client certs are optional.
func ServerTLSConfig(caCertPath, serverCertPath, serverKeyPath string, requireClientCert bool) (*tls.Config, error) {
	cert, err := tls.LoadX509KeyPair(serverCertPath, serverKeyPath)
	if err != nil {
		return nil, common.Wrap(err, common.ErrTLSHandshake, "load server keypair")
	}
	cfg := &tls.Config{
		MinVersion:   tls.VersionTLS13,
		Certificates: []tls.Certificate{cert},
	}
	switch {
	case requireClientCert:
		pool, err := LoadCAPool(caCertPath)
		if err != nil {
			return nil, err
		}
		cfg.ClientCAs = pool
		cfg.ClientAuth = tls.RequireAndVerifyClientCert
	case caCertPath != "":
		pool, err := LoadCAPool(caCertPath)
		if err != nil {
			return nil, err
		}
		cfg.ClientCAs = pool
		cfg.ClientAuth = tls.VerifyClientCertIfGiven
	default:
		cfg.ClientAuth = tls.NoClientCert
	}
	return cfg, nil
}

// ClientTLSConfig builds the *tls.Config used by the client to dial the
// server. ServerName must match a SAN on the server cert. If clientCertPath
// and clientKeyPath are empty, the client authenticates only at the
// application layer (e.g. JWT) while still verifying the server certificate.
func ClientTLSConfig(caCertPath, clientCertPath, clientKeyPath, serverName string) (*tls.Config, error) {
	pool, err := LoadCAPool(caCertPath)
	if err != nil {
		return nil, err
	}
	cfg := &tls.Config{
		MinVersion: tls.VersionTLS13,
		RootCAs:    pool,
		ServerName: serverName,
	}
	if clientCertPath == "" && clientKeyPath == "" {
		return cfg, nil
	}
	if clientCertPath == "" || clientKeyPath == "" {
		return nil, common.Wrap(nil, common.ErrTLSHandshake, "client certificate and key must be provided together")
	}
	cert, err := tls.LoadX509KeyPair(clientCertPath, clientKeyPath)
	if err != nil {
		return nil, common.Wrap(err, common.ErrTLSHandshake, "load client keypair")
	}
	cfg.Certificates = []tls.Certificate{cert}
	return cfg, nil
}
