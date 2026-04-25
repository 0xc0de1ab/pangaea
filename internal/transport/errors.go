package transport

import (
	"errors"

	"github.com/0xc0de1ab/pangaea/internal/common"
)

// Sentinel errors for transport-internal flow control. They wrap
// common.ErrInvalidMessage so callers can match either the broad protocol
// category or the specific cause via errors.Is.
var (
	// ErrConnClosed is returned by Send when the underlying writer goroutine
	// has stopped (graceful or otherwise). Use Conn.Err() to inspect cause.
	ErrConnClosed = errors.New("transport: connection closed")

	// ErrWriteTimeout indicates a single Send exceeded common.WriteTimeout.
	ErrWriteTimeout = errors.New("transport: write timeout")
)

// wrapInvalid wraps an error with common.ErrInvalidMessage and a contextual
// message. Used by codec helpers exposed to the conn layer.
func wrapInvalid(err error, msg string) error { //nolint:unused // exported via tests
	return common.Wrap(err, common.ErrInvalidMessage, "%s", msg)
}
