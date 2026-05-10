package control

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/0xc0de1ab/pangaea/internal/provider"
)

func TestUnmarshalEnvelopeValidation(t *testing.T) {
	cases := []struct {
		name     string
		raw      string
		wantType MessageType
		wantErr  error
	}{
		{
			name: "valid envelope with unknown optional fields",
			raw: `{
				"version":"provider-protocol/v1",
				"type":"node.heartbeat",
				"id":"msg_01",
				"sent_at":"2026-05-05T00:00:00Z",
				"trace":{"node_id":"a1"},
				"payload":{"node_id":"a1","extra":"ignored"},
				"extra":"ignored"
			}`,
		},
		{
			name:    "malformed json",
			raw:     `{not json`,
			wantErr: ErrInvalidEnvelope,
		},
		{
			name: "missing version",
			raw: `{
				"type":"node.heartbeat",
				"id":"msg_01",
				"sent_at":"2026-05-05T00:00:00Z",
				"payload":{}
			}`,
			wantErr: ErrInvalidEnvelope,
		},
		{
			name: "unsupported version",
			raw: `{
				"version":"provider-protocol/v2",
				"type":"node.heartbeat",
				"id":"msg_01",
				"sent_at":"2026-05-05T00:00:00Z",
				"payload":{}
			}`,
			wantErr: ErrInvalidEnvelope,
		},
		{
			name: "missing type",
			raw: `{
				"version":"provider-protocol/v1",
				"id":"msg_01",
				"sent_at":"2026-05-05T00:00:00Z",
				"payload":{}
			}`,
			wantErr: ErrInvalidEnvelope,
		},
		{
			name: "unknown type is forward-compatible",
			raw: `{
				"version":"provider-protocol/v1",
				"type":"made.up",
				"id":"msg_01",
				"sent_at":"2026-05-05T00:00:00Z",
				"payload":{}
			}`,
			wantType: MessageType("made.up"),
		},
		{
			name: "missing id",
			raw: `{
				"version":"provider-protocol/v1",
				"type":"node.heartbeat",
				"sent_at":"2026-05-05T00:00:00Z",
				"payload":{}
			}`,
			wantErr: ErrInvalidEnvelope,
		},
		{
			name: "missing sent_at",
			raw: `{
				"version":"provider-protocol/v1",
				"type":"node.heartbeat",
				"id":"msg_01",
				"payload":{}
			}`,
			wantErr: ErrInvalidEnvelope,
		},
		{
			name: "missing payload",
			raw: `{
				"version":"provider-protocol/v1",
				"type":"node.heartbeat",
				"id":"msg_01",
				"sent_at":"2026-05-05T00:00:00Z"
			}`,
			wantErr: ErrInvalidEnvelope,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env, err := Unmarshal([]byte(tc.raw))
			if tc.wantErr == nil {
				if err != nil {
					t.Fatalf("Unmarshal: %v", err)
				}
				if env.Version != ProtocolVersion {
					t.Fatalf("version got %q want %q", env.Version, ProtocolVersion)
				}
				wantType := tc.wantType
				if wantType == "" {
					wantType = MessageTypeNodeHeartbeat
				}
				if env.Type != wantType {
					t.Fatalf("type got %q want %q", env.Type, wantType)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected error")
			}
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("error got %v want %v", err, tc.wantErr)
			}
		})
	}
}

