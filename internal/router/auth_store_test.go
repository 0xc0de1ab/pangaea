package router

import (
	"testing"
	"time"

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
