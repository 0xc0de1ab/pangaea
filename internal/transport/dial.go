package transport

import (
	"context"
	"crypto/tls"
	"net/http"

	"github.com/dh-kam/claude-creds-share/internal/common"
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
		return nil, common.Wrap(err, common.ErrTLSHandshake, "%s", url)
	}
	opt := defaultConnOptions()
	// Client side has no peer CN to surface; the server proves itself via SAN.
	return newConn(ws, opt), nil
}
