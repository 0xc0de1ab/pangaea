package router

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/0xc0de1ab/pangaea/internal/control"
	"github.com/0xc0de1ab/pangaea/internal/provider"
)

const maxAuthEvents = 512

const authEventProviderHeartbeat = "provider.heartbeat.auth"

type authRawMetadata struct {
	Auth               provider.AuthState
	Fingerprint        string
	Source             string
	Filename           string
	Format             string
	ProviderInstanceID string
	ObservedAt         time.Time
	UpdatedAt          time.Time
}

type AuthRecord struct {
	ID                 string              `json:"id"`
	Service            provider.Service    `json:"service"`
	Account            provider.Account    `json:"account,omitempty"`
	Status             provider.AuthStatus `json:"status"`
	ExpiresAt          time.Time           `json:"expires_at,omitempty"`
	Refreshable        bool                `json:"refreshable"`
	LastRefreshAt      time.Time           `json:"last_refresh_at,omitempty"`
	LastRefreshErr     string              `json:"last_refresh_error,omitempty"`
	SelectedSource     string              `json:"selected_source,omitempty"`
	BootstrapSource    string              `json:"bootstrap_source,omitempty"`
	Fingerprint        string              `json:"fingerprint,omitempty"`
	Source             string              `json:"source,omitempty"`
	Filename           string              `json:"filename"`
	Format             string              `json:"format,omitempty"`
	LatestProviderType string              `json:"latest_provider_type,omitempty"`
	ProviderInstanceID string              `json:"provider_instance_id,omitempty"`
	NodeID             string              `json:"node_id,omitempty"`
	HostName           string              `json:"host_name,omitempty"`
	ObservedAt         time.Time           `json:"observed_at,omitempty"`
	ReportedAt         time.Time           `json:"reported_at,omitempty"`
	UpdatedAt          time.Time           `json:"updated_at"`
	HasDownload        bool                `json:"has_download"`
	DownloadURL        string              `json:"download_url,omitempty"`
	Replicas           []AuthReplica       `json:"replicas,omitempty"`
}

type AuthReplica struct {
	ProviderType       string              `json:"provider_type,omitempty"`
	ProviderInstanceID string              `json:"provider_instance_id"`
	NodeID             string              `json:"node_id,omitempty"`
	HostName           string              `json:"host_name,omitempty"`
	Service            provider.Service    `json:"service,omitempty"`
	Account            provider.Account    `json:"account,omitempty"`
	Status             provider.AuthStatus `json:"status,omitempty"`
	Fingerprint        string              `json:"fingerprint,omitempty"`
	Source             string              `json:"source,omitempty"`
	ObservedAt         time.Time           `json:"observed_at,omitempty"`
	UpdatedAt          time.Time           `json:"updated_at"`
	HasDownload        bool                `json:"has_download"`
}

type AuthEvent struct {
	ID                 string              `json:"id"`
	AuthID             string              `json:"auth_id"`
	Type               string              `json:"type"`
	Service            provider.Service    `json:"service,omitempty"`
	Account            provider.Account    `json:"account,omitempty"`
	ProviderType       string              `json:"provider_type,omitempty"`
	ProviderInstanceID string              `json:"provider_instance_id,omitempty"`
	NodeID             string              `json:"node_id,omitempty"`
	HostName           string              `json:"host_name,omitempty"`
	Status             provider.AuthStatus `json:"status,omitempty"`
	Fingerprint        string              `json:"fingerprint,omitempty"`
	Source             string              `json:"source,omitempty"`
	Message            string              `json:"message,omitempty"`
	Details            map[string]string   `json:"details,omitempty"`
	At                 time.Time           `json:"at"`
}

func (e *Engine) RecordProviderAuthReport(providerInstanceID string, auth provider.AuthState, reportedAt time.Time) {
	e.recordAuth(providerInstanceID, auth, "", "", "", nil, "", reportedAt, "provider.auth.report", "", nil)
}

