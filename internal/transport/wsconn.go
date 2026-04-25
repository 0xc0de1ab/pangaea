package transport

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"sync"
	"sync/atomic"
	"time"

	"github.com/dh-kam/claude-creds-share/internal/common"
	"github.com/gorilla/websocket"
)

// jsonMarshal produces wire bytes from an Envelope. Callers in this package
// should hold onto encodeEnvelope which also validates kind/version.
func jsonMarshal(env Envelope) ([]byte, error) {
	return json.Marshal(env)
}

// Conn is the abstract handle on one WebSocket session. Implementations are
// safe for concurrent Send from multiple goroutines (writes are serialized
// through a single internal writer). Recv() returns a channel that closes
// when the connection terminates.
type Conn interface {
	// Send marshals env over the wire. It is thread-safe — writes are
	// serialized internally with a single writer goroutine and a per-call
	// deadline of common.WriteTimeout. Returns ErrConnClosed if the
	// connection has terminated, or the underlying error from JSON encoding
	// or the websocket library. The provided context can abort the
	// hand-off to the writer (it does not abort an in-flight write).
	Send(ctx context.Context, env Envelope) error

	// Recv yields decoded envelopes. The channel is buffered to common.ChannelBuf.
	// On overflow the OLDEST queued envelope is dropped (and a stderr line
	// emitted). The channel closes once the reader goroutine exits.
	Recv() <-chan Envelope

	// Close sends a websocket close frame with the given code and reason
	// and tears down the read/write goroutines. Idempotent.
	Close(code int, reason string) error

	// Err returns the last non-close error observed by either the reader
	// or writer goroutine. nil after a clean close.
	Err() error

	// RemoteCN returns the peer certificate Common Name on server-side
	// connections (set at Upgrade time), or "" for client-side conns.
	RemoteCN() string
}

// connOptions captures the small set of knobs that tests want to override.
// Production callers go through Upgrade/Dial which fill in defaults.
type connOptions struct {
	recvBuf      int
	pingInterval time.Duration
	writeTimeout time.Duration
	readTimeout  time.Duration
	remoteCN     string
}

func defaultConnOptions() connOptions {
	return connOptions{
		recvBuf:      common.ChannelBuf,
		pingInterval: common.PingInterval,
		writeTimeout: common.WriteTimeout,
		readTimeout:  common.ReadTimeout,
	}
}

// wsConn is the production Conn backed by gorilla/websocket. It owns one
// reader goroutine, one writer goroutine, and a ping ticker that runs inside
// the writer.
type wsConn struct {
	ws  *websocket.Conn
	opt connOptions

	sendCh chan sendReq
	recvCh chan Envelope

	closeOnce sync.Once
	doneCh    chan struct{}

	errMu   sync.Mutex
	lastErr error

	closing atomic.Bool
}

// sendReq is the unit of work passed to the writer goroutine.
type sendReq struct {
	data []byte
	done chan error
}

// newConn wires up the goroutines around an already-handshaked websocket.Conn.
func newConn(ws *websocket.Conn, opt connOptions) *wsConn {
	if opt.recvBuf <= 0 {
		opt.recvBuf = common.ChannelBuf
	}
	if opt.pingInterval <= 0 {
		opt.pingInterval = common.PingInterval
	}
	if opt.writeTimeout <= 0 {
		opt.writeTimeout = common.WriteTimeout
	}
	if opt.readTimeout <= 0 {
		opt.readTimeout = common.ReadTimeout
	}
	c := &wsConn{
		ws:     ws,
		opt:    opt,
		sendCh: make(chan sendReq, opt.recvBuf),
		recvCh: make(chan Envelope, opt.recvBuf),
		doneCh: make(chan struct{}),
	}
	// Read deadline is refreshed by the pong handler; install it before
	// goroutines start.
	_ = ws.SetReadDeadline(time.Now().Add(opt.readTimeout))
	ws.SetPongHandler(func(string) error {
		_ = ws.SetReadDeadline(time.Now().Add(c.opt.readTimeout))
		return nil
	})
	go c.readLoop()
	go c.writeLoop()
	return c
}

func (c *wsConn) recordErr(err error) {
	if err == nil {
		return
	}
	// Close-frame errors from a clean shutdown are not interesting.
	if websocket.IsCloseError(err,
		websocket.CloseNormalClosure,
		websocket.CloseGoingAway,
	) {
		return
	}
	c.errMu.Lock()
	if c.lastErr == nil {
		c.lastErr = err
	}
	c.errMu.Unlock()
}

func (c *wsConn) Err() error {
	c.errMu.Lock()
	defer c.errMu.Unlock()
	return c.lastErr
}

