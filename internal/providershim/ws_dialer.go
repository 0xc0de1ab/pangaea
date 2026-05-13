package providershim

import (
	"crypto/tls"

	"github.com/gorilla/websocket"
)

// routerWebSocketDialer returns a dialer suitable for router control/data WSS URLs.
// Forcing ALPN to http/1.1 avoids WebSocket upgrade failures when the TLS stack
// negotiates HTTP/2 first (observed with some Docker bridge/NAT paths against ingress).
func routerWebSocketDialer() *websocket.Dialer {
	d := websocket.DefaultDialer
	return &websocket.Dialer{
		Proxy:             d.Proxy,
		HandshakeTimeout:  d.HandshakeTimeout,
		ReadBufferSize:    d.ReadBufferSize,
		WriteBufferSize:   d.WriteBufferSize,
		EnableCompression: d.EnableCompression,
		Jar:               d.Jar,
		Subprotocols:      d.Subprotocols,
		TLSClientConfig: &tls.Config{
			MinVersion: tls.VersionTLS12,
			NextProtos: []string{"http/1.1"},
		},
	}
}
