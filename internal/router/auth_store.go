package router

import (
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
	At                 time.Time           `json:"at"`
}

func (e *Engine) RecordProviderAuthReport(providerInstanceID string, auth provider.AuthState, reportedAt time.Time) {
	e.recordAuth(providerInstanceID, auth, "", "", "", nil, "", reportedAt, "provider.auth.report", "")
}

func (e *Engine) RecordProviderAuthHeartbeat(providerInstanceID string, auth provider.AuthState, reportedAt time.Time) {
	if auth.Status == "" {
		return
	}
	e.recordAuth(providerInstanceID, auth, "", "", "", nil, "", reportedAt, authEventProviderHeartbeat, "")
}

func (e *Engine) RecordAuthSnapshot(snapshot control.AuthSnapshot) {
	auth := snapshot.Auth
	if auth.Status == "" {
		auth.Status = provider.AuthUnknown
	}
	if auth.SelectedSource == "" && snapshot.Source != "" {
		auth.SelectedSource = snapshot.Source
	}
	e.recordAuth(snapshot.ProviderInstanceID, auth, snapshot.Fingerprint, snapshot.Source, snapshot.Filename, snapshot.Raw, snapshot.Format, coalesceTime(snapshot.ObservedAt, snapshot.ReportedAt), "auth.snapshot", "provider observed auth snapshot")
}

func (e *Engine) RecordAuthRefreshResult(result control.AuthRefreshResult) {
	message := ""
	if result.Error != nil {
		message = result.Error.Message
	}
	eventType := "auth.refresh.result"
	if !result.OK {
		eventType = "auth.refresh.failed"
	}
	e.recordAuth(result.ProviderInstanceID, result.Auth, "", result.Auth.SelectedSource, "", nil, "", result.ReportedAt, eventType, message)
}

func (e *Engine) RecordAuthPush(push control.AuthPush, message string) {
	e.recordAuth(push.ProviderInstanceID, push.Auth, push.Fingerprint, push.Source, push.Filename, push.Raw, push.Format, time.Now().UTC(), "auth.push.sent", message)
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

func (e *Engine) recordAuth(providerInstanceID string, auth provider.AuthState, fingerprint string, source string, filename string, raw []byte, format string, observedAt time.Time, eventType string, message string) {
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
	if filename == "" {
		filename = authFilename(identity.Service, format)
	}
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
	record.Filename = firstNonEmpty(filename, record.Filename, authFilename(identity.Service, format))
	record.Format = firstNonEmpty(format, record.Format)
	record.LatestProviderType = identity.ProviderType
	record.ProviderInstanceID = providerInstanceID
	record.NodeID = identity.NodeID
	record.HostName = identity.HostName
	record.ObservedAt = observedAt
	record.ReportedAt = observedAt
	record.UpdatedAt = now
	if len(raw) > 0 {
		record.HasDownload = true
		record.DownloadURL = "/router/v1/auth/" + url.PathEscape(authID) + "/download"
		e.authRaw[authID] = append([]byte(nil), raw...)
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
			At:                 observedAt,
		})
	}
}

func (e *Engine) AuthRecords() []AuthRecord {
	if e == nil {
		return nil
	}
	e.authMu.RLock()
	defer e.authMu.RUnlock()
	out := make([]AuthRecord, 0, len(e.authRecords))
	for _, record := range e.authRecords {
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
	return append([]byte(nil), raw...), firstNonEmpty(record.Filename, authFilename(record.Service, record.Format)), true
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
	default:
		return "auth.json"
	}
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