func (c *wsConn) RemoteCN() string { return c.opt.remoteCN }

func (c *wsConn) Recv() <-chan Envelope { return c.recvCh }

func (c *wsConn) Send(ctx context.Context, env Envelope) error {
	if c.closing.Load() {
		return ErrConnClosed
	}
	data, err := encodeEnvelope(env)
	if err != nil {
		return err
	}
	req := sendReq{data: data, done: make(chan error, 1)}
	select {
	case c.sendCh <- req:
	case <-c.doneCh:
		return ErrConnClosed
	case <-ctx.Done():
		return ctx.Err()
	}
	select {
	case err := <-req.done:
		return err
	case <-c.doneCh:
		return ErrConnClosed
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (c *wsConn) Close(code int, reason string) error {
	var err error
	c.closeOnce.Do(func() {
		c.closing.Store(true)
		// Best-effort close frame, then break the underlying connection so
		// goroutines unwind. We ignore write errors here — the goal is to
		// signal teardown.
		deadline := time.Now().Add(c.opt.writeTimeout)
		_ = c.ws.WriteControl(
			websocket.CloseMessage,
			websocket.FormatCloseMessage(code, reason),
			deadline,
		)
		err = c.ws.Close()
		close(c.doneCh)
	})
	return err
}

// readLoop pulls frames off the wire, decodes them, and pushes onto recvCh.
// On overflow it drops the OLDEST queued envelope (specs §E.6 corner case).
// On any read error it triggers Close.
func (c *wsConn) readLoop() {
	defer close(c.recvCh)
	for {
		_, msg, err := c.ws.ReadMessage()
		if err != nil {
			c.recordErr(err)
			c.tearDown()
			return
		}
		env, derr := Unmarshal(msg)
		if derr != nil {
			c.recordErr(derr)
			// Bad frame from peer is unrecoverable at this layer.
			c.tearDown()
			return
		}
		c.deliver(env)
	}
}

func (c *wsConn) deliver(env Envelope) {
	for {
		select {
		case c.recvCh <- env:
			return
		default:
			// Overflow: drop oldest.
			select {
			case <-c.recvCh:
				log.Printf("transport.recv.overflow: dropped oldest envelope (cap=%d)", cap(c.recvCh))
			default:
				// Channel was drained between the two selects; retry insert.
			}
		}
	}
}

// writeLoop serializes Send and emits ping frames on the configured tick.
// Writes carry common.WriteTimeout deadlines so a wedged peer cannot hang
// other senders.
func (c *wsConn) writeLoop() {
	ping := time.NewTicker(c.opt.pingInterval)
	defer ping.Stop()
	for {
		select {
		case req, ok := <-c.sendCh:
			if !ok {
				return
			}
			_ = c.ws.SetWriteDeadline(time.Now().Add(c.opt.writeTimeout))
			err := c.ws.WriteMessage(websocket.TextMessage, req.data)
			if err != nil {
				c.recordErr(err)
				if errors.Is(err, websocket.ErrCloseSent) {
					req.done <- ErrConnClosed
				} else if isTimeoutErr(err) {
					req.done <- ErrWriteTimeout
				} else {
					req.done <- err
				}
				c.tearDown()
				return
			}
			req.done <- nil
		case <-ping.C:
			_ = c.ws.SetWriteDeadline(time.Now().Add(c.opt.writeTimeout))
			if err := c.ws.WriteMessage(websocket.PingMessage, nil); err != nil {
				c.recordErr(err)
				c.tearDown()
				return
			}
		case <-c.doneCh:
			return
		}
	}
}

// tearDown closes the websocket and signals goroutines to exit. Safe to call
// from either pump.
func (c *wsConn) tearDown() {
	c.closeOnce.Do(func() {
		c.closing.Store(true)
		_ = c.ws.Close()
		close(c.doneCh)
	})
}

// encodeEnvelope produces wire bytes for an already-built Envelope. We do
// not re-validate Kind/V here — Marshal() is the public, validating entry
// point. This path is taken only by Conn.Send where the envelope was
// constructed in-process.
func encodeEnvelope(env Envelope) ([]byte, error) {
	if !validKind(env.Type) {
		return nil, common.Wrap(nil, common.ErrInvalidMessage, "invalid kind on send")
	}
	if env.V != common.EnvelopeV {
		return nil, common.Wrap(nil, common.ErrInvalidMessage, "invalid envelope version on send")
	}
	return jsonMarshal(env)
}

// isTimeoutErr returns true for net.Error{Timeout()=true}.
func isTimeoutErr(err error) bool {
	type tm interface{ Timeout() bool }
	if te, ok := err.(tm); ok {
		return te.Timeout()
	}
	return false
}
