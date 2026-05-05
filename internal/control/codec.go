package control

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

var (
	ErrInvalidEnvelope    = errors.New("invalid control envelope")
	ErrInvalidMessageType = errors.New("invalid control message type")
	ErrInvalidPayload     = errors.New("invalid control payload")
)

func NewEnvelope(messageType MessageType, id string, sentAt time.Time, payload any) (Envelope, error) {
	raw, err := marshalPayload(payload)
	if err != nil {
		return Envelope{}, err
	}
	env := Envelope{
		Version: ProtocolVersion,
		Type:    messageType,
		ID:      id,
		SentAt:  sentAt,
		Payload: raw,
	}
	if err := env.Validate(); err != nil {
		return Envelope{}, err
	}
	return env, nil
}

func Marshal(messageType MessageType, id string, sentAt time.Time, payload any) ([]byte, error) {
	env, err := NewEnvelope(messageType, id, sentAt, payload)
	if err != nil {
		return nil, err
	}
	return MarshalEnvelope(env)
}

func MarshalEnvelope(env Envelope) ([]byte, error) {
	if err := env.Validate(); err != nil {
		return nil, err
	}
	out, err := json.Marshal(env)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidEnvelope, err)
	}
	return out, nil
}

func Unmarshal(data []byte) (Envelope, error) {
	return UnmarshalEnvelope(data)
}

func UnmarshalEnvelope(data []byte) (Envelope, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return Envelope{}, fmt.Errorf("%w: %v", ErrInvalidEnvelope, err)
	}
	for _, name := range []string{"version", "type", "id", "sent_at", "payload"} {
		if _, ok := fields[name]; !ok {
			return Envelope{}, fmt.Errorf("%w: missing required field %q", ErrInvalidEnvelope, name)
		}
	}

	var env Envelope
	if err := json.Unmarshal(data, &env); err != nil {
		return Envelope{}, fmt.Errorf("%w: %v", ErrInvalidEnvelope, err)
	}
	if err := env.Validate(); err != nil {
		return Envelope{}, err
	}
	return env, nil
}

func (e Envelope) Validate() error {
	if strings.TrimSpace(e.Version) == "" {
		return fmt.Errorf("%w: missing version", ErrInvalidEnvelope)
	}
	if e.Version != ProtocolVersion {
		return fmt.Errorf("%w: unsupported version %q", ErrInvalidEnvelope, e.Version)
	}
	if strings.TrimSpace(string(e.Type)) == "" {
		return fmt.Errorf("%w: missing type", ErrInvalidEnvelope)
	}
	if strings.TrimSpace(e.ID) == "" {
		return fmt.Errorf("%w: missing id", ErrInvalidEnvelope)
	}
	if e.SentAt.IsZero() {
		return fmt.Errorf("%w: missing sent_at", ErrInvalidEnvelope)
	}
	if len(e.Payload) == 0 {
		return fmt.Errorf("%w: missing payload", ErrInvalidEnvelope)
	}
	return nil
}

func DecodePayload(env Envelope, want MessageType, dst any) error {
	if err := env.Validate(); err != nil {
		return err
	}
	if env.Type != want {
		return fmt.Errorf("%w: got %q want %q", ErrInvalidMessageType, env.Type, want)
	}
	if dst == nil {
		return fmt.Errorf("%w: nil destination", ErrInvalidPayload)
	}
	if err := json.Unmarshal(env.Payload, dst); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidPayload, err)
	}
	return nil
}

func Decode[T any](env Envelope, want MessageType) (T, error) {
	var out T
	err := DecodePayload(env, want, &out)
	return out, err
}

func (e Envelope) DecodePayload(want MessageType, dst any) error {
	return DecodePayload(e, want, dst)
}

func DecodeKnownPayload(env Envelope) (any, error) {
	switch env.Type {
	case MessageTypeNodeHello:
		return Decode[NodeHello](env, MessageTypeNodeHello)
	case MessageTypeNodeHeartbeat:
		return Decode[NodeHeartbeat](env, MessageTypeNodeHeartbeat)
	case MessageTypeProviderRegister:
		return Decode[ProviderRegisterPayload](env, MessageTypeProviderRegister)
	case MessageTypeProviderHeartbeat:
		return Decode[ProviderHeartbeat](env, MessageTypeProviderHeartbeat)
	case MessageTypeProviderInventoryReport:
		return Decode[ProviderInventoryReport](env, MessageTypeProviderInventoryReport)
	case MessageTypeProviderAuthReport:
		return Decode[ProviderAuthReport](env, MessageTypeProviderAuthReport)
	case MessageTypeProviderUsageReport:
		return Decode[ProviderUsageReport](env, MessageTypeProviderUsageReport)
	case MessageTypeAuthRefreshRequest:
		return Decode[AuthRefreshRequest](env, MessageTypeAuthRefreshRequest)
	case MessageTypeAuthRefreshResult:
		return Decode[AuthRefreshResult](env, MessageTypeAuthRefreshResult)
	case MessageTypeProviderDrain:
		return Decode[ProviderDrain](env, MessageTypeProviderDrain)
	case MessageTypeStreamOpenRequest:
		return Decode[StreamOpenRequest](env, MessageTypeStreamOpenRequest)
	case MessageTypeStreamOpenReady:
		return Decode[StreamOpenReady](env, MessageTypeStreamOpenReady)
	case MessageTypeStreamCancel:
		return Decode[StreamCancel](env, MessageTypeStreamCancel)
	case MessageTypeStreamClosed:
		return Decode[StreamClosed](env, MessageTypeStreamClosed)
	case MessageTypeAck:
		return Decode[Ack](env, MessageTypeAck)
	case MessageTypeControlError:
		return Decode[ControlError](env, MessageTypeControlError)
	default:
		return nil, fmt.Errorf("%w: %q", ErrInvalidMessageType, env.Type)
	}
}

func marshalPayload(payload any) (json.RawMessage, error) {
	if payload == nil {
		return nil, fmt.Errorf("%w: nil payload", ErrInvalidPayload)
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidPayload, err)
	}
	if len(raw) == 0 {
		return nil, fmt.Errorf("%w: empty payload", ErrInvalidPayload)
	}
	return raw, nil
}
