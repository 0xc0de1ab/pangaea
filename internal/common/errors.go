package common

import (
	"errors"
	"fmt"
)

// Sentinel errors. Callers identify categories with errors.Is.
var (
	ErrProfileNotFound      = errors.New("profile not found")
	ErrFormatNotRegistered  = errors.New("format not registered")
	ErrInvalidMessage       = errors.New("invalid message")
	ErrTLSHandshake         = errors.New("tls handshake failed")
	ErrParseFailed          = errors.New("parse failed")
	ErrExpired              = errors.New("credential expired")
	ErrLiveCheckUnreachable = errors.New("live check unreachable")
	ErrApplyFailed          = errors.New("apply failed")
	ErrLockTimeout          = errors.New("lock acquisition timed out")
	ErrConfigInvalid        = errors.New("config invalid")
	ErrCNMismatch           = errors.New("client common name not allowed")
	ErrSessionDisplaced     = errors.New("session displaced by newer connection")
	ErrShutdown             = errors.New("shutdown in progress")
)

// wrapped links a free-form message to an underlying error *and* a category
// sentinel, so errors.Is matches against both.
type wrapped struct {
	sentinel error
	inner    error
	msg      string
}

func (w *wrapped) Error() string {
	if w.inner != nil {
		return fmt.Sprintf("%s: %s: %s", w.sentinel.Error(), w.msg, w.inner.Error())
	}
	return fmt.Sprintf("%s: %s", w.sentinel.Error(), w.msg)
}

func (w *wrapped) Unwrap() []error {
	if w.inner == nil {
		return []error{w.sentinel}
	}
	return []error{w.sentinel, w.inner}
}

// Wrap produces an error that errors.Is matches against the given sentinel and
// (if non-nil) the underlying err. format/args are mandatory context.
func Wrap(err, sentinel error, format string, args ...any) error {
	if sentinel == nil {
		sentinel = errors.New("unspecified")
	}
	return &wrapped{
		sentinel: sentinel,
		inner:    err,
		msg:      fmt.Sprintf(format, args...),
	}
}
