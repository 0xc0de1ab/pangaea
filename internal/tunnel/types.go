// Package tunnel contains reverse data-plane primitives that can be exercised
// before any network tunnel transport exists.
package tunnel

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

type StreamID string

type ProviderInstanceID string

type State string

const (
	StateIdle   State = "idle"
	StateActive State = "active"
	StateClosed State = "closed"
)

var (
	ErrInvalidTokenClaims = errors.New("tunnel: invalid token claims")
	ErrTokenExpired       = errors.New("tunnel: token expired")
	ErrProviderMismatch   = errors.New("tunnel: provider mismatch")
	ErrStreamMismatch     = errors.New("tunnel: stream mismatch")
	ErrModelMismatch      = errors.New("tunnel: model mismatch")
	ErrStreamClosed       = errors.New("tunnel: stream closed")
)

type StreamDescriptor struct {
	StreamID           StreamID           `json:"stream_id"`
	ProviderInstanceID ProviderInstanceID `json:"provider_instance_id"`
	Model              string             `json:"model"`
	State              State              `json:"state"`
	CreatedAt          time.Time          `json:"created_at"`
	UpdatedAt          time.Time          `json:"updated_at"`
}

func (d StreamDescriptor) Validate() error {
	switch {
	case blank(string(d.StreamID)):
		return fmt.Errorf("%w: stream_id is required", ErrStreamMismatch)
	case blank(string(d.ProviderInstanceID)):
		return fmt.Errorf("%w: provider_instance_id is required", ErrProviderMismatch)
	case blank(d.Model):
		return fmt.Errorf("%w: model is required", ErrModelMismatch)
	case !d.State.Valid():
		return fmt.Errorf("%w: state %q is invalid", ErrStreamMismatch, d.State)
	}
	return nil
}

func (d StreamDescriptor) Matches(provider ProviderInstanceID, model string) bool {
	return d.ProviderInstanceID == provider && d.Model == model
}

func (d StreamDescriptor) ValidateClaims(claims StreamTokenClaims, now time.Time) error {
	return claims.ValidateForDescriptor(d, now)
}

func (s State) Valid() bool {
	switch s {
	case StateIdle, StateActive, StateClosed:
		return true
	}
	return false
}

type StreamTokenClaims struct {
	RequestID          string             `json:"request_id"`
	StreamID           StreamID           `json:"stream_id"`
	ProviderInstanceID ProviderInstanceID `json:"provider_instance_id"`
	Model              string             `json:"model"`
	Deadline           time.Time          `json:"deadline"`
}

type TokenClaims = StreamTokenClaims

func (c StreamTokenClaims) Validate(now time.Time) error {
	switch {
	case blank(c.RequestID):
		return fmt.Errorf("%w: request_id is required", ErrInvalidTokenClaims)
	case blank(string(c.StreamID)):
		return fmt.Errorf("%w: stream_id is required", ErrInvalidTokenClaims)
	case blank(string(c.ProviderInstanceID)):
		return fmt.Errorf("%w: provider_instance_id is required", ErrInvalidTokenClaims)
	case blank(c.Model):
		return fmt.Errorf("%w: model is required", ErrInvalidTokenClaims)
	case c.Deadline.IsZero():
		return fmt.Errorf("%w: deadline is required", ErrInvalidTokenClaims)
	case !now.Before(c.Deadline):
		return fmt.Errorf("%w: deadline %s is not after %s", ErrTokenExpired, c.Deadline.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano))
	}
	return nil
}

func (c StreamTokenClaims) ValidateForDescriptor(desc StreamDescriptor, now time.Time) error {
	if err := c.Validate(now); err != nil {
		return err
	}
	if desc.State == StateClosed {
		return fmt.Errorf("%w: stream %q is closed", ErrStreamClosed, desc.StreamID)
	}
	if c.StreamID != desc.StreamID {
		return fmt.Errorf("%w: claim stream %q does not match descriptor stream %q", ErrStreamMismatch, c.StreamID, desc.StreamID)
	}
	if c.ProviderInstanceID != desc.ProviderInstanceID {
		return fmt.Errorf("%w: claim provider %q does not match descriptor provider %q", ErrProviderMismatch, c.ProviderInstanceID, desc.ProviderInstanceID)
	}
	if c.Model != desc.Model {
		return fmt.Errorf("%w: claim model %q does not match descriptor model %q", ErrModelMismatch, c.Model, desc.Model)
	}
	return nil
}

func (c StreamTokenClaims) MatchesDescriptor(desc StreamDescriptor, now time.Time) bool {
	return c.ValidateForDescriptor(desc, now) == nil
}

func blank(s string) bool {
	return strings.TrimSpace(s) == ""
}