func (e *Engine) RecordProviderAuthHeartbeat(providerInstanceID string, auth provider.AuthState, reportedAt time.Time) {
	if auth.Status == "" {
		return
	}
	e.recordAuth(providerInstanceID, auth, "", "", "", nil, "", reportedAt, authEventProviderHeartbeat, "", nil)
}

func (e *Engine) RecordAuthSnapshot(snapshot control.AuthSnapshot) {
	auth := snapshot.Auth
	if auth.Status == "" {
		auth.Status = provider.AuthUnknown
	}
	if auth.SelectedSource == "" && snapshot.Source != "" {
		auth.SelectedSource = snapshot.Source
	}
	e.recordAuth(snapshot.ProviderInstanceID, auth, snapshot.Fingerprint, snapshot.Source, snapshot.Filename, snapshot.Raw, snapshot.Format, coalesceTime(snapshot.ObservedAt, snapshot.ReportedAt), "auth.snapshot", "provider observed auth snapshot", e.authUsageEventDetails(snapshot.ProviderInstanceID))
}

func (e *Engine) RecordAuthRefreshResult(result control.AuthRefreshResult) {
	pending, _ := e.pendingAuthRefreshRequest(result.RefreshID)
	result = mergeAuthRefreshResultWithRequest(result, pending)
	message := authRefreshResultMessage(result)
	if result.Error != nil {
		message = firstNonEmpty(message, result.Error.Message)
	}
	eventType := "auth.refresh.result"
	if !result.OK {
		eventType = "auth.refresh.failed"
	}
	details := authRefreshResultDetails(result)
	mergeDetails(details, e.authUsageEventDetails(result.ProviderInstanceID))
	e.recordAuth(result.ProviderInstanceID, result.Auth, "", result.Auth.SelectedSource, "", nil, "", result.ReportedAt, eventType, message, details)
}

func (e *Engine) RecordAuthPush(push control.AuthPush, message string) {
	details := map[string]string{
		"trigger":        "automatic",
		"request_method": "router-push",
		"reason":         push.Reason,
	}
	mergeDetails(details, e.authUsageEventDetails(push.ProviderInstanceID))
	e.recordAuth(push.ProviderInstanceID, push.Auth, push.Fingerprint, push.Source, push.Filename, push.Raw, push.Format, time.Now().UTC(), "auth.push.sent", message, details)
}

func mergeAuthRefreshResultWithRequest(result control.AuthRefreshResult, request control.AuthRefreshRequest) control.AuthRefreshResult {
	if result.Reason == "" {
		result.Reason = request.Reason
	}
	result.Metadata = mergeControlAuthRefreshMetadata(request.Metadata, result.Metadata)
	if result.Metadata.Trigger == "" {
		if strings.HasPrefix(result.RefreshID, "auto_refresh_") {
			result.Metadata.Trigger = "automatic"
		} else {
			result.Metadata.Trigger = "manual"
		}
	}
	return result
}

func mergeControlAuthRefreshMetadata(base control.AuthRefreshMetadata, extra control.AuthRefreshMetadata) control.AuthRefreshMetadata {
	if base.Trigger == "" {
		base.Trigger = extra.Trigger
	}
	if base.Initiator == "" {
		base.Initiator = extra.Initiator
	}
	if base.RequestMethod == "" {
		base.RequestMethod = extra.RequestMethod
	}
	if base.ExecutionMethod == "" {
		base.ExecutionMethod = extra.ExecutionMethod
	}
	if base.Command == "" {
		base.Command = extra.Command
	}
	if base.Endpoint == "" {
		base.Endpoint = extra.Endpoint
	}
	return base
}

func authRefreshResultMessage(result control.AuthRefreshResult) string {
	status := "completed"
	if !result.OK {
		status = "failed"
	}
	trigger := firstNonEmpty(result.Metadata.Trigger, "manual")
	method := firstNonEmpty(result.Metadata.ExecutionMethod, result.Metadata.RequestMethod, "unknown-method")
	reason := strings.TrimSpace(result.Reason)
	if reason != "" {
		return fmt.Sprintf("Auth refresh %s via %s %s: %s.", status, trigger, method, reason)
	}
	return fmt.Sprintf("Auth refresh %s via %s %s.", status, trigger, method)
}

