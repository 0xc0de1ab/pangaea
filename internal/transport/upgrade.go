package transport

import (
	"net/http"

	"github.com/gorilla/websocket"
)

// upgrader is package-scoped to reuse buffers across upgrades. CheckOrigin is
// always-true: this handler is unconditionally protected by mTLS at the TLS
// layer, so origin headers are not the appropriate access control.
var upgrader = websocket.Upgrader{
	ReadBufferSize:  4096,
	WriteBufferSize: 4096,
	CheckOrigin:     func(r *http.Request) bool { return true },
}

// Upgrade promotes an incoming HTTP request to a Conn. The caller is
// responsible for ensuring TLS has already verified the client certificate
// (this is the contract behind specs §4: mTLS is the gate). remoteCN is the
// peer certificate's CN extracted by the caller; it is exposed via Conn.RemoteCN().
func Upgrade(w http.ResponseWriter, r *http.Request, remoteCN string) (Conn, error) {
	ws, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return nil, err
	}
	opt := defaultConnOptions()
	opt.remoteCN = remoteCN
	return newConn(ws, opt), nil
}
