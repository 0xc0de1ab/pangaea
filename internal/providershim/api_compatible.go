package providershim

import (
	"context"
	"fmt"
	"os"
	"reflect"
	"strings"
	"sync"
	"time"

	"github.com/0xc0de1ab/pangaea/internal/control"
	"github.com/0xc0de1ab/pangaea/internal/provider"
	"golang.org/x/sync/errgroup"
)

const (
	defaultModelDiscoveryTimeout = 5 * time.Second
	defaultTargetVersionTimeout  = 2 * time.Second
)

type APICompatibleShimOptions struct {
	ControlURL           string
	DataURL              string
	PeerToken            string
	HeartbeatInterval    time.Duration
	TokenKey             []byte
	Provider             APICompatibleProvider
	AuthRefresher        AuthRefresher
	AutoRefreshThreshold time.Duration
	AutoRefreshCooldown  time.Duration
}

type APICompatibleProvider interface {
	providerInvoker
	usageReporter
	modelReporter
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
	var dynamicHealth healthReporter
	if reporter, ok := any(opts.Provider).(healthReporter); ok {
		dynamicHealth = reporter
	}
	var dynamicAuth authReporter
	if reporter, ok := any(opts.Provider).(authReporter); ok {
		dynamicAuth = reporter
	}
	var dynamicTargetVersion targetVersionReporter
	if reporter, ok := any(opts.Provider).(targetVersionReporter); ok {
		dynamicTargetVersion = reporter
	}

	backoff := time.Second
	for {
		err := runAPICompatibleShimSession(ctx, opts, registration, dataURL, dynamicHealth, dynamicAuth, dynamicTargetVersion)
		if ctx.Err() != nil {
			return nil
		}
		if err == nil {
			return nil
		}
		_, _ = fmt.Fprintf(os.Stderr, "provider shim session disconnected: %v; reconnecting in %s\n", err, backoff)
		timer := time.NewTimer(backoff)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil
		case <-timer.C:
		}
		if backoff < 10*time.Second {
			backoff *= 2
			if backoff > 10*time.Second {
				backoff = 10 * time.Second
			}
		}
	}
}

func runAPICompatibleShimSession(ctx context.Context, opts APICompatibleShimOptions, registration provider.Registration, dataURL string, dynamicHealth healthReporter, dynamicAuth authReporter, dynamicTargetVersion targetVersionReporter) error {
	eg, ctx := errgroup.WithContext(ctx)
	eg.Go(func() error {
		authSnapshotReporter, _ := opts.Provider.(authSnapshotReporter)
		authPushApplier, _ := opts.Provider.(authPushApplier)
		if err := RunStaticControlClient(ctx, StaticControlClientOptions{
			ControlURL:            opts.ControlURL,
			PeerToken:             opts.PeerToken,
			HeartbeatInterval:     opts.HeartbeatInterval,
			Registration:          registration,
			UsageReporter:         opts.Provider,
			HealthReporter:        dynamicHealth,
			AuthReporter:          dynamicAuth,
			ModelReporter:         opts.Provider,
			TargetVersionReporter: dynamicTargetVersion,
			AuthSnapshotReporter:  authSnapshotReporter,
			AuthPushApplier:       authPushApplier,
			AuthRefresher:         opts.AuthRefresher,
			AutoRefreshThreshold:  opts.AutoRefreshThreshold,
			AutoRefreshCooldown:   opts.AutoRefreshCooldown,
		}); err != nil {
			err = fmt.Errorf("control ws: %w", err)
			_, _ = fmt.Fprintf(os.Stderr, "provider shim control session ended: %v\n", err)
			return err
		}
		return nil
	})
	eg.Go(func() error {
		if err := RunSimulatorDataClient(ctx, DataClientOptions{
			DataURL:   dataURL,
			PeerToken: opts.PeerToken,
			TokenKey:  opts.TokenKey,
			Provider:  opts.Provider,
		}); err != nil {
			err = fmt.Errorf("data ws: %w", err)
			_, _ = fmt.Fprintf(os.Stderr, "provider shim data session ended: %v\n", err)
			return err
		}
		return nil
	})
	return eg.Wait()
}

