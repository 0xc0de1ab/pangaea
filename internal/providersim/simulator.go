// Package providersim provides a deterministic in-memory provider simulator
// for router, control-plane, and usage tests.
package providersim

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/0xc0de1ab/pangaea/internal/compat"
	"github.com/0xc0de1ab/pangaea/internal/provider"
)

const (
	defaultStaleHeartbeatAge = 2 * time.Minute
	defaultAuthLifetime      = time.Hour
)

var (
	ErrRegistrationRejected = errors.New("providersim: registration rejected")
	ErrUsageMissing         = errors.New("providersim: usage missing")
	ErrStreamOpenTimeout    = errors.New("providersim: stream open timeout")
	ErrMalformedStream      = errors.New("providersim: malformed stream")
	ErrLocalServerCrash     = errors.New("providersim: local server crash")
)

// Mode identifies the transport shape the simulator is standing in for.
type Mode string

const (
	ModeAPICompatible  Mode = "api-compatible"
	ModeCLILocalServer Mode = "cli-local-server"
	ModeCLIStdio       Mode = "cli-stdio"
	ModeSidecarAgent   Mode = "sidecar-agent"
)

// Valid reports whether m is a known simulator mode.
func (m Mode) Valid() bool {
	switch m {
	case ModeAPICompatible, ModeCLILocalServer, ModeCLIStdio, ModeSidecarAgent:
		return true
	}
	return false
}

func (m Mode) providerKind() provider.Kind {
	switch m {
	case ModeCLILocalServer, ModeCLIStdio:
		return provider.KindCLIContainer
	case ModeSidecarAgent:
		return provider.KindSidecar
	default:
		return provider.KindAPICompatible
	}
}

// Failure identifies deterministic failure modes exposed by the simulator.
type Failure string

const (
	FailureRegistrationRejected Failure = "registration_rejected"
	FailureStaleHeartbeat       Failure = "stale_heartbeat"
	FailureAuthExpired          Failure = "auth_expired"
	FailureStreamOpenTimeout    Failure = "stream_open_timeout"
	FailureMalformedStream      Failure = "malformed_stream"
	FailureUsageMissing         Failure = "usage_missing"
	FailureLocalServerCrash     Failure = "local_server_crash"
)

// Valid reports whether f is a known simulator failure.
func (f Failure) Valid() bool {
	switch f {
	case FailureRegistrationRejected, FailureStaleHeartbeat, FailureAuthExpired,
		FailureStreamOpenTimeout, FailureMalformedStream, FailureUsageMissing,
		FailureLocalServerCrash:
		return true
	}
	return false
}

// Error returns the canonical error associated with f, when f maps to one.
func (f Failure) Error() error {
	switch f {
	case FailureRegistrationRejected:
		return ErrRegistrationRejected
	case FailureStreamOpenTimeout:
		return ErrStreamOpenTimeout
	case FailureMalformedStream:
		return ErrMalformedStream
	case FailureUsageMissing:
		return ErrUsageMissing
	case FailureLocalServerCrash:
		return ErrLocalServerCrash
	default:
		return nil
	}
}

// FailureOptions configures deterministic failures at simulator construction.
type FailureOptions struct {
	Enabled           []Failure
	StaleHeartbeatAge time.Duration
}

// NewFailureOptions returns FailureOptions with the supplied failures enabled.
func NewFailureOptions(failures ...Failure) FailureOptions {
	return FailureOptions{Enabled: append([]Failure(nil), failures...)}
}

// Clock supplies deterministic time to the simulator.
type Clock interface {
	Now() time.Time
}

// ClockFunc adapts a function to Clock.
type ClockFunc func() time.Time

// Now returns the current time from f.
func (f ClockFunc) Now() time.Time {
	if f == nil {
		return time.Now()
	}
	return f()
}

type realClock struct{}

func (realClock) Now() time.Time { return time.Now() }

// ManualClock is a deterministic mutable clock for tests.
type ManualClock struct {
	mu  sync.Mutex
	now time.Time
}

// NewManualClock returns a ManualClock initialized to now.
func NewManualClock(now time.Time) *ManualClock {
	return &ManualClock{now: now}
}

