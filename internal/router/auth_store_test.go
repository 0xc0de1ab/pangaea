package router

import (
	"bytes"
	"testing"
	"time"

	"github.com/0xc0de1ab/pangaea/internal/control"
	"github.com/0xc0de1ab/pangaea/internal/provider"
	"github.com/0xc0de1ab/pangaea/internal/quota"
)

func TestAuthDownloadUsesGitHubCopilotConfigFilename(t *testing.T) {
	registry := provider.NewRegistry()
	reg := registration("github-copilot-octocat", "github-copilot-sidecar", "octocat", 10, 0)
	reg.Identity.Service = provider.ServiceGitHubCopilot
	reg.Identity.Kind = provider.KindSidecar
	if err := registry.Upsert(reg); err != nil {
		t.Fatalf("upsert provider: %v", err)
	}
	engine, err := NewEngine(validPolicy(), registry, quota.NewLedger())
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}

	engine.recordAuth(
		reg.Identity.ProviderInstanceID,
		provider.AuthState{
			Status:  provider.AuthHealthy,
			Account: provider.Account{ID: "octocat", Display: "octocat"},
		},
		"",
		"container",
		"",
		[]byte(`{"copilotTokens":{"https://github.com:octocat":"redacted"}}`),
		"github-copilot-config-json-format",
		time.Now().UTC(),
		"auth.snapshot",
		"copilot auth snapshot",
	)

	records := engine.AuthRecords()
	if len(records) != 1 {
		t.Fatalf("auth records len = %d, want 1: %#v", len(records), records)
	}
	if records[0].Filename != "config.json" {
		t.Fatalf("auth record filename = %q, want config.json", records[0].Filename)
	}
	_, filename, ok := engine.AuthDownload(records[0].ID)
	if !ok {
		t.Fatalf("auth download not available")
	}
	if filename != "config.json" {
		t.Fatalf("download filename = %q, want config.json", filename)
	}
}

func TestAuthRecordsNormalizeGenericGitHubCopilotFilename(t *testing.T) {
	registry := provider.NewRegistry()
	reg := registration("github-copilot-octocat", "github-copilot-sidecar", "octocat", 10, 0)
	reg.Identity.Service = provider.ServiceGitHubCopilot
	reg.Identity.Kind = provider.KindSidecar
	if err := registry.Upsert(reg); err != nil {
		t.Fatalf("upsert provider: %v", err)
	}
	engine, err := NewEngine(validPolicy(), registry, quota.NewLedger())
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}

	engine.recordAuth(
		reg.Identity.ProviderInstanceID,
		provider.AuthState{
			Status:  provider.AuthHealthy,
			Account: provider.Account{ID: "octocat", Display: "octocat"},
		},
		"",
		"container",
		"auth.json",
		[]byte(`{"copilotTokens":{"https://github.com:octocat":"redacted"}}`),
		"github-copilot-config-json-format",
		time.Now().UTC(),
		"auth.snapshot",
		"copilot auth snapshot",
	)

	records := engine.AuthRecords()
	if len(records) != 1 {
		t.Fatalf("auth records len = %d, want 1: %#v", len(records), records)
	}
	if records[0].Filename != "config.json" {
		t.Fatalf("auth record filename = %q, want config.json", records[0].Filename)
	}
	_, filename, ok := engine.AuthDownload(records[0].ID)
	if !ok {
		t.Fatalf("auth download not available")
	}
	if filename != "config.json" {
		t.Fatalf("download filename = %q, want config.json", filename)
	}
}

func TestAuthSyncPushesLatestUsableRawToStaleReplica(t *testing.T) {
	registry := provider.NewRegistry()
	account := "7001e8a6-c42a-4b6c-8a17-d8c00abd7c99"
	freshReg := registration("codex-rpi5", "codex-cli", account, 10, 0)
	freshReg.Identity.NodeID = "rpi5"
	freshReg.Identity.HostName = "rpi5"
	freshReg.Capabilities = append(freshReg.Capabilities, provider.CapabilityAuthFile)
	staleReg := registration("codex-a1", "codex-cli", account, 10, 0)
	staleReg.Identity.NodeID = "a1"
	staleReg.Identity.HostName = "a1"
	staleReg.Capabilities = append(staleReg.Capabilities, provider.CapabilityAuthFile)
	if err := registry.Upsert(freshReg); err != nil {
		t.Fatalf("upsert fresh provider: %v", err)
	}
	if err := registry.Upsert(staleReg); err != nil {
		t.Fatalf("upsert stale provider: %v", err)
	}
	engine, err := NewEngine(validPolicy(), registry, quota.NewLedger())
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	now := time.Now().UTC()
	accountState := provider.Account{ID: account, Display: "nopcode@naver.com"}
	freshRaw := []byte(`{"token":"fresh"}`)
	staleRaw := []byte(`{"token":"expired"}`)

	engine.RecordAuthSnapshot(control.AuthSnapshot{
		ProviderInstanceID: freshReg.Identity.ProviderInstanceID,
		Auth: provider.AuthState{
			Status:      provider.AuthHealthy,
			Account:     accountState,
			ExpiresAt:   now.Add(10 * 24 * time.Hour),
			Refreshable: true,
		},
		Fingerprint: "fresh-fingerprint",
		Source:      "rpi5-container",
		Filename:    "auth.json",
		Format:      "codex-auth-json-format",
		Raw:         freshRaw,
		ObservedAt:  now,
		ReportedAt:  now,
	})
	engine.RecordAuthSnapshot(control.AuthSnapshot{
		ProviderInstanceID: staleReg.Identity.ProviderInstanceID,
		Auth: provider.AuthState{
			Status:      provider.AuthExpired,
			Account:     accountState,
			ExpiresAt:   now.Add(-time.Hour),
			Refreshable: true,
		},
		Fingerprint: "expired-fingerprint",
		Source:      "a1-host",
		Filename:    "auth.json",
		Format:      "codex-auth-json-format",
		Raw:         staleRaw,
		ObservedAt:  now.Add(time.Second),
		ReportedAt:  now.Add(time.Second),
	})

	pushes := engine.authPushesForProvider(staleReg.Identity.ProviderInstanceID, "test")
	if len(pushes) != 1 {
		t.Fatalf("auth pushes len = %d, want 1: %#v", len(pushes), pushes)
	}
	push := pushes[0]
	if push.ProviderInstanceID != staleReg.Identity.ProviderInstanceID {
		t.Fatalf("push target = %q, want %q", push.ProviderInstanceID, staleReg.Identity.ProviderInstanceID)
	}
	if push.Fingerprint != "fresh-fingerprint" {
		t.Fatalf("push fingerprint = %q, want fresh-fingerprint", push.Fingerprint)
	}
	if !bytes.Equal(push.Raw, freshRaw) {
		t.Fatalf("push raw = %s, want %s", push.Raw, freshRaw)
	}

	records := engine.AuthRecords()
	if len(records) != 1 {
		t.Fatalf("auth records len = %d, want 1: %#v", len(records), records)
	}
	download, _, ok := engine.AuthDownload(records[0].ID)
	if !ok {
		t.Fatalf("auth download not available")
	}
	if !bytes.Equal(download, freshRaw) {
		t.Fatalf("download raw = %s, want %s", download, freshRaw)
	}
}