type StaticControlClientOptions struct {
	ControlURL            string
	PeerToken             string
	HeartbeatInterval     time.Duration
	Registration          provider.Registration
	UsageReporter         usageReporter
	HealthReporter        healthReporter
	AuthReporter          authReporter
	ModelReporter         modelReporter
	TargetVersionReporter targetVersionReporter
	AuthSnapshotReporter  authSnapshotReporter
	AuthPushApplier       authPushApplier
	AuthRefresher         AuthRefresher
	AutoRefreshThreshold  time.Duration
	AutoRefreshCooldown   time.Duration
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

type modelReporter interface {
	Models(context.Context) ([]provider.Model, error)
}

type targetVersionReporter interface {
	TargetVersion(context.Context) (string, error)
}

type forcedModelDiscoveryReporter interface {
	ForceModelDiscovery() bool
}

type AuthSnapshotReport struct {
	Raw         []byte
	Fingerprint string
	Filename    string
	Format      string
}

type authSnapshotReporter interface {
	AuthSnapshot(context.Context) (AuthSnapshotReport, error)
}

type authPushApplier interface {
	ApplyAuthPush(context.Context, control.AuthPush, provider.Registration) (provider.AuthState, error)
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
	refreshStaticTargetVersion(ctx, state, opts.TargetVersionReporter)
	refreshStaticModels(ctx, state, opts.ModelReporter)
	refreshStaticHealth(ctx, state, opts.HealthReporter)
	refreshStaticAuth(ctx, state, opts.AuthReporter)
	conn, _, err := routerWebSocketDialer().DialContext(ctx, opts.ControlURL, routerPeerDialHeader(opts.PeerToken))
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
	if err := writeStaticAuthReport(ctx, client, state, opts.AuthSnapshotReporter, "provider_auth_initial"); err != nil {
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
			if err := handleStaticControlRequest(ctx, client, state, opts.AuthRefresher, opts.AuthSnapshotReporter, opts.AuthPushApplier, env); err != nil {
				if ctx.Err() != nil {
					return nil
				}
				return err
			}
		case <-ticker.C:
			if refreshStaticTargetVersion(ctx, state, opts.TargetVersionReporter) {
				if err := writeStaticInventoryReport(ctx, client, state, "provider_inventory_version_"+time.Now().UTC().Format("20060102150405.000000000")); err != nil {
					if ctx.Err() != nil {
						return nil
					}
					return err
				}
			}
			if refreshStaticModels(ctx, state, opts.ModelReporter) {
				if err := writeStaticInventoryReport(ctx, client, state, "provider_inventory_"+time.Now().UTC().Format("20060102150405.000000000")); err != nil {
					if ctx.Err() != nil {
						return nil
					}
					return err
				}
			}
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
			if err := maybeStaticAutoAuthRefresh(ctx, client, state, opts.AuthRefresher, opts.AuthSnapshotReporter, opts.AutoRefreshThreshold, opts.AutoRefreshCooldown); err != nil {
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
	s.registration.Identity.Account = s.registration.Identity.Account.MergeMissingFrom(auth.Account)
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
	s.registration.Identity.Account = s.registration.Identity.Account.MergeMissingFrom(auth.Account)
	return s.registration
}

func refreshStaticHealth(ctx context.Context, state *staticControlState, reporter healthReporter) bool {
	if reporter == nil {
		return false
	}
	health, err := reporter.Health()
	if err != nil || health.Status == "" {
		return false
	}
	before := state.registrationSnapshot().Health
	state.setHealthFromReporter(health)
	after := state.registrationSnapshot().Health
	return before != after
}

func refreshStaticAuth(ctx context.Context, state *staticControlState, reporter authReporter) bool {
	if reporter == nil {
		return false
	}
	auth, err := reporter.Auth()
	if err != nil || auth.Status == "" {
		return false
	}
	before := state.registrationSnapshot().Auth
	state.setAuthFromReporter(auth)
	after := state.registrationSnapshot().Auth
	return before != after
}

func (s *staticControlState) setModels(models []provider.Model) provider.Registration {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.registration.Models = cloneProviderModels(models)
	return s.registration
}

func (s *staticControlState) setTargetVersion(version string) provider.Registration {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.registration.Identity.TargetVersion = strings.TrimSpace(version)
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

func refreshStaticModels(ctx context.Context, state *staticControlState, reporter modelReporter) bool {
	if state == nil || reporter == nil {
		return false
	}
	registration := state.registrationSnapshot()
	current := registration.Models
	if len(current) > 0 && !shouldAugmentConfiguredModels(registration) {
		force, ok := reporter.(forcedModelDiscoveryReporter)
		if !ok || !force.ForceModelDiscovery() {
			return false
		}
	}
	if _, ok := ctx.Deadline(); !ok && defaultModelDiscoveryTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, defaultModelDiscoveryTimeout)
		defer cancel()
	}
	models, err := reporter.Models(ctx)
	if err != nil || len(models) == 0 {
		return false
	}
	merged := mergeDiscoveredModels(current, models)
	if reflect.DeepEqual(current, merged) {
		return false
	}
	state.setModels(merged)
	return true
}

func refreshStaticTargetVersion(ctx context.Context, state *staticControlState, reporter targetVersionReporter) bool {
	if state == nil || reporter == nil {
		return false
	}
	registration := state.registrationSnapshot()
	if _, ok := ctx.Deadline(); !ok && defaultTargetVersionTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, defaultTargetVersionTimeout)
		defer cancel()
	}
	version, err := reporter.TargetVersion(ctx)
	if err != nil {
		return false
	}
	version = strings.TrimSpace(version)
	if version == "" || version == registration.Identity.TargetVersion {
		return false
	}
	state.setTargetVersion(version)
	return true
}

