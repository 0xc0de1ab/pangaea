package transport

import (
	"crypto/x509"
	"errors"
	"net"
	"syscall"
	"testing"
)

func TestIsTLSHandshakeError(t *testing.T) {
	if !isTLSHandshakeError(x509.HostnameError{}) {
		t.Fatalf("expected hostname error to count as TLS handshake failure")
	}
	connRefused := &net.OpError{Err: syscall.ECONNREFUSED}
	if isTLSHandshakeError(connRefused) {
		t.Fatalf("connection refused must not count as TLS handshake failure")
	}
	if !isTLSHandshakeError(errors.New("remote error: tls: bad certificate")) {
		t.Fatalf("tls-prefixed error must count as TLS handshake failure")
	}
}