// Now returns the clock's current time.
func (c *ManualClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

// Set moves the clock to now.
func (c *ManualClock) Set(now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = now
}

// Advance moves the clock forward by d.
func (c *ManualClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// Options configures a Simulator.
type Options struct {
	Mode         Mode
	Registration provider.Registration
	Usage        provider.UsageReport
	Failures     FailureOptions
	Clock        Clock
}

// HeartbeatReport is a simulator heartbeat snapshot.
type HeartbeatReport struct {
	Identity   provider.ProviderIdentity `json:"identity"`
	Health     provider.Health           `json:"health"`
	Limits     provider.LimitState       `json:"limits,omitempty"`
	ReportedAt time.Time                 `json:"reported_at"`
}

// InventoryReport is a simulator inventory snapshot.
type InventoryReport struct {
	Identity     provider.ProviderIdentity `json:"identity"`
	Capabilities []provider.Capability     `json:"capabilities,omitempty"`
	Models       []provider.Model          `json:"models,omitempty"`
	ReportedAt   time.Time                 `json:"reported_at"`
}

// AuthReport is a simulator auth snapshot.
type AuthReport struct {
	Identity   provider.ProviderIdentity `json:"identity"`
	Auth       provider.AuthState        `json:"auth"`
	ReportedAt time.Time                 `json:"reported_at"`
}

// UsageReport is a simulator usage snapshot.
type UsageReport struct {
	Identity   provider.ProviderIdentity `json:"identity"`
	Usage      provider.UsageReport      `json:"usage"`
	ReportedAt time.Time                 `json:"reported_at"`
}

// Simulator is a deterministic in-memory provider simulator.
type Simulator struct {
	mu           sync.RWMutex
	clock        Clock
	mode         Mode
	registration provider.Registration
	usage        provider.UsageReport
	failures     map[Failure]bool
	staleAge     time.Duration
}

// New returns a simulator configured from opts.
func New(opts Options) (*Simulator, error) {
	clock := opts.Clock
	if clock == nil {
		clock = realClock{}
	}

	mode := opts.Mode
	if mode == "" {
		mode = ModeAPICompatible
	}
	if !mode.Valid() {
		return nil, fmt.Errorf("providersim: invalid mode %q", mode)
	}

	now := clock.Now()
	registration := opts.Registration
	if isZeroRegistration(registration) {
		registration = defaultRegistration(mode, now)
	} else {
		registration = normalizeRegistration(registration, mode, now)
	}
	if err := registration.Validate(); err != nil {
		return nil, fmt.Errorf("providersim: invalid registration: %w", err)
	}

	usage := opts.Usage
	if usage == (provider.UsageReport{}) {
		usage = defaultUsage(now)
	} else if usage.ObservedAt.IsZero() {
		usage.ObservedAt = now
	}
	if usage.Source == "" {
		usage.Source = "providersim"
	}

	failures := make(map[Failure]bool, len(opts.Failures.Enabled))
	for _, failure := range opts.Failures.Enabled {
		if !failure.Valid() {
			return nil, fmt.Errorf("providersim: invalid failure %q", failure)
		}
		failures[failure] = true
	}

	staleAge := opts.Failures.StaleHeartbeatAge
	if staleAge <= 0 {
		staleAge = defaultStaleHeartbeatAge
	}

	return &Simulator{
		clock:        clock,
		mode:         mode,
		registration: cloneRegistration(registration),
		usage:        usage,
		failures:     failures,
		staleAge:     staleAge,
	}, nil
}

// MustNew returns a simulator or panics if opts are invalid.
func MustNew(opts Options) *Simulator {
	sim, err := New(opts)
	if err != nil {
		panic(err)
	}
	return sim
}

// Mode returns the configured simulator mode.
func (s *Simulator) Mode() Mode {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.mode
}

// Registration returns the provider registration unless registration rejection
// has been injected.
func (s *Simulator) Registration() (provider.Registration, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.failures[FailureRegistrationRejected] {
		return provider.Registration{}, ErrRegistrationRejected
	}
	return s.effectiveRegistrationLocked(s.clock.Now()), nil
}

func (s *Simulator) Invoke(_ context.Context, registration provider.Registration, request compat.Request) (compat.Response, error) {
	if err := request.Validate(); err != nil {
		return compat.Response{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.failures[FailureStreamOpenTimeout] {
		return compat.Response{}, ErrStreamOpenTimeout
	}
	if s.failures[FailureMalformedStream] {
		return compat.Response{}, ErrMalformedStream
	}
	if s.failures[FailureLocalServerCrash] {
		return compat.Response{}, ErrLocalServerCrash
	}
	if registration.Identity.ProviderInstanceID != s.registration.Identity.ProviderInstanceID {
		return compat.Response{}, fmt.Errorf("providersim: provider instance mismatch")
	}

	inputTokens := int64(0)
	lastUserText := ""
	for _, message := range request.Messages {
		for _, part := range message.Content {
			inputTokens += int64(len(part.Text) / 4)
			if message.Role == compat.MessageRoleUser && part.Text != "" {
				lastUserText = part.Text
			}
		}
	}
	if inputTokens == 0 {
		inputTokens = 1
	}
	text := "providersim: ok"
	if lastUserText != "" {
		text = "providersim: " + lastUserText
	}
	outputTokens := int64(len(text)/4 + 1)
	return compat.Response{
		ID:      "providersim-response",
		Dialect: request.Dialect,
		Model:   request.Model,
		Message: compat.Message{
			Role:    compat.MessageRoleAssistant,
			Content: []compat.ContentPart{{Type: compat.ContentPartText, Text: text}},
		},
		StopReason: "stop",
		Usage: compat.Usage{
			InputTokens:  inputTokens,
			OutputTokens: outputTokens,
			TotalTokens:  inputTokens + outputTokens,
		},
	}, nil
}

func (s *Simulator) InvokeStream(ctx context.Context, registration provider.Registration, request compat.Request, emit func(compat.Event) error) (compat.Response, error) {
	response, err := s.Invoke(ctx, registration, request)
	if err != nil {
		return compat.Response{}, err
	}
	events, err := compat.EventsFromResponse(response)
	if err != nil {
		return compat.Response{}, err
	}
	for _, event := range events {
		if ctx.Err() != nil {
			return compat.Response{}, ctx.Err()
		}
		if err := emit(event); err != nil {
			return compat.Response{}, err
		}
	}
	return response, nil
}

// Heartbeat returns the current heartbeat snapshot.
func (s *Simulator) Heartbeat() HeartbeatReport {
	s.mu.RLock()
	defer s.mu.RUnlock()

	now := s.clock.Now()
	reportedAt := now
	if s.failures[FailureStaleHeartbeat] {
		reportedAt = now.Add(-s.staleAge)
	}

	health := s.effectiveHealthLocked(reportedAt)
	return HeartbeatReport{
		Identity:   s.registration.Identity,
		Health:     health,
		Limits:     s.registration.Limits,
		ReportedAt: reportedAt,
	}
}

// Inventory returns the current inventory snapshot.
func (s *Simulator) Inventory() InventoryReport {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return InventoryReport{
		Identity:     s.registration.Identity,
		Capabilities: append([]provider.Capability(nil), s.registration.Capabilities...),
		Models:       cloneModels(s.registration.Models),
		ReportedAt:   s.clock.Now(),
	}
}

// Auth returns the current auth snapshot.
func (s *Simulator) Auth() AuthReport {
	s.mu.RLock()
	defer s.mu.RUnlock()

	now := s.clock.Now()
	return AuthReport{
		Identity:   s.registration.Identity,
		Auth:       s.effectiveAuthLocked(now),
		ReportedAt: now,
	}
}

// Usage returns the current usage snapshot unless usage-missing has been
// injected.
func (s *Simulator) Usage() (UsageReport, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.failures[FailureUsageMissing] {
		return UsageReport{}, ErrUsageMissing
	}

	now := s.clock.Now()
	usage := s.usage
	if usage.ObservedAt.IsZero() {
		usage.ObservedAt = now
	}
	return UsageReport{
		Identity:   s.registration.Identity,
		Usage:      usage,
		ReportedAt: now,
	}, nil
}

// SetHealth replaces the current health status and stamps it with the clock.
func (s *Simulator) SetHealth(status provider.HealthStatus, reason string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.registration.Health = provider.Health{
		Status:    status,
		Reason:    reason,
		CheckedAt: s.clock.Now(),
	}
}

// SetHealthState replaces the current health state.
func (s *Simulator) SetHealthState(health provider.Health) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if health.CheckedAt.IsZero() {
		health.CheckedAt = s.clock.Now()
	}
	s.registration.Health = health
}

// SetAuthState replaces the current auth state.
func (s *Simulator) SetAuthState(auth provider.AuthState) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if auth.Account == (provider.Account{}) {
		auth.Account = s.registration.Identity.Account
	}
	s.registration.Auth = auth
}

// SetAuthStatus changes the auth status and stamps status-specific times.
func (s *Simulator) SetAuthStatus(status provider.AuthStatus) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := s.clock.Now()
	auth := s.registration.Auth
	auth.Status = status
	if auth.Account == (provider.Account{}) {
		auth.Account = s.registration.Identity.Account
	}
	switch status {
	case provider.AuthExpired, provider.AuthRevoked, provider.AuthUnavailable:
		auth.ExpiresAt = now
	case provider.AuthHealthy:
		auth.ExpiresAt = now.Add(defaultAuthLifetime)
		auth.LastRefreshAt = now
		auth.LastRefreshErr = ""
	}
	s.registration.Auth = auth
}

// ExpireAuth marks auth as expired and stores reason as the last refresh error.
func (s *Simulator) ExpireAuth(reason string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := s.clock.Now()
	auth := s.registration.Auth
	auth.Status = provider.AuthExpired
	auth.ExpiresAt = now
	auth.LastRefreshErr = reason
	if auth.Account == (provider.Account{}) {
		auth.Account = s.registration.Identity.Account
	}
	s.registration.Auth = auth
}

// SetUsage replaces the current usage report.
func (s *Simulator) SetUsage(usage provider.UsageReport) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if usage.ObservedAt.IsZero() {
		usage.ObservedAt = s.clock.Now()
	}
	if usage.Source == "" {
		usage.Source = "providersim"
	}
	s.usage = usage
}