func authRefreshResultDetails(result control.AuthRefreshResult) map[string]string {
	details := map[string]string{
		"refresh_id": result.RefreshID,
		"ok":         fmt.Sprintf("%t", result.OK),
		"reason":     result.Reason,
	}
	metadata := result.Metadata
	if metadata.Trigger != "" {
		details["trigger"] = metadata.Trigger
	}
	if metadata.Initiator != "" {
		details["initiator"] = metadata.Initiator
	}
	if metadata.RequestMethod != "" {
		details["request_method"] = metadata.RequestMethod
	}
	if metadata.ExecutionMethod != "" {
		details["execution_method"] = metadata.ExecutionMethod
	}
	if metadata.Command != "" {
		details["command"] = metadata.Command
	}
	if metadata.Endpoint != "" {
		details["endpoint"] = metadata.Endpoint
	}
	if result.Error != nil {
		details["error_code"] = result.Error.Code
		details["error"] = result.Error.Message
	}
	return details
}

func (e *Engine) authUsageEventDetails(providerInstanceID string) map[string]string {
	if e == nil || strings.TrimSpace(providerInstanceID) == "" {
		return nil
	}
	e.usageMu.RLock()
	usage, ok := e.usages[providerInstanceID]
	e.usageMu.RUnlock()
	if !ok {
		return nil
	}
	registration, ok := e.registry.Get(providerInstanceID)
	if !ok {
		return nil
	}
	windows := routerProviderQuotaWindows(registration, usage)
	details := map[string]string{
		"quota_source": usage.Usage.Source,
	}
	if !usage.ReportedAt.IsZero() {
		details["quota_reported_at"] = usage.ReportedAt.UTC().Format(time.RFC3339)
	}
	if !usage.Usage.ObservedAt.IsZero() {
		details["quota_observed_at"] = usage.Usage.ObservedAt.UTC().Format(time.RFC3339)
	}
	if usage.Usage.PlanTier != "" {
		details["quota_plan_tier"] = usage.Usage.PlanTier
	}
	if usage.Usage.Subscription != nil {
		details["quota_subscription"] = firstNonEmpty(usage.Usage.Subscription.Name, usage.Usage.Subscription.Tier, usage.Usage.Subscription.PaidTier, usage.Usage.Subscription.RateLimitTier)
	}
	for i, window := range windows {
		if i >= 6 {
			details["quota_windows_omitted"] = fmt.Sprintf("%d", len(windows)-i)
			break
		}
		prefix := fmt.Sprintf("quota_window_%d", i+1)
		details[prefix] = window.Label
		details[prefix+"_usage"] = describeQuotaWindowUsage(window)
		if !window.ResetAt.IsZero() {
			details[prefix+"_reset_at"] = window.ResetAt.UTC().Format(time.RFC3339)
		}
		if window.Source != "" {
			details[prefix+"_source"] = window.Source
		}
	}
	return details
}

func mergeDetails(dst map[string]string, src map[string]string) {
	if len(src) == 0 {
		return
	}
	if dst == nil {
		return
	}
	for key, value := range src {
		if _, exists := dst[key]; !exists {
			dst[key] = value
		}
	}
}

func (e *Engine) RecordAuthDownload(authID string) {
	e.authMu.Lock()
	defer e.authMu.Unlock()
	record, ok := e.authRecords[authID]
	if !ok {
		return
	}
	e.appendAuthEventLocked(AuthEvent{
		AuthID:             authID,
		Type:               "auth.download",
		Service:            record.Service,
		Account:            record.Account,
		ProviderType:       record.LatestProviderType,
		ProviderInstanceID: record.ProviderInstanceID,
		NodeID:             record.NodeID,
		HostName:           record.HostName,
		Status:             record.Status,
		Fingerprint:        record.Fingerprint,
		Source:             record.Source,
		Message:            "operator downloaded latest auth file",
		At:                 time.Now().UTC(),
	})
}

