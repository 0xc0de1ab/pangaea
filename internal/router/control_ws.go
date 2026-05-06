package router

import (
	"errors"
	"net/http"
	"time"

	"github.com/0xc0de1ab/pangaea/internal/control"
	"github.com/0xc0de1ab/pangaea/internal/provider"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

var controlUpgrader = websocket.Upgrader{
	CheckOrigin: func(*http.Request) bool { return true },
}

func handleControlWS(engine *Engine, peerToken string) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !authenticateRouterPeerRequest(c, peerToken) {
			return
		}
		if engine == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": ErrRouterNotReady.Error()})
			return
		}
		conn, err := controlUpgrader.Upgrade(c.Writer, c.Request, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		session := newControlSession(conn)
		defer engine.removeControlSession(session)

		for {
			_, data, err := conn.ReadMessage()
			if err != nil {
				return
			}
			env, err := control.Unmarshal(data)
			if err != nil {
				_ = writeControlError(session, "invalid_envelope", err)
				continue
			}
			if err := applyControlEnvelope(engine, env); err != nil {
				_ = writeControlError(session, "apply_failed", err)
				continue
			}
			bindControlSessionForEnvelope(engine, session, env)
			_ = writeControlAck(session, env.ID)
		}
	}
}

func applyControlEnvelope(engine *Engine, env control.Envelope) error {
	switch env.Type {
	case control.MessageTypeProviderRegister:
		registration, err := control.Decode[control.ProviderRegisterPayload](env, control.MessageTypeProviderRegister)
		if err != nil {
			return err
		}
		return engine.UpsertProvider(registration)
	case control.MessageTypeProviderHeartbeat:
		heartbeat, err := control.Decode[control.ProviderHeartbeat](env, control.MessageTypeProviderHeartbeat)
		if err != nil {
			return err
		}
		if heartbeat.ProviderInstanceID == "" {
			return control.ErrInvalidPayload
		}
		return engine.UpdateProviderHeartbeat(heartbeat.ProviderInstanceID, heartbeat.Health, heartbeat.Auth, heartbeat.Limits)
	case control.MessageTypeProviderInventoryReport:
		report, err := control.Decode[control.ProviderInventoryReport](env, control.MessageTypeProviderInventoryReport)
		if err != nil {
			return err
		}
		return engine.ApplyProviderInventoryReport(report)
	case control.MessageTypeProviderAuthReport:
		report, err := control.Decode[control.ProviderAuthReport](env, control.MessageTypeProviderAuthReport)
		if err != nil {
			return err
		}
		if report.ProviderInstanceID == "" {
			return control.ErrInvalidPayload
		}
		return engine.UpdateProviderAuth(report.ProviderInstanceID, report.Auth)
	case control.MessageTypeProviderUsageReport:
		report, err := control.Decode[control.ProviderUsageReport](env, control.MessageTypeProviderUsageReport)
		if err != nil {
			return err
		}
		if report.ProviderInstanceID == "" {
			return control.ErrInvalidPayload
		}
		return engine.UpdateProviderUsage(report.ProviderInstanceID, report.Usage, report.ReportedAt)
	case control.MessageTypeAuthSnapshot:
		snapshot, err := control.Decode[control.AuthSnapshot](env, control.MessageTypeAuthSnapshot)
		if err != nil {
			return err
		}
		if snapshot.ProviderInstanceID == "" {
			return control.ErrInvalidPayload
		}
		auth := snapshot.Auth
		if auth.Status == "" {
			auth.Status = provider.AuthUnknown
		}
		if auth.SelectedSource == "" && snapshot.Source != "" {
			auth.SelectedSource = snapshot.Source
		}
		return engine.UpdateProviderAuth(snapshot.ProviderInstanceID, auth)
	case control.MessageTypeAuthRefreshResult:
		result, err := control.Decode[control.AuthRefreshResult](env, control.MessageTypeAuthRefreshResult)
		if err != nil {
			return err
		}
		if result.ProviderInstanceID == "" {
			return control.ErrInvalidPayload
		}
		auth := result.Auth
		if auth.Status == "" {
			if result.OK {
				auth.Status = provider.AuthHealthy
			} else {
				auth.Status = provider.AuthUnavailable
			}
		}
		if !result.OK && result.Error != nil && auth.LastRefreshErr == "" {
			auth.LastRefreshErr = result.Error.Message
		}
		if !result.ReportedAt.IsZero() && auth.LastRefreshAt.IsZero() {
			auth.LastRefreshAt = result.ReportedAt
		}
		if err := engine.UpdateProviderAuth(result.ProviderInstanceID, auth); err != nil {
			return err
		}
		engine.completeAuthRefreshResult(result)
		return nil
	case control.MessageTypeNodeHello:
		hello, err := control.Decode[control.NodeHello](env, control.MessageTypeNodeHello)
		if err != nil {
			return err
		}
		return engine.UpdateNodeHello(hello, env.SentAt)
	case control.MessageTypeNodeHeartbeat:
		heartbeat, err := control.Decode[control.NodeHeartbeat](env, control.MessageTypeNodeHeartbeat)
		if err != nil {
			return err
		}
		return engine.UpdateNodeHeartbeat(heartbeat)
	default:
		return nil
	}
}