// AddUsage increments usage counters and stamps the report with the clock.
func (s *Simulator) AddUsage(requests, inputTokens, outputTokens int64) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.usage.Requests += requests
	s.usage.InputTokens += inputTokens
	s.usage.OutputTokens += outputTokens
	s.usage.TotalTokens = s.usage.InputTokens + s.usage.OutputTokens
	s.usage.ObservedAt = s.clock.Now()
	if s.usage.Source == "" {
		s.usage.Source = "providersim"
	}
}

// SetInventory replaces capabilities and models on the registration snapshot.
func (s *Simulator) SetInventory(capabilities []provider.Capability, models []provider.Model) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.registration.Capabilities = append([]provider.Capability(nil), capabilities...)
	s.registration.Models = cloneModels(models)
}

// SetFailure enables or disables a deterministic failure.
func (s *Simulator) SetFailure(failure Failure, enabled bool) error {
	if !failure.Valid() {
		return fmt.Errorf("providersim: invalid failure %q", failure)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.failures[failure] = enabled
	return nil
}

// FailureEnabled reports whether a deterministic failure is enabled.
func (s *Simulator) FailureEnabled(failure Failure) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.failures[failure]
}

// StreamError returns the deterministic stream error implied by stream failures.
func (s *Simulator) StreamError() error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	switch {
	case s.failures[FailureStreamOpenTimeout]:
		return ErrStreamOpenTimeout
	case s.failures[FailureMalformedStream]:
		return ErrMalformedStream
	default:
		return nil
	}
}

