package transport

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/0xc0de1ab/pangaea/internal/common"
	"github.com/gorilla/websocket"
)

// Dial establishes a WebSocket client connection. The supplied tls.Config
// MUST be configured for mTLS — it is the only authenticator. The handshake
// honours ctx (via DialContext on the websocket.Dialer).
func Dial(ctx context.Context, url string, tlsCfg *tls.Config, headers http.Header) (Conn, error) {
	d := &websocket.Dialer{
		TLSClientConfig:  tlsCfg,
		HandshakeTimeout: common.WriteTimeout,
	}
	ws, _, err := d.DialContext(ctx, url, headers)
	if err != nil {
		if isTLSHandshakeError(err) {
			return nil, common.Wrap(err, common.ErrTLSHandshake, "%s", url)
		}
		return nil, fmt.Errorf("dial %s: %w", url, err)
	}
	opt := defaultConnOptions()
	// Client side has no peer CN to surface; the server proves itself via SAN.
	return newConn(ws, opt), nil
}

func isTLSHandshakeError(err error) bool {
	var unknownAuthorityErr x509.UnknownAuthorityError
	if errors.As(err, &unknownAuthorityErr) {
		return true
	}
	var hostnameErr x509.HostnameError
	if errors.As(err, &hostnameErr) {
		return true
	}
	var certInvalidErr x509.CertificateInvalidError
	if errors.As(err, &certInvalidErr) {
		return true
	}
	var systemRootsErr x509.SystemRootsError
	if errors.As(err, &systemRootsErr) {
		return true
	}
	var recordHeaderErr tls.RecordHeaderError
	if errors.As(err, &recordHeaderErr) {
		return true
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "tls:") || strings.Contains(msg, "x509:")
}