func (e *Engine) recordAuth(providerInstanceID string, auth provider.AuthState, fingerprint string, source string, filename string, raw []byte, format string, observedAt time.Time, eventType string, message string, details map[string]string) {
	if e == nil || strings.TrimSpace(providerInstanceID) == "" {
		return
	}
	now := time.Now().UTC()
	if observedAt.IsZero() {
		observedAt = now
	}
	registration, ok := e.registry.Get(providerInstanceID)
	if !ok {
		return
	}
	identity := registration.Identity
	account := accountWithFallback(identity.Account, auth.Account)
	auth.Account = account
	if fingerprint == "" && len(raw) > 0 {
		sum := sha256.Sum256(raw)
		fingerprint = hex.EncodeToString(sum[:])
	}
	filename = canonicalAuthFilename(identity.Service, format, filename)
	authID := authRecordID(identity.Service, account, providerInstanceID)
	replica := AuthReplica{
		ProviderType:       identity.ProviderType,
		ProviderInstanceID: providerInstanceID,
		NodeID:             identity.NodeID,
		HostName:           identity.HostName,
		Service:            identity.Service,
		Account:            account,
		Status:             auth.Status,
		Fingerprint:        fingerprint,
		Source:             firstNonEmpty(source, auth.SelectedSource),
		ObservedAt:         observedAt,
		UpdatedAt:          now,
		HasDownload:        len(raw) > 0,
	}

	e.authMu.Lock()
	defer e.authMu.Unlock()
	if e.authRecords == nil {
		e.authRecords = make(map[string]AuthRecord)
	}
	if e.authRaw == nil {
		e.authRaw = make(map[string][]byte)
	}
	if e.authRawMeta == nil {
		e.authRawMeta = make(map[string]authRawMetadata)
	}
	record := e.authRecords[authID]
	previous := record
	if record.ID == "" {
		record.ID = authID
		record.Service = identity.Service
		record.Account = account
		record.Filename = filename
	}
	record.Status = auth.Status
	record.ExpiresAt = auth.ExpiresAt
	record.Refreshable = auth.Refreshable
	record.LastRefreshAt = auth.LastRefreshAt
	record.LastRefreshErr = auth.LastRefreshErr
	record.SelectedSource = auth.SelectedSource
	record.BootstrapSource = auth.BootstrapSource
	record.Fingerprint = firstNonEmpty(fingerprint, record.Fingerprint)
	record.Source = firstNonEmpty(source, auth.SelectedSource, record.Source)
	record.Filename = canonicalAuthFilename(identity.Service, format, firstNonEmpty(filename, record.Filename))
	record.Format = firstNonEmpty(format, record.Format)
	record.LatestProviderType = identity.ProviderType
	record.ProviderInstanceID = providerInstanceID
	record.NodeID = identity.NodeID
	record.HostName = identity.HostName
	record.ObservedAt = observedAt
	record.ReportedAt = observedAt
	record.UpdatedAt = now
	if len(raw) > 0 && shouldStoreAuthRaw(e.authRawMeta[authID], auth, observedAt) {
		record.HasDownload = true
		record.DownloadURL = "/router/v1/auth/" + url.PathEscape(authID) + "/download"
		e.authRaw[authID] = append([]byte(nil), raw...)
		e.authRawMeta[authID] = authRawMetadata{
			Auth:               auth,
			Fingerprint:        fingerprint,
			Source:             firstNonEmpty(source, auth.SelectedSource),
			Filename:           filename,
			Format:             format,
			ProviderInstanceID: providerInstanceID,
			ObservedAt:         observedAt,
			UpdatedAt:          now,
		}
	} else if len(e.authRaw[authID]) > 0 {
		record.HasDownload = true
		record.DownloadURL = "/router/v1/auth/" + url.PathEscape(authID) + "/download"
	}
	record.Replicas = upsertAuthReplica(record.Replicas, replica)
	e.authRecords[authID] = record

	if shouldAppendAuthEvent(previous, record, eventType, message) {
		e.appendAuthEventLocked(AuthEvent{
			AuthID:             authID,
			Type:               eventType,
			Service:            identity.Service,
			Account:            account,
			ProviderType:       identity.ProviderType,
			ProviderInstanceID: providerInstanceID,
			NodeID:             identity.NodeID,
			HostName:           identity.HostName,
			Status:             auth.Status,
			Fingerprint:        fingerprint,
			Source:             firstNonEmpty(source, auth.SelectedSource),
			Message:            message,
			Details:            details,
			At:                 observedAt,
		})
	}
}

