package client

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"time"

	"github.com/0xc0de1ab/pangaea/internal/common"
	"github.com/0xc0de1ab/pangaea/internal/transport"
)

// sendEnvelope marshals payload under kind and forwards it through conn.
// Caller-supplied ctx bounds the write; a per-call deadline still kicks in
// inside the transport layer.
func sendEnvelope(ctx context.Context, conn transport.Conn, kind transport.Kind, payload any) error {
	data, err := transport.Marshal(kind, common.EnvelopeV, transport.NewID(), time.Now(), payload)
	if err != nil {
		return err
	}
	var env transport.Envelope
	if err := json.Unmarshal(data, &env); err != nil {
		return err
	}
	writeCtx, cancel := context.WithTimeout(ctx, common.WriteTimeout)
	defer cancel()
	return conn.Send(writeCtx, env)
}

// awaitKind reads envelopes from conn until one of the expected kind arrives
// or ctx expires. Other envelope kinds are discarded (they would normally not
// happen during handshake, but being permissive here avoids a wedged session
// if a stale message is in flight).
func awaitKind(ctx context.Context, conn transport.Conn, want transport.Kind) (transport.Envelope, error) {
	for {
		select {
		case <-ctx.Done():
			return transport.Envelope{}, ctx.Err()
		case env, ok := <-conn.Recv():
			if !ok {
				if err := conn.Err(); err != nil {
					return transport.Envelope{}, err
				}
				return transport.Envelope{}, common.Wrap(nil, common.ErrInvalidMessage, "connection closed before %q", want)
			}
			if env.Type == want {
				return env, nil
			}
			// Otherwise silently skip — caller did not expect this kind yet.
		}
	}
}

// readFileIfExists returns (data, nil) on success, (nil, nil) on ENOENT, or
// (nil, err) on other errors.
func readFileIfExists(path string) ([]byte, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	return b, nil
}

// extractHost returns the hostname portion of a wss:// URL, or the input if
// parsing fails. Used to set ServerName on the TLS config when callers did
// not supply an override.
func extractHost(server string) string {
	u, err := url.Parse(server)
	if err != nil || u.Host == "" {
		return server
	}
	// net/url's Hostname() strips the :port bit.
	return u.Hostname()
}
