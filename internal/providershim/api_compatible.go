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
	ControlURL           string
	DataURL              string
	HeartbeatInterval    time.Duration
	TokenKey             []byte
	Provider             *apiprovider.Provider
	AuthRefresher        AuthRefresher
	AutoRefreshThreshold time.Duration
	AutoRefreshCooldown  time.Duration
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
	var dynamicHealth healthReporter
	if reporter, ok := any(opts.Provider).(healthReporter); ok {
		dynamicHealth = reporter
	}
	var dynamicAuth authReporter
	if registration.Identity.Kind == provider.KindAPICompatible {
		if reporter, ok := any(opts.Provider).(authReporter); ok {
			dynamicAuth = reporter
		}
	}
	eg.Go(func() error {
		return RunStaticControlClient(ctx, StaticControlClientOptions{
			ControlURL:           opts.ControlURL,
			HeartbeatInterval:    opts.HeartbeatInterval,
			Registration:         registration,
			UsageReporter:        opts.Provider,
			HealthReporter:       dynamicHealth,
			AuthReporter:         dynamicAuth,
			AuthRefresher:        opts.AuthRefresher,
			AutoRefreshThreshold: opts.AutoRefreshThreshold,
			AutoRefreshCooldown:  opts.AutoRefreshCooldown,
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
	ControlURL           string
	HeartbeatInterval    time.Duration
	Registration         provider.Registration
	UsageReporter        usageReporter
	HealthReporter       healthReporter
	AuthReporter         authReporter
	AuthRefresher        AuthRefresher
	AutoRefreshThreshold time.Duration
	AutoRefreshCooldown  time.Duration
}

type usageReporter interface {
	Usage() (provider.UsageReport, error)
}

type healthReporter interface {
	Health() (provider.Health, error)
}

type authReporter interface {
	Auth() (provider.AuthState, error)
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
			if err := handleStaticControlRequest(ctx, client, state, opts.AuthRefresher, env); err != nil {
				if ctx.Err() != nil {
					return nil
				}
				return err
			}
		case <-ticker.C:
			if err := writeStaticHeartbeat(ctx, client, state, opts.HealthReporter, opts.AuthReporter, "provider_heartbeat_"+time.Now().UTC().Format("20060102150405.000000000")); err != nil {
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
			if err := maybeStaticAutoAuthRefresh(ctx, client, state, opts.AuthRefresher, opts.AutoRefreshThreshold, opts.AutoRefreshCooldown); err != nil {
				if ctx.Err() != nil {
					return nil
				}
				return err
			}
		}
	}
}

type staticControlState struct {
	mu                sync.RWMutex
	registration      provider.Registration
	lastAutoRefreshAt time.Time
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

func (s *staticControlState) setAuth(auth provider.AuthState) provider.Registration {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.registration.Auth = auth
	return s.registration
}

func (s *staticControlState) setHealthFromReporter(health provider.Health) provider.Registration {
	s.mu.Lock()
	defer s.mu.Unlock()
	if health.Status == "" {
		return s.registration
	}
	if s.registration.Health.Status == provider.HealthDraining {
		return s.registration
	}
	s.registration.Health = health
	return s.registration
}

func (s *staticControlState) setAuthFromReporter(auth provider.AuthState) provider.Registration {
	s.mu.Lock()
	defer s.mu.Unlock()
	if auth.Status == "" {
		return s.registration
	}
	if s.registration.Auth.Status == provider.AuthRefreshing {
		return s.registration
	}
	s.registration.Auth = auth
	return s.registration
}

func (s *staticControlState) claimAutoRefresh(now time.Time, cooldown time.Duration) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if cooldown > 0 && !s.lastAutoRefreshAt.IsZero() && now.Sub(s.lastAutoRefreshAt) < cooldown {
		return false
	}
	s.lastAutoRefreshAt = now
	return true
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

func writeStaticHeartbeat(ctx context.Context, client *controlClientConn, state *staticControlState, healthReporter healthReporter, authReporter authReporter, id string) error {
	if healthReporter != nil {
		if health, err := healthReporter.Health(); err == nil {
			state.setHealthFromReporter(health)
		}
	}
	if authReporter != nil {
		if auth, err := authReporter.Auth(); err == nil {
			state.setAuthFromReporter(auth)
		}
	}
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

func handleStaticControlRequest(ctx context.Context, client *controlClientConn, state *staticControlState, refresher AuthRefresher, env control.Envelope) error {
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
		result := executeStaticAuthRefresh(ctx, state, refresher, request)
		return client.sendAndWaitAck(ctx, control.MessageTypeAuthRefreshResult, "auth_refresh_result_"+request.RefreshID, result)
	default:
		return nil
	}
}

func maybeStaticAutoAuthRefresh(ctx context.Context, client *controlClientConn, state *staticControlState, refresher AuthRefresher, threshold time.Duration, cooldown time.Duration) error {
	if refresher == nil || threshold <= 0 {
		return nil
	}
	now := time.Now().UTC()
	registration := state.registrationSnapshot()
	if !shouldAutoRefreshAuth(registration.Auth, threshold, now) {
		return nil
	}
	if !state.claimAutoRefresh(now, cooldown) {
		return nil
	}
	refreshID := "auto_refresh_" + registration.Identity.ProviderInstanceID + "_" + now.Format("20060102150405.000000000")
	result := executeStaticAuthRefresh(ctx, state, refresher, control.AuthRefreshRequest{
		RefreshID:          refreshID,
		ProviderInstanceID: registration.Identity.ProviderInstanceID,
		Reason:             "auto refresh threshold reached",
	})
	return client.sendAndWaitAck(ctx, control.MessageTypeAuthRefreshResult, "auth_refresh_result_"+refreshID, result)
}

func shouldAutoRefreshAuth(auth provider.AuthState, threshold time.Duration, now time.Time) bool {
	if !auth.Refreshable {
		return false
	}
	switch auth.Status {
	case provider.AuthExpired, provider.AuthRevoked, provider.AuthRefreshSoon:
		return true
	case provider.AuthRefreshing:
		return false
	}
	if auth.ExpiresAt.IsZero() {
		return false
	}
	if !auth.ExpiresAt.After(now) {
		return true
	}
	return !auth.ExpiresAt.After(now.Add(threshold))
}

func executeStaticAuthRefresh(ctx context.Context, state *staticControlState, refresher AuthRefresher, request control.AuthRefreshRequest) control.AuthRefreshResult {
	registration := state.registrationSnapshot()
	auth := registration.Auth
	ok := false
	var refreshErr error
	if request.ProviderInstanceID != "" && request.ProviderInstanceID != registration.Identity.ProviderInstanceID {
		auth.Status = provider.AuthUnavailable
		auth.LastRefreshErr = "refresh request provider_instance_id does not match this shim"
		refreshErr = fmt.Errorf("%w: %s", ErrShimConfig, auth.LastRefreshErr)
	} else if refresher == nil {
		auth.Status = provider.AuthUnavailable
		auth.LastRefreshErr = "auth refresh unsupported for this provider"
		refreshErr = fmt.Errorf("%w: %s", ErrShimConfig, auth.LastRefreshErr)
	} else {
		auth.Status = provider.AuthRefreshing
		state.setAuth(auth)
		auth, refreshErr = refresher.RefreshAuth(ctx, request, registration)
		if refreshErr != nil {
			if auth.Status == "" || auth.Status == provider.AuthRefreshing {
				auth.Status = provider.AuthUnavailable
			}
			auth.LastRefreshErr = refreshErr.Error()
		} else {
			if auth.Status == "" || auth.Status == provider.AuthRefreshing {
				auth.Status = provider.AuthHealthy
			}
			auth.LastRefreshErr = ""
			ok = true
		}
	}
	state.setAuth(auth)
	result := control.AuthRefreshResult{
		RefreshID:          request.RefreshID,
		ProviderInstanceID: registration.Identity.ProviderInstanceID,
		Auth:               auth,
		OK:                 ok,
		ReportedAt:         time.Now().UTC(),
	}
	if refreshErr != nil {
		result.Error = &control.ErrorPayload{Code: "refresh_failed", Message: refreshErr.Error()}
	}
	return result
}