func (e *Engine) ScheduleAuthSyncForProvider(providerInstanceID string, reason string) {
	pushes := e.authPushesForProvider(providerInstanceID, reason)
	for _, push := range pushes {
		push := push
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			_ = e.SendAuthPush(ctx, push)
		}()
	}
}

func (e *Engine) authPushesForProvider(providerInstanceID string, reason string) []control.AuthPush {
	if e == nil || e.registry == nil || strings.TrimSpace(providerInstanceID) == "" {
		return nil
	}
	registration, ok := e.registry.Get(providerInstanceID)
	if !ok {
		return nil
	}
	account := accountWithFallback(registration.Identity.Account, registration.Auth.Account)
	authID := authRecordID(registration.Identity.Service, account, providerInstanceID)
	return e.authPushesForRecord(authID, providerInstanceID, reason)
}

func (e *Engine) authPushesForRecord(authID string, _ string, reason string) []control.AuthPush {
	if e == nil || e.registry == nil || strings.TrimSpace(authID) == "" {
		return nil
	}
	e.authMu.RLock()
	record, ok := e.authRecords[authID]
	raw := append([]byte(nil), e.authRaw[authID]...)
	meta := e.authRawMeta[authID]
	replicas := append([]AuthReplica(nil), record.Replicas...)
	e.authMu.RUnlock()
	if !ok || len(raw) == 0 || !authStatusCanBePushed(meta.Auth.Status) {
		return nil
	}
	pushes := []control.AuthPush{}
	for _, replica := range replicas {
		if replica.ProviderInstanceID == "" || replica.ProviderInstanceID == meta.ProviderInstanceID {
			continue
		}
		target, ok := e.registry.Get(replica.ProviderInstanceID)
		if !ok || !hasCapability(target.Capabilities, provider.CapabilityAuthFile) {
			continue
		}
		if target.Health.Status == provider.HealthAuthUpdating || target.Auth.Status == provider.AuthRefreshing {
			continue
		}
		if replica.Fingerprint != "" && meta.Fingerprint != "" &&
			replica.Fingerprint == meta.Fingerprint && authStatusCanBePushed(replica.Status) {
			continue
		}
		auth := meta.Auth
		auth.Account = accountWithFallback(record.Account, auth.Account)
		if auth.SelectedSource == "" {
			auth.SelectedSource = "router"
		}
		pushes = append(pushes, control.AuthPush{
			ProviderInstanceID: replica.ProviderInstanceID,
			AccountID:          record.Account.ID,
			Auth:               auth,
			Fingerprint:        meta.Fingerprint,
			Source:             firstNonEmpty(meta.Source, "router"),
			Filename:           canonicalAuthFilename(record.Service, meta.Format, meta.Filename),
			Format:             meta.Format,
			Raw:                append([]byte(nil), raw...),
			Reason:             firstNonEmpty(reason, "sync latest auth for same service account"),
		})
	}
	return pushes
}

