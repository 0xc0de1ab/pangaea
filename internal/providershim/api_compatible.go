package providershim

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/0xc0de1ab/pangaea/internal/apiprovider"
	"github.com/0xc0de1ab/pangaea/internal/control"
	"github.com/0xc0de1ab/pangaea/internal/provider"
	"github.com/gorilla/websocket"
	"golang.org/x/sync/errgroup"
)

type APICompatibleShimOptions struct {
	ControlURL        string
	DataURL           string
	HeartbeatInterval time.Duration
	TokenKey          []byte
	Provider          *apiprovider.Provider
}

func RunAPICompatibleShim(ctx context.Context, opts APICompatibleShimOptions) error {
	if opts.ControlURL == "" {
		return fmt.Errorf("%w: control url is required", ErrShimConfig)
	}
	if opts.Provider == nil {
		return fmt.Errorf("%w: provider is required", ErrShimConfig)
	}
	registration, err := opts.Provider.Registration()
	if err != nil {
		return err
	}
	dataURL := opts.DataURL
	if dataURL == "" {
		dataURL, err = DeriveDataURL(opts.ControlURL, registration.Identity.ProviderInstanceID)
		if err != nil {
			return err
		}
	}

	eg, ctx := errgroup.WithContext(ctx)
	eg.Go(func() error {
		return RunStaticControlClient(ctx, StaticControlClientOptions{
			ControlURL:        opts.ControlURL,
			HeartbeatInterval: opts.HeartbeatInterval,
			Registration:      registration,
			UsageReporter:     opts.Provider,
		})
	})
	eg.Go(func() error {
		return RunSimulatorDataClient(ctx, DataClientOptions{
			DataURL:  dataURL,
			TokenKey: opts.TokenKey,
			Provider: opts.Provider,
		})
	})
	return eg.Wait()
}

type StaticControlClientOptions struct {
	ControlURL        string
	HeartbeatInterval time.Duration
	Registration      provider.Registration
	UsageReporter     usageReporter
}

type usageReporter interface {
	Usage() (provider.UsageReport, error)
}

func RunStaticControlClient(ctx context.Context, opts StaticControlClientOptions) error {
	if opts.ControlURL == "" {
		return fmt.Errorf("%w: control url is required", ErrShimConfig)
	}
	if err := opts.Registration.Validate(); err != nil {
		return err
	}
	heartbeatInterval := opts.HeartbeatInterval
	if heartbeatInterval <= 0 {
		heartbeatInterval = 30 * time.Second
	}
	state := newStaticControlState(opts.Registration)
	conn, _, err := websocket.DefaultDialer.DialContext(ctx, opts.ControlURL, nil)
	if err != nil {
		return err
	}
	defer conn.Close()
	client := newControlClientConn(conn)
	defer client.close()

	if err := client.sendAndWaitAck(ctx, control.MessageTypeProviderRegister, "provider_register", state.registrationSnapshot()); err != nil {
		return err
	}
	if err := writeStaticInventoryReport(ctx, client, state, "provider_inventory_initial"); err != nil {
		return err
	}
	if err := writeStaticAuthReport(ctx, client, state, "provider_auth_initial"); err != nil {
		return err
	}
	if err := writeStaticUsageReport(ctx, client, state, opts.UsageReporter, "provider_usage_initial"); err != nil {
		return err
	}

	ticker := time.NewTicker(heartbeatInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case err := <-client.errCh:
			if ctx.Err() != nil {
				return nil
			}
			return err
		case env := <-client.incoming:
			if err := handleStaticControlRequest(ctx, client, state, env); err != nil {
				if ctx.Err() != nil {
					return nil
				}
				return err
			}
		case <-ticker.C:
			if err := writeStaticHeartbeat(ctx, client, state, "provider_heartbeat_"+time.Now().UTC().Format("20060102150405.000000000")); err != nil {
				if ctx.Err() != nil {
					return nil
				}
				return err
			}
			if err := writeStaticUsageReport(ctx, client, state, opts.UsageReporter, "provider_usage_"+time.Now().UTC().Format("20060102150405.000000000")); err != nil {
				if ctx.Err() != nil {
					return nil
				}
				return err
			}
		}
	}
}

type staticControlState struct {
	mu           sync.RWMutex
	registration provider.Registration
}

func newStaticControlState(registration provider.Registration) *staticControlState {
	if registration.Auth.Status == "" {
		registration.Auth.Status = provider.AuthHealthy
	}
	return &staticControlState{registration: registration}
}