func (s *Simulator) effectiveRegistrationLocked(now time.Time) provider.Registration {
	registration := cloneRegistration(s.registration)
	registration.Health = s.effectiveHealthLocked(now)
	registration.Auth = s.effectiveAuthLocked(now)
	return registration
}

func (s *Simulator) effectiveHealthLocked(now time.Time) provider.Health {
	health := s.registration.Health
	if s.failures[FailureLocalServerCrash] {
		health.Status = provider.HealthDown
		health.Reason = "local server crashed"
	}
	if health.CheckedAt.IsZero() {
		health.CheckedAt = now
	}
	return health
}

func (s *Simulator) effectiveAuthLocked(now time.Time) provider.AuthState {
	auth := s.registration.Auth
	if auth.Account == (provider.Account{}) {
		auth.Account = s.registration.Identity.Account
	}
	if s.failures[FailureAuthExpired] {
		auth.Status = provider.AuthExpired
		auth.ExpiresAt = now
		if auth.LastRefreshErr == "" {
			auth.LastRefreshErr = "auth expired failure injected"
		}
	}
	return auth
}

func defaultRegistration(mode Mode, now time.Time) provider.Registration {
	account := provider.Account{ID: "acct-providersim", Display: "providersim@example.test"}
	return provider.Registration{
		Identity: provider.ProviderIdentity{
			ProviderType:       "providersim-openai",
			ProviderInstanceID: "providersim-openai-0001",
			NodeID:             "providersim-node",
			HostName:           "providersim-host",
			Service:            provider.ServiceOpenAI,
			Kind:               mode.providerKind(),
			Account:            account,
		},
		Capabilities: []provider.Capability{
			provider.CapabilityOpenAIChat,
			provider.CapabilityStreamSSE,
			provider.CapabilityUsageRead,
			provider.CapabilityModelsRead,
			provider.CapabilityAuthRefreshOneshot,
		},
		Models: []provider.Model{{
			ID: "gpt-5-sim",
			Aliases: []string{
				"providersim-default",
			},
			Capabilities: []provider.Capability{
				provider.CapabilityOpenAIChat,
				provider.CapabilityStreamSSE,
			},
			ContextTokens:    128000,
			MaxContextTokens: 128000,
		}},
		Health: provider.Health{
			Status:    provider.HealthReady,
			CheckedAt: now,
		},
		Auth: provider.AuthState{
			Status:         provider.AuthHealthy,
			Account:        account,
			ExpiresAt:      now.Add(defaultAuthLifetime),
			Refreshable:    true,
			LastRefreshAt:  now,
			SelectedSource: "providersim",
		},
		Limits: provider.LimitState{
			MaxConcurrency: 8,
			QueueDepth:     0,
			ActiveStreams:  0,
		},
		RegisteredAt: now,
	}
}