func (e *Engine) AuthRecords() []AuthRecord {
	if e == nil {
		return nil
	}
	e.authMu.RLock()
	defer e.authMu.RUnlock()
	out := make([]AuthRecord, 0, len(e.authRecords))
	for _, record := range e.authRecords {
		record.Filename = canonicalAuthFilename(record.Service, record.Format, record.Filename)
		record.Replicas = append([]AuthReplica(nil), record.Replicas...)
		out = append(out, record)
	}
	sort.Slice(out, func(i, j int) bool {
		a := out[i]
		b := out[j]
		switch {
		case a.Service != b.Service:
			return a.Service < b.Service
		case accountLabel(a.Account) != accountLabel(b.Account):
			return accountLabel(a.Account) < accountLabel(b.Account)
		default:
			return a.ID < b.ID
		}
	})
	return out
}

func shouldStoreAuthRaw(current authRawMetadata, auth provider.AuthState, observedAt time.Time) bool {
	if current.Fingerprint == "" {
		return true
	}
	if !authStatusCanBePushed(auth.Status) {
		return false
	}
	if !authStatusCanBePushed(current.Auth.Status) {
		return true
	}
	if !auth.ExpiresAt.IsZero() && current.Auth.ExpiresAt.IsZero() {
		return true
	}
	if !auth.ExpiresAt.IsZero() && !current.Auth.ExpiresAt.IsZero() && auth.ExpiresAt.After(current.Auth.ExpiresAt) {
		return true
	}
	if auth.ExpiresAt.Equal(current.Auth.ExpiresAt) && observedAt.After(current.ObservedAt) {
		return true
	}
	return false
}

func authStatusCanBePushed(status provider.AuthStatus) bool {
	switch status {
	case provider.AuthHealthy, provider.AuthRefreshSoon:
		return true
	default:
		return false
	}
}

func (e *Engine) AuthEvents(authID string) []AuthEvent {
	if e == nil {
		return nil
	}
	e.authMu.RLock()
	defer e.authMu.RUnlock()
	out := []AuthEvent{}
	for i := len(e.authEvents) - 1; i >= 0; i-- {
		event := e.authEvents[i]
		if authID == "" || event.AuthID == authID {
			out = append(out, event)
		}
	}
	return out
}

func (e *Engine) authEventsInRecordOrder(limit int) []AuthEvent {
	if e == nil {
		return nil
	}
	if limit <= 0 || limit > maxAuthEvents {
		limit = maxAuthEvents
	}
	e.authMu.RLock()
	defer e.authMu.RUnlock()
	start := 0
	if len(e.authEvents) > limit {
		start = len(e.authEvents) - limit
	}
	out := make([]AuthEvent, 0, len(e.authEvents)-start)
	for _, event := range e.authEvents[start:] {
		if len(event.Details) > 0 {
			event.Details = cloneStringMap(event.Details)
		}
		out = append(out, event)
	}
	return out
}

func (e *Engine) AuthDownload(authID string) ([]byte, string, bool) {
	if e == nil {
		return nil, "", false
	}
	e.authMu.RLock()
	defer e.authMu.RUnlock()
	raw := e.authRaw[authID]
	record, ok := e.authRecords[authID]
	if !ok || len(raw) == 0 {
		return nil, "", false
	}
	return append([]byte(nil), raw...), canonicalAuthFilename(record.Service, record.Format, record.Filename), true
}

func (e *Engine) appendAuthEventLocked(event AuthEvent) {
	if event.AuthID == "" || event.Type == "" {
		return
	}
	if event.At.IsZero() {
		event.At = time.Now().UTC()
	}
	if event.ID == "" {
		event.ID = fmt.Sprintf("auth_event_%s_%06d", event.At.UTC().Format("20060102150405.000000000"), len(e.authEvents)+1)
	}
	if len(event.Details) > 0 {
		event.Details = cloneStringMap(event.Details)
	}
	e.authEvents = append(e.authEvents, event)
	if len(e.authEvents) > maxAuthEvents {
		e.authEvents = append([]AuthEvent(nil), e.authEvents[len(e.authEvents)-maxAuthEvents:]...)
	}
}

