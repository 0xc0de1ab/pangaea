package server

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"
	"time"

	"github.com/0xc0de1ab/pangaea/internal/common"
	"github.com/0xc0de1ab/pangaea/internal/config"
	"github.com/0xc0de1ab/pangaea/internal/logging"
	"github.com/0xc0de1ab/pangaea/internal/transport"
)

// Session owns a single WS connection's lifecycle on the server side. It is
// created after a successful mTLS handshake + hello exchange, and its readLoop
// drives all subsequent message handling.
type Session struct {
	conn     transport.Conn
	profile  string
	peerCN   string
	identity string
	authMode config.AuthMode
	nodeID   string

	log *slog.Logger

	hub *profileHub

	closeOnce sync.Once
}

// newSession wires a Session around an upgraded transport.Conn. The nodeID is
// taken from the client's Hello.
func newSession(conn transport.Conn, profile, peerCN, identity, nodeID string, authMode config.AuthMode, log *slog.Logger) *Session {
	return &Session{
		conn:     conn,
		profile:  profile,
		peerCN:   peerCN,
		identity: identity,
		authMode: authMode,
		nodeID:   nodeID,
		log: log.With(
			slog.String(logging.FieldComponent, logging.ComponentServer),
			slog.String(logging.FieldProfile, profile),
			slog.String(logging.FieldPeerCN, peerCN),
			slog.String(logging.FieldIdentity, identity),
			slog.String(logging.FieldAuthMode, string(authMode)),
			slog.String(logging.FieldNodeID, nodeID),
		),
	}
}

// sendWelcome writes the welcome envelope after hello is accepted.
func (s *Session) sendWelcome(ctx context.Context, serverVersion string, known *transport.TruthMeta) error {
	w := transport.Welcome{ServerVersion: serverVersion, KnownTruth: known}
	data, err := transport.Marshal(transport.KindWelcome, common.EnvelopeV, transport.NewID(), time.Now(), w)
	if err != nil {
		return err
	}
	var env transport.Envelope
	if err := json.Unmarshal(data, &env); err != nil {
		return err
	}
	return s.conn.Send(ctx, env)
}

// readLoop consumes envelopes from s.conn and routes them to the mediator.
// Terminates when the connection closes; the caller is responsible for
// calling Hub.Unregister afterwards.
func (s *Session) readLoop(ctx context.Context) {
	defer s.log.Info(logging.EvtDisconnected,
		slog.String(logging.FieldEvent, logging.EvtDisconnected),
	)
	for {
		select {
		case <-ctx.Done():
			return
		case env, ok := <-s.conn.Recv():
			if !ok {
				return
			}
			s.handle(ctx, env)
		}
	}
}

func (s *Session) handle(ctx context.Context, env transport.Envelope) {
	switch env.Type {
	case transport.KindSnapshotReport:
		var p transport.SnapshotReport
		if err := json.Unmarshal(env.Payload, &p); err != nil {
			s.protocolError(ctx, "invalid snapshot.report payload")
			return
		}
		if p.Profile != s.profile {
			s.protocolError(ctx, "profile mismatch in snapshot.report")
			return
		}
		if s.hub != nil {
			s.hub.mediator.Report(mediatorReport{session: s, payload: p})
		}
	case transport.KindSnapshotAbsent:
		var p transport.SnapshotAbsent
		if err := json.Unmarshal(env.Payload, &p); err != nil {
			s.protocolError(ctx, "invalid snapshot.absent payload")
			return
		}
		if p.Profile != s.profile {
			s.protocolError(ctx, "profile mismatch in snapshot.absent")
			return
		}
		if s.hub != nil {
			s.hub.mediator.Absent(mediatorAbsent{session: s, payload: p})
		}
	case transport.KindTruthAck:
		var p transport.TruthAck
		if err := json.Unmarshal(env.Payload, &p); err != nil {
			s.protocolError(ctx, "invalid truth.ack payload")
			return
		}
		if s.hub != nil {
			s.hub.mediator.Ack(mediatorAck{session: s, payload: p})
		}
	case transport.KindAuthJWT, transport.KindHello, transport.KindWelcome, transport.KindSnapshotRequest, transport.KindTruthPush:
		// Unexpected direction — server never receives these after handshake.
		s.protocolError(ctx, "unexpected message for server direction")
	case transport.KindError:
		// Peer reporting an error; just log and let the next message (or
		// disconnection) drive state.
		var p transport.ErrorPayload
		_ = json.Unmarshal(env.Payload, &p)
		s.log.Warn("peer reported error",
			slog.String(logging.FieldEvent, logging.EvtSessionRejected),
			slog.String(logging.FieldReason, p.Message),
		)
	default:
		s.protocolError(ctx, "unknown message kind")
	}
}

func (s *Session) protocolError(ctx context.Context, msg string) {
	s.log.Warn("protocol error",
		slog.String(logging.FieldEvent, logging.EvtSessionRejected),
		slog.String(logging.FieldReason, msg),
	)
	_ = s.conn.Close(1002, msg)
}

// sendTruthPush delivers an envelope to this session. Used by the mediator.
func (s *Session) sendTruthPush(ctx context.Context, push transport.TruthPush) error {
	data, err := transport.Marshal(transport.KindTruthPush, common.EnvelopeV, transport.NewID(), time.Now(), push)
	if err != nil {
		return err
	}
	var env transport.Envelope
	if err := json.Unmarshal(data, &env); err != nil {
		return err
	}
	return s.conn.Send(ctx, env)
}

func (s *Session) sendSnapshotRequest(ctx context.Context, req transport.SnapshotRequest) error {
	data, err := transport.Marshal(transport.KindSnapshotRequest, common.EnvelopeV, transport.NewID(), time.Now(), req)
	if err != nil {
		return err
	}
	var env transport.Envelope
	if err := json.Unmarshal(data, &env); err != nil {
		return err
	}
	return s.conn.Send(ctx, env)
}
