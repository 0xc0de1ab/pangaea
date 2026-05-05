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

func handleControlWS(engine *Engine) gin.HandlerFunc {
	return func(c *gin.Context) {
		if engine == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": ErrRouterNotReady.Error()})
			return
		}
		conn, err := controlUpgrader.Upgrade(c.Writer, c.Request, nil)
		if err != nil {
			return
		}
		defer conn.Close()

		for {
			_, data, err := conn.ReadMessage()
			if err != nil {
				return
			}
			env, err := control.Unmarshal(data)
			if err != nil {
				_ = writeControlError(conn, "invalid_envelope", err)
				continue
			}
			if err := applyControlEnvelope(engine, env); err != nil {
				_ = writeControlError(conn, "apply_failed", err)
				continue
			}
			_ = writeControlAck(conn, env.ID)
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
		for _, registration := range report.Providers {
			if err := engine.UpsertProvider(registration); err != nil {
				return err
			}
		}
		return nil
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
		_, err := control.Decode[control.ProviderUsageReport](env, control.MessageTypeProviderUsageReport)
		return err
	case control.MessageTypeNodeHello, control.MessageTypeNodeHeartbeat:
		_, err := control.DecodeKnownPayload(env)
		return err
	default:
		return control.ErrInvalidMessageType
	}
}

func writeControlAck(conn *websocket.Conn, replyTo string) error {
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
	return conn.WriteMessage(websocket.TextMessage, data)
}

func writeControlError(conn *websocket.Conn, code string, err error) error {
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
	return conn.WriteMessage(websocket.TextMessage, data)
}