func shouldAugmentConfiguredModels(registration provider.Registration) bool {
	return registration.Identity.Kind == provider.KindAppServer || registration.Identity.Kind == provider.KindSidecar
}

func mergeDiscoveredModels(current []provider.Model, discovered []provider.Model) []provider.Model {
	out := cloneProviderModels(current)
	index := make(map[string]int, len(out))
	for i, model := range out {
		index[model.ID] = i
	}
	for _, model := range discovered {
		if model.ID == "" {
			continue
		}
		if i, ok := index[model.ID]; ok {
			out[i] = mergeDiscoveredModel(out[i], model)
			continue
		}
		index[model.ID] = len(out)
		out = append(out, cloneProviderModels([]provider.Model{model})[0])
	}
	return out
}

func mergeDiscoveredModel(current provider.Model, discovered provider.Model) provider.Model {
	current.Aliases = mergeStrings(current.Aliases, discovered.Aliases)
	current.Capabilities = mergeProviderCapabilities(current.Capabilities, discovered.Capabilities)
	if current.ContextTokens == 0 {
		current.ContextTokens = discovered.ContextTokens
	}
	if current.MaxContextTokens == 0 {
		current.MaxContextTokens = discovered.MaxContextTokens
	}
	if current.MaxOutputTokens == 0 {
		current.MaxOutputTokens = discovered.MaxOutputTokens
	}
	if current.Kind == "" {
		current.Kind = discovered.Kind
	}
	if len(current.GroupMembers) == 0 {
		current.GroupMembers = append([]string(nil), discovered.GroupMembers...)
	} else {
		current.GroupMembers = mergeStrings(current.GroupMembers, discovered.GroupMembers)
	}
	if discovered.Quota != nil {
		current.Quota = cloneModelQuota(discovered.Quota)
	}
	return current
}