func normalizeRegistration(registration provider.Registration, mode Mode, now time.Time) provider.Registration {
	registration = cloneRegistration(registration)
	if registration.Identity.Kind == "" {
		registration.Identity.Kind = mode.providerKind()
	}
	if registration.Health.Status == "" {
		registration.Health.Status = provider.HealthReady
	}
	if registration.Health.CheckedAt.IsZero() {
		registration.Health.CheckedAt = now
	}
	if registration.Auth.Status == "" {
		registration.Auth.Status = provider.AuthHealthy
	}
	if registration.Auth.Account == (provider.Account{}) {
		registration.Auth.Account = registration.Identity.Account
	}
	if registration.Auth.ExpiresAt.IsZero() {
		registration.Auth.ExpiresAt = now.Add(defaultAuthLifetime)
	}
	if registration.RegisteredAt.IsZero() {
		registration.RegisteredAt = now
	}
	return registration
}

func defaultUsage(now time.Time) provider.UsageReport {
	return provider.UsageReport{
		ObservedAt:   now,
		Source:       "providersim",
		Requests:     1,
		InputTokens:  32,
		OutputTokens: 64,
		TotalTokens:  96,
	}
}

func isZeroRegistration(registration provider.Registration) bool {
	return registration.Identity == (provider.ProviderIdentity{}) &&
		len(registration.Capabilities) == 0 &&
		len(registration.Models) == 0 &&
		registration.Health == (provider.Health{}) &&
		registration.Auth == (provider.AuthState{}) &&
		registration.Limits == (provider.LimitState{}) &&
		registration.RegisteredAt.IsZero()
}

func cloneRegistration(registration provider.Registration) provider.Registration {
	registration.Capabilities = append([]provider.Capability(nil), registration.Capabilities...)
	registration.Models = cloneModels(registration.Models)
	return registration
}

func cloneModels(models []provider.Model) []provider.Model {
	out := make([]provider.Model, len(models))
	for i, model := range models {
		out[i] = model
		out[i].Aliases = append([]string(nil), model.Aliases...)
		out[i].Capabilities = append([]provider.Capability(nil), model.Capabilities...)
	}
	return out
}
