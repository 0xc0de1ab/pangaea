package server

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/0xc0de1ab/pangaea/internal/common"
	"github.com/0xc0de1ab/pangaea/internal/config"
	"github.com/0xc0de1ab/pangaea/internal/logging"
	"github.com/0xc0de1ab/pangaea/internal/transport"
	"github.com/gin-gonic/gin"
)

// newRouter builds a gin engine with a single /ws/profile/:name route. gin's
// recovery and logging middleware are disabled — we use slog for structured
// logging; gin's default logger would bypass our redactor.
func newRouter(h *Hub, ps config.ProfileStore, serverVersion string, auth *authenticator, log *slog.Logger) *gin.Engine {
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery())
	r.GET("/ws/profile/:name", handleWS(h, ps, serverVersion, auth, log))
	r.GET("/healthz", func(c *gin.Context) { c.String(http.StatusOK, "ok") })
	return r
}

// handleWS performs: profile existence check, mTLS CN extraction, upgrade,
// hello/welcome exchange, session registration, and read-loop spawn. On any
// pre-upgrade error we return an HTTP status so the client can log it; after
// upgrade, errors go over the WS close frame.
func handleWS(h *Hub, ps config.ProfileStore, serverVersion string, auth *authenticator, log *slog.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		name := c.Param("name")
		if _, ok := ps.Get(name); !ok {
			c.String(http.StatusNotFound, "unknown profile: %s", name)
			return
		}

		hs, status, err := auth.preUpgrade(c.Request, name)
		if err != nil {
			c.String(status, err.Error())
			return
		}

		conn, err := transport.Upgrade(c.Writer, c.Request, hs.peerCN)
		if err != nil {
			log.Warn("ws upgrade failed",
				slog.String(logging.FieldComponent, logging.ComponentServer),
				slog.String(logging.FieldReason, err.Error()),
				slog.String(logging.FieldRemoteAddr, c.ClientIP()),
			)
			return
		}
		serveConn(c.Request.Context(), h, name, serverVersion, auth, hs, conn, c.ClientIP(), log)
	}
}

func serveConn(
	ctx context.Context,
	h *Hub,
	profileName string,
	serverVersion string,
	auth *authenticator,
	hs handshakeAuth,
	conn transport.Conn,
	remoteAddr string,
	log *slog.Logger,
) {
	handshakeTO := common.ReadTimeout
	if auth != nil && auth.authTimeout > handshakeTO {
		handshakeTO = auth.authTimeout
	}
	hctx, cancel := context.WithTimeout(ctx, handshakeTO)
	defer cancel()

	var err error
	if hs.needsJWT {
		if auth == nil {
			_ = conn.Close(1008, common.MsgAuthJWTRequired)
			return
		}
		authCtx, authCancel := context.WithTimeout(hctx, auth.authTimeout)
		defer authCancel()
		hs, err = auth.completeJWT(authCtx, conn, profileName)
		if err != nil {
			_ = conn.Close(1008, err.Error())
			return
		}
	}

	env, ok := readKind(hctx, conn, transport.KindHello)
	if !ok {
		_ = conn.Close(1002, "hello required")
		return
	}
	var hello transport.Hello
	if err := json.Unmarshal(env.Payload, &hello); err != nil || hello.NodeID == "" {
		_ = conn.Close(1002, "hello payload invalid")
		return
	}
	if hs.identity != "" && hello.NodeID != hs.identity {
		_ = conn.Close(1008, common.MsgHelloIdentityMismatch)
		return
	}

	session := newSession(conn, profileName, hs.peerCN, hs.identity, hello.NodeID, hs.mode, log)
	if err := h.Register(ctx, profileName, hs, session); err != nil {
		log.Warn("register rejected",
			slog.String(logging.FieldComponent, logging.ComponentServer),
			slog.String(logging.FieldProfile, profileName),
			slog.String(logging.FieldPeerCN, hs.peerCN),
			slog.String(logging.FieldIdentity, hs.identity),
			slog.String(logging.FieldAuthMode, string(hs.mode)),
			slog.String(logging.FieldNodeID, hello.NodeID),
			slog.String(logging.FieldReason, err.Error()),
		)
		_ = conn.Close(1008, err.Error())
		return
	}

	var known *transport.TruthMeta
	if session.hub != nil {
		known = session.hub.mediator.currentTruthMeta(ctx)
	}
	if err := session.sendWelcome(hctx, serverVersion, known); err != nil {
		log.Warn("welcome send failed",
			slog.String(logging.FieldComponent, logging.ComponentServer),
			slog.String(logging.FieldReason, err.Error()),
		)
		h.Unregister(session)
		_ = conn.Close(1011, "welcome failed")
		return
	}

	log.Info("session connected",
		slog.String(logging.FieldComponent, logging.ComponentServer),
		slog.String(logging.FieldEvent, logging.EvtConnected),
		slog.String(logging.FieldProfile, profileName),
		slog.String(logging.FieldPeerCN, hs.peerCN),
		slog.String(logging.FieldIdentity, hs.identity),
		slog.String(logging.FieldAuthMode, string(hs.mode)),
		slog.String(logging.FieldNodeID, hello.NodeID),
		slog.String(logging.FieldRemoteAddr, remoteAddr),
	)
	session.readLoop(ctx)
	h.Unregister(session)
}

func readKind(ctx context.Context, conn transport.Conn, want transport.Kind) (transport.Envelope, bool) {
	select {
	case env, ok := <-conn.Recv():
		if !ok {
			return transport.Envelope{}, false
		}
		if env.Type != want {
			return transport.Envelope{}, false
		}
		return env, true
	case <-ctx.Done():
		return transport.Envelope{}, false
	case <-time.After(common.ReadTimeout):
		return transport.Envelope{}, false
	}
}