func upsertAuthReplica(replicas []AuthReplica, replica AuthReplica) []AuthReplica {
	for i := range replicas {
		if replicas[i].ProviderInstanceID == replica.ProviderInstanceID {
			previous := replicas[i]
			if replica.Account == (provider.Account{}) {
				replica.Account = previous.Account
			}
			replica.Fingerprint = firstNonEmpty(replica.Fingerprint, previous.Fingerprint)
			replica.Source = firstNonEmpty(replica.Source, previous.Source)
			if replica.ObservedAt.IsZero() {
				replica.ObservedAt = previous.ObservedAt
			}
			if !replica.HasDownload {
				replica.HasDownload = previous.HasDownload
			}
			replicas[i] = replica
			return replicas
		}
	}
	replicas = append(replicas, replica)
	sort.Slice(replicas, func(i, j int) bool {
		if replicas[i].HostName != replicas[j].HostName {
			return replicas[i].HostName < replicas[j].HostName
		}
		return replicas[i].ProviderInstanceID < replicas[j].ProviderInstanceID
	})
	return replicas
}

func shouldAppendAuthEvent(previous AuthRecord, next AuthRecord, eventType string, message string) bool {
	if eventType == "" {
		return false
	}
	if eventType != authEventProviderHeartbeat {
		return true
	}
	if previous.ID == "" || message != "" {
		return true
	}
	return previous.Status != next.Status ||
		!previous.ExpiresAt.Equal(next.ExpiresAt) ||
		previous.Refreshable != next.Refreshable ||
		!previous.LastRefreshAt.Equal(next.LastRefreshAt) ||
		previous.LastRefreshErr != next.LastRefreshErr ||
		previous.SelectedSource != next.SelectedSource ||
		previous.BootstrapSource != next.BootstrapSource ||
		previous.Fingerprint != next.Fingerprint ||
		previous.Source != next.Source ||
		previous.Account != next.Account
}

func authRecordID(service provider.Service, account provider.Account, fallback string) string {
	key := account.ID
	if key == "" {
		key = account.Display
	}
	if key == "" {
		key = fallback
	}
	sum := sha256.Sum256([]byte(string(service) + ":" + key))
	return string(service) + "-" + sanitizeAuthID(key) + "-" + hex.EncodeToString(sum[:4])
}

func sanitizeAuthID(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	if value == "" {
		return "unknown"
	}
	var b strings.Builder
	lastDash := false
	for _, r := range value {
		ok := unicode.IsLetter(r) || unicode.IsDigit(r) || r == '.' || r == '_' || r == '-'
		if ok {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash {
			b.WriteByte('-')
			lastDash = true
		}
	}
	out := strings.Trim(b.String(), "-._")
	if out == "" {
		out = "unknown"
	}
	if len(out) > 48 {
		out = out[:48]
	}
	return out
}

func authFilename(service provider.Service, format string) string {
	switch {
	case service == provider.ServiceCodex || format == "codex-auth-json-format":
		return "auth.json"
	case service == provider.ServiceClaude || format == "claude-credentials-json-format":
		return ".credentials.json"
	case service == provider.ServiceGemini || format == "gemini-oauth-creds-json-format":
		return "oauth_creds.json"
	case service == provider.ServiceAntigravity:
		return "state.vscdb"
	case format == "github-copilot-apps-json-format":
		return "apps.json"
	case service == provider.ServiceGitHubCopilot || format == "github-copilot-config-json-format":
		return "config.json"
	default:
		return "auth.json"
	}
}

func canonicalAuthFilename(service provider.Service, format string, filename string) string {
	filename = strings.TrimSpace(filename)
	fallback := authFilename(service, format)
	if filename == "" || (filename == "auth.json" && fallback != "auth.json") {
		return fallback
	}
	return filename
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func coalesceTime(values ...time.Time) time.Time {
	for _, value := range values {
		if !value.IsZero() {
			return value
		}
	}
	return time.Time{}
}

func accountLabel(account provider.Account) string {
	if account.Display != "" {
		return account.Display
	}
	return account.ID
}