func mergeStrings(current []string, discovered []string) []string {
	if len(current) == 0 {
		return append([]string(nil), discovered...)
	}
	seen := make(map[string]struct{}, len(current)+len(discovered))
	out := make([]string, 0, len(current)+len(discovered))
	for _, value := range current {
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	for _, value := range discovered {
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func mergeProviderCapabilities(current []provider.Capability, discovered []provider.Capability) []provider.Capability {
	if len(current) == 0 {
		return append([]provider.Capability(nil), discovered...)
	}
	seen := make(map[provider.Capability]struct{}, len(current)+len(discovered))
	out := make([]provider.Capability, 0, len(current)+len(discovered))
	for _, capability := range current {
		if _, ok := seen[capability]; ok {
			continue
		}
		seen[capability] = struct{}{}
		out = append(out, capability)
	}
	for _, capability := range discovered {
		if _, ok := seen[capability]; ok {
			continue
		}
		seen[capability] = struct{}{}
		out = append(out, capability)
	}
	return out
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

func writeStaticAuthReport(ctx context.Context, client *controlClientConn, state *staticControlState, reporter authSnapshotReporter, id string) error {
	registration := state.registrationSnapshot()
	now := time.Now().UTC()
	snapshot := control.AuthSnapshot{
		ProviderInstanceID: registration.Identity.ProviderInstanceID,
		Auth:               registration.Auth,
		Source:             registration.Auth.SelectedSource,
		ObservedAt:         now,
		ReportedAt:         now,
	}
	if reporter != nil {
		if report, err := reporter.AuthSnapshot(ctx); err == nil {
			snapshot.Raw = append([]byte(nil), report.Raw...)
			snapshot.Fingerprint = report.Fingerprint
			snapshot.Filename = report.Filename
			snapshot.Format = report.Format
		}
	}
	return client.sendAndWaitAck(ctx, control.MessageTypeAuthSnapshot, id, snapshot)
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

func handleStaticControlRequest(ctx context.Context, client *controlClientConn, state *staticControlState, refresher AuthRefresher, reporter authSnapshotReporter, pushApplier authPushApplier, env control.Envelope) error {
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
		if err := client.sendAndWaitAck(ctx, control.MessageTypeAuthRefreshResult, "auth_refresh_result_"+request.RefreshID, result); err != nil {
			return err
		}
		return writeStaticAuthReport(ctx, client, state, reporter, "auth_snapshot_refresh_"+time.Now().UTC().Format("20060102150405.000000000"))
	case control.MessageTypeAuthPush:
		push, err := control.Decode[control.AuthPush](env, control.MessageTypeAuthPush)
		if err != nil {
			return err
		}
		registration := state.registrationSnapshot()
		if push.ProviderInstanceID != registration.Identity.ProviderInstanceID {
			return fmt.Errorf("%w: auth push provider_instance_id does not match this shim", ErrShimConfig)
		}
		if pushApplier != nil && len(push.Raw) > 0 {
			auth, err := pushApplier.ApplyAuthPush(ctx, push, registration)
			if err != nil {
				auth := registration.Auth
				auth.Status = provider.AuthUnavailable
				auth.LastRefreshErr = err.Error()
				state.setAuth(auth)
				return writeStaticAuthReport(ctx, client, state, reporter, "auth_snapshot_push_failed_"+time.Now().UTC().Format("20060102150405.000000000"))
			}
			state.setAuth(auth)
		} else if push.Auth.Status != "" {
			auth := push.Auth
			if auth.Account == (provider.Account{}) {
				auth.Account = registration.Identity.Account
			}
			if auth.SelectedSource == "" && push.Source != "" {
				auth.SelectedSource = push.Source
			}
			state.setAuth(auth)
		}
		return writeStaticAuthReport(ctx, client, state, reporter, "auth_snapshot_push_"+time.Now().UTC().Format("20060102150405.000000000"))
	default:
		return nil
	}
}

func maybeStaticAutoAuthRefresh(ctx context.Context, client *controlClientConn, state *staticControlState, refresher AuthRefresher, reporter authSnapshotReporter, threshold time.Duration, cooldown time.Duration) error {
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
	auth := registration.Auth
	auth.Status = provider.AuthRefreshing
	auth.LastRefreshErr = ""
	state.setAuth(auth)
	if err := writeStaticAuthReport(ctx, client, state, reporter, "auth_snapshot_auto_refreshing_"+now.Format("20060102150405.000000000")); err != nil {
		return err
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

func cloneProviderModels(models []provider.Model) []provider.Model {
	if len(models) == 0 {
		return nil
	}
	out := make([]provider.Model, len(models))
	for i, model := range models {
		out[i] = model
		out[i].Aliases = append([]string(nil), model.Aliases...)
		out[i].Capabilities = append([]provider.Capability(nil), model.Capabilities...)
		out[i].Quota = cloneModelQuota(model.Quota)
	}
	return out
}

func cloneModelQuota(quota *provider.ModelQuota) *provider.ModelQuota {
	if quota == nil {
		return nil
	}
	copied := *quota
	return &copied
}