func TestProviderRegisterRoundTrip(t *testing.T) {
	ts := time.Date(2026, 5, 5, 0, 0, 0, 0, time.UTC)
	registration := ProviderRegisterPayload{
		Identity: provider.ProviderIdentity{
			ProviderType:       "codex-primary",
			ProviderInstanceID: "codex-primary/a1/01",
			NodeID:             "a1",
			HostName:           "snowbox",
			ContainerID:        "docker://abc123",
			Service:            provider.ServiceCodex,
			Kind:               provider.KindCLIContainer,
			Account:            provider.Account{ID: "user-H8Pbt", Display: "primary@example.test"},
		},
		Capabilities: []provider.Capability{
			provider.CapabilityOpenAIChat,
			provider.CapabilityAnthropicMessages,
			provider.CapabilityGeminiGenerateContent,
			provider.CapabilityUsageRead,
			provider.CapabilityAuthFile,
			provider.CapabilityAuthRefreshOneshot,
			provider.CapabilityStreamSSE,
		},
		Models: []provider.Model{
			{
				ID:            "gpt-5.3-codex-spark",
				Aliases:       []string{"gpt-5.3", "codex-spark"},
				Capabilities:  []provider.Capability{provider.CapabilityOpenAIChat, provider.CapabilityStreamSSE},
				ContextTokens: 200000,
			},
		},
		Health:       provider.Health{Status: provider.HealthReady, CheckedAt: ts},
		Auth:         provider.AuthState{Status: provider.AuthHealthy, ExpiresAt: ts.Add(24 * time.Hour), Refreshable: true},
		Limits:       provider.LimitState{MaxConcurrency: 2, QueueDepth: 0},
		RegisteredAt: ts,
	}

	data, err := Marshal(MessageTypeProviderRegister, "msg_01", ts, registration)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	env, err := Unmarshal(data)
	if err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if env.Type != MessageTypeProviderRegister {
		t.Fatalf("type got %q want %q", env.Type, MessageTypeProviderRegister)
	}

	var got ProviderRegisterPayload
	if err := DecodePayload(env, MessageTypeProviderRegister, &got); err != nil {
		t.Fatalf("DecodePayload: %v", err)
	}
	if err := got.Validate(); err != nil {
		t.Fatalf("Validate registration: %v", err)
	}
	if !reflect.DeepEqual(got, registration) {
		t.Fatalf("registration mismatch:\ngot  %#v\nwant %#v", got, registration)
	}
}

func TestAuthSnapshotAndPushRoundTrip(t *testing.T) {
	ts := time.Date(2026, 5, 5, 0, 0, 0, 0, time.UTC)
	snapshot := AuthSnapshot{
		ProviderInstanceID: "codex-primary/a1/01",
		AccountID:          "acct-1",
		Auth: provider.AuthState{
			Status:         provider.AuthRefreshSoon,
			Account:        provider.Account{ID: "acct-1", Display: "primary@example.test"},
			ExpiresAt:      ts.Add(time.Hour),
			Refreshable:    true,
			SelectedSource: "container",
		},
		Fingerprint: "sha256:auth-state",
		Source:      "container",
		ObservedAt:  ts,
		ReportedAt:  ts,
	}
	data, err := Marshal(MessageTypeAuthSnapshot, "auth_snapshot_1", ts, snapshot)
	if err != nil {
		t.Fatalf("Marshal snapshot: %v", err)
	}
	env, err := Unmarshal(data)
	if err != nil {
		t.Fatalf("Unmarshal snapshot: %v", err)
	}
	gotSnapshot, err := Decode[AuthSnapshot](env, MessageTypeAuthSnapshot)
	if err != nil {
		t.Fatalf("Decode snapshot: %v", err)
	}
	if !reflect.DeepEqual(gotSnapshot, snapshot) {
		t.Fatalf("snapshot mismatch:\ngot  %#v\nwant %#v", gotSnapshot, snapshot)
	}

	push := AuthPush{
		PushID:             "push_1",
		ProviderInstanceID: "codex-primary/a1/01",
		AccountID:          "acct-1",
		Auth:               snapshot.Auth,
		Fingerprint:        snapshot.Fingerprint,
		Source:             "router",
		Reason:             "operator sync",
		DeadlineAt:         ts.Add(30 * time.Second),
	}
	data, err = Marshal(MessageTypeAuthPush, "auth_push_1", ts, push)
	if err != nil {
		t.Fatalf("Marshal push: %v", err)
	}
	env, err = Unmarshal(data)
	if err != nil {
		t.Fatalf("Unmarshal push: %v", err)
	}
	gotPush, err := Decode[AuthPush](env, MessageTypeAuthPush)
	if err != nil {
		t.Fatalf("Decode push: %v", err)
	}
	if !reflect.DeepEqual(gotPush, push) {
		t.Fatalf("push mismatch:\ngot  %#v\nwant %#v", gotPush, push)
	}
}

func TestDecodePayloadRejectsTypeMismatch(t *testing.T) {
	ts := time.Date(2026, 5, 5, 0, 0, 0, 0, time.UTC)
	data, err := Marshal(MessageTypeNodeHeartbeat, "msg_01", ts, NodeHeartbeat{NodeID: "a1"})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	env, err := Unmarshal(data)
	if err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	var got NodeHello
	if err := DecodePayload(env, MessageTypeNodeHello, &got); !errors.Is(err, ErrInvalidMessageType) {
		t.Fatalf("expected ErrInvalidMessageType, got %v", err)
	}
}