func bindControlSessionForEnvelope(engine *Engine, session *controlSession, env control.Envelope) {
	switch env.Type {
	case control.MessageTypeProviderRegister:
		registration, err := control.Decode[control.ProviderRegisterPayload](env, control.MessageTypeProviderRegister)
		if err == nil {
			engine.bindProviderControlSession(registration.Identity.ProviderInstanceID, session)
		}
	case control.MessageTypeProviderHeartbeat:
		heartbeat, err := control.Decode[control.ProviderHeartbeat](env, control.MessageTypeProviderHeartbeat)
		if err == nil {
			engine.bindProviderControlSession(heartbeat.ProviderInstanceID, session)
		}
	case control.MessageTypeProviderAuthReport:
		report, err := control.Decode[control.ProviderAuthReport](env, control.MessageTypeProviderAuthReport)
		if err == nil {
			engine.bindProviderControlSession(report.ProviderInstanceID, session)
		}
	case control.MessageTypeProviderUsageReport:
		report, err := control.Decode[control.ProviderUsageReport](env, control.MessageTypeProviderUsageReport)
		if err == nil {
			engine.bindProviderControlSession(report.ProviderInstanceID, session)
		}
	case control.MessageTypeAuthRefreshResult:
		result, err := control.Decode[control.AuthRefreshResult](env, control.MessageTypeAuthRefreshResult)
		if err == nil {
			engine.bindProviderControlSession(result.ProviderInstanceID, session)
		}
	case control.MessageTypeAuthSnapshot:
		snapshot, err := control.Decode[control.AuthSnapshot](env, control.MessageTypeAuthSnapshot)
		if err == nil {
			engine.bindProviderControlSession(snapshot.ProviderInstanceID, session)
		}
	}
}

func writeControlAck(session *controlSession, replyTo string) error {
	env, err := control.NewEnvelope(control.MessageTypeAck, "ack_"+replyTo, time.Now().UTC(), control.Ack{
		ReplyTo: replyTo,
		OK:      true,
		Message: "ok",
	})
	if err != nil {
		return err
	}
	data, err := control.MarshalEnvelope(env)
	if err != nil {
		return err
	}
	session.writeMu.Lock()
	defer session.writeMu.Unlock()
	return session.conn.WriteMessage(websocket.TextMessage, data)
}

func writeControlError(session *controlSession, code string, err error) error {
	if err == nil {
		err = errors.New("unknown error")
	}
	if errors.Is(err, provider.ErrProviderNotFound) {
		code = "provider_not_found"
	}
	payload := control.ControlError{Code: code, Message: err.Error()}
	env, envelopeErr := control.NewEnvelope(control.MessageTypeControlError, "err_"+time.Now().UTC().Format("20060102150405.000000000"), time.Now().UTC(), payload)
	if envelopeErr != nil {
		return envelopeErr
	}
	data, envelopeErr := control.MarshalEnvelope(env)
	if envelopeErr != nil {
		return envelopeErr
	}
	session.writeMu.Lock()
	defer session.writeMu.Unlock()
	return session.conn.WriteMessage(websocket.TextMessage, data)
}