func (s *staticControlState) registrationSnapshot() provider.Registration {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.registration
}

func (s *staticControlState) setDrain(drain bool, reason string) provider.Registration {
	s.mu.Lock()
	defer s.mu.Unlock()
	if drain {
		s.registration.Health.Status = provider.HealthDraining
	} else {
		s.registration.Health.Status = provider.HealthReady
	}
	s.registration.Health.Reason = reason
	s.registration.Health.CheckedAt = time.Now().UTC()
	return s.registration
}

func writeStaticInventoryReport(ctx context.Context, client *controlClientConn, state *staticControlState, id string) error {
	registration := state.registrationSnapshot()
	now := time.Now().UTC()
	return client.sendAndWaitAck(ctx, control.MessageTypeProviderInventoryReport, id, control.ProviderInventoryReport{
		Mode:       "full",
		NodeID:     registration.Identity.NodeID,
		HostName:   registration.Identity.HostName,
		Providers:  []provider.Registration{registration},
		ReportedAt: now,
	})
}

func writeStaticAuthReport(ctx context.Context, client *controlClientConn, state *staticControlState, id string) error {
	registration := state.registrationSnapshot()
	return client.sendAndWaitAck(ctx, control.MessageTypeProviderAuthReport, id, control.ProviderAuthReport{
		ProviderInstanceID: registration.Identity.ProviderInstanceID,
		Auth:               registration.Auth,
		ReportedAt:         time.Now().UTC(),
	})
}

func writeStaticHeartbeat(ctx context.Context, client *controlClientConn, state *staticControlState, id string) error {
	registration := state.registrationSnapshot()
	return client.sendAndWaitAck(ctx, control.MessageTypeProviderHeartbeat, id, control.ProviderHeartbeat{
		ProviderInstanceID: registration.Identity.ProviderInstanceID,
		Health:             registration.Health,
		Auth:               registration.Auth,
		Limits:             registration.Limits,
		ReportedAt:         time.Now().UTC(),
	})
}

func writeStaticUsageReport(ctx context.Context, client *controlClientConn, state *staticControlState, reporter usageReporter, id string) error {
	if reporter == nil {
		return nil
	}
	usage, err := reporter.Usage()
	if err != nil {
		return nil
	}
	registration := state.registrationSnapshot()
	return client.sendAndWaitAck(ctx, control.MessageTypeProviderUsageReport, id, control.ProviderUsageReport{
		ProviderInstanceID: registration.Identity.ProviderInstanceID,
		Usage:              usage,
		ReportedAt:         time.Now().UTC(),
	})
}

func handleStaticControlRequest(ctx context.Context, client *controlClientConn, state *staticControlState, env control.Envelope) error {
	switch env.Type {
	case control.MessageTypeProviderDrain:
		request, err := control.Decode[control.ProviderDrain](env, control.MessageTypeProviderDrain)
		if err != nil {
			return err
		}
		registration := state.setDrain(request.Drain, request.Reason)
		return client.sendAndWaitAck(ctx, control.MessageTypeProviderHeartbeat, "provider_heartbeat_drain_"+time.Now().UTC().Format("20060102150405.000000000"), control.ProviderHeartbeat{
			ProviderInstanceID: registration.Identity.ProviderInstanceID,
			Health:             registration.Health,
			Auth:               registration.Auth,
			Limits:             registration.Limits,
			ReportedAt:         time.Now().UTC(),
		})
	case control.MessageTypeAuthRefreshRequest:
		request, err := control.Decode[control.AuthRefreshRequest](env, control.MessageTypeAuthRefreshRequest)
		if err != nil {
			return err
		}
		registration := state.registrationSnapshot()
		auth := registration.Auth
		auth.Status = provider.AuthUnavailable
		auth.LastRefreshErr = "auth refresh unsupported for api-compatible provider"
		return client.sendAndWaitAck(ctx, control.MessageTypeAuthRefreshResult, "auth_refresh_result_"+request.RefreshID, control.AuthRefreshResult{
			RefreshID:          request.RefreshID,
			ProviderInstanceID: registration.Identity.ProviderInstanceID,
			Auth:               auth,
			OK:                 false,
			Error:              &control.ErrorPayload{Code: "unsupported", Message: auth.LastRefreshErr},
			ReportedAt:         time.Now().UTC(),
		})
	default:
		return fmt.Errorf("%w: unsupported control request type %q", ErrShimConfig, env.Type)
	}
}
