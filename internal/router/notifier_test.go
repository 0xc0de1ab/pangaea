package router

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/0xc0de1ab/pangaea/internal/notifier/telegram"
	"github.com/0xc0de1ab/pangaea/internal/provider"
	"github.com/0xc0de1ab/pangaea/internal/quota"
)

func TestRenderRouterTelegramSummaryIncludesProviderQuota(t *testing.T) {
	now := time.Date(2026, 5, 11, 1, 0, 0, 0, time.Local)
	reset := time.Date(2026, 5, 11, 3, 0, 0, 0, time.Local)
	modelReset := time.Date(2026, 5, 12, 4, 30, 0, 0, time.Local)

	registry := provider.NewRegistry()
	reg := registration("codex-primary-a1", "codex-cli", "primary@example.test", 10, 0)
	reg.Models = []provider.Model{{
		ID:      "gpt-5.3-codex-spark",
		Aliases: []string{"GPT-5.3 Codex Spark"},
		Quota: &provider.ModelQuota{
			RemainingPct: 42,
			ResetAt:      modelReset,
			Source:       "model quota",
		},
	}}
	if err := registry.Upsert(reg); err != nil {
		t.Fatalf("upsert provider: %v", err)
	}
	engine, err := NewEngine(validPolicy(), registry, quota.NewLedger())
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	if err := engine.UpdateProviderUsage("codex-primary-a1", provider.UsageReport{
		ObservedAt: now,
		Source:     "codex-auth-json-format/usage-probe",
		NativeSummary: map[string]any{
			"windows": []any{
				map[string]any{
					"label":         "5h limit",
					"remaining_pct": 90,
					"reset_at":      reset.Format(time.RFC3339),
				},
			},
		},
	}, now); err != nil {
		t.Fatalf("update usage: %v", err)
	}

	body := renderRouterTelegramSummary(engine, "periodic", now)
	for _, want := range []string{
		"Quota:",
		"- codex node-a1 primary@example.test",
		"5h limit",
		"90% left",
		"reset 05-11 03:00",
		"GPT-5.3 Codex Spark",
		"42% left",
		"reset 05-12 04:30",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("body missing %q\n--- body ---\n%s", want, body)
		}
	}
}

func TestRenderRouterTelegramProviderCommandIncludesListAndQuota(t *testing.T) {
	now := time.Date(2026, 5, 11, 1, 0, 0, 0, time.Local)
	codexReset := time.Date(2026, 5, 11, 6, 0, 0, 0, time.Local)
	geminiReset := time.Date(2026, 5, 11, 2, 30, 0, 0, time.Local)

	registry := provider.NewRegistry()
	codex := registration("codex-primary-a1", "codex-cli", "codex@example.test", 10, 0)
	gemini := registration("gemini-primary-a1", "gemini-cli", "gemini@example.test", 10, 0)
	gemini.Identity.Service = provider.ServiceGemini
	gemini.Models = []provider.Model{{
		ID:      "gemini-3-flash",
		Aliases: []string{"Gemini 3 Flash"},
		Quota: &provider.ModelQuota{
			RemainingPct: 73,
			ResetAt:      geminiReset,
		},
	}}
	for _, reg := range []provider.Registration{codex, gemini} {
		if err := registry.Upsert(reg); err != nil {
			t.Fatalf("upsert provider: %v", err)
		}
	}
	engine, err := NewEngine(validPolicy(), registry, quota.NewLedger())
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	if err := engine.UpdateProviderUsage("codex-primary-a1", provider.UsageReport{
		ObservedAt: now,
		NativeSummary: map[string]any{
			"windows": []any{map[string]any{
				"label":         "5h limit",
				"remaining_pct": 88,
				"reset_at":      codexReset.Format(time.RFC3339),
			}},
		},
	}, now); err != nil {
		t.Fatalf("update usage: %v", err)
	}

	codexBody := renderRouterTelegramCommand(engine, "codex", now)
	for _, want := range []string{
		"Pangaea Providers · Codex",
		"Providers: 1",
		"codex-primary-a1",
		"codex@example.test",
		"5h limit",
		"88% left",
	} {
		if !strings.Contains(codexBody, want) {
			t.Fatalf("codex body missing %q\n--- body ---\n%s", want, codexBody)
		}
	}
	if strings.Contains(codexBody, "gemini-primary-a1") {
		t.Fatalf("codex response should not include gemini provider:\n%s", codexBody)
	}

	geminiBody := renderRouterTelegramCommand(engine, "gemini", now)
	for _, want := range []string{
		"Pangaea Providers · Gemini",
		"gemini-primary-a1",
		"Flash",
		"73% left",
	} {
		if !strings.Contains(geminiBody, want) {
			t.Fatalf("gemini body missing %q\n--- body ---\n%s", want, geminiBody)
		}
	}
	if strings.Contains(geminiBody, "Gemini 3 Flash") {
		t.Fatalf("gemini quota should be rendered as the CLI bucket label, not the model alias:\n%s", geminiBody)
	}
}

func TestRouterProviderQuotaWindowsGroupsGeminiQuotaLikeCLI(t *testing.T) {
	now := time.Date(2026, 5, 11, 1, 0, 0, 0, time.Local)
	reset := now.Add(2 * time.Hour)
	reg := registration("gemini-primary-a1", "gemini-cli", "gemini@example.test", 10, 0)
	reg.Identity.Service = provider.ServiceGemini
	reg.Models = []provider.Model{
		{
			ID:           "auto-gemini-3",
			Aliases:      []string{"Auto (Gemini 3)"},
			Kind:         "group",
			GroupMembers: []string{"gemini-3.1-pro-preview", "gemini-3-flash-preview"},
			Quota:        &provider.ModelQuota{RemainingPct: 41, ResetAt: reset},
		},
		{
			ID:      "gemini-3-flash-preview",
			Aliases: []string{"Gemini 3 Flash"},
			Quota:   &provider.ModelQuota{RemainingPct: 73, ResetAt: reset},
		},
		{
			ID:      "gemini-2.5-flash-lite",
			Aliases: []string{"Gemini 2.5 Flash Lite"},
			Quota:   &provider.ModelQuota{RemainingPct: 81, ResetAt: reset},
		},
		{
			ID:      "gemini-3.1-pro-preview",
			Aliases: []string{"Gemini 3.1 Pro"},
			Quota:   &provider.ModelQuota{RemainingPct: 66, ResetAt: reset},
		},
		{
			ID:      "gemma-3",
			Aliases: []string{"Gemma 3"},
			Quota:   &provider.ModelQuota{RemainingPct: 12, ResetAt: reset},
		},
	}
	usage := ProviderUsageSnapshot{
		Usage: provider.UsageReport{
			NativeSummary: map[string]any{
				"windows": []any{
					map[string]any{"label": "Gemini 3 Flash", "remaining_pct": 70, "reset_at": reset.Format(time.RFC3339)},
					map[string]any{"label": "Gemini 3.1 Pro", "remaining_pct": 60, "reset_at": reset.Format(time.RFC3339)},
				},
			},
		},
	}

	windows := routerProviderQuotaWindows(reg, usage)
	got := make([]string, 0, len(windows))
	for _, window := range windows {
		got = append(got, window.Label)
	}
	want := []string{"Flash", "Flash Lite", "Pro"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("gemini quota labels = %#v, want %#v", got, want)
	}
	for _, window := range windows {
		if strings.Contains(window.Label, "Gemini") || strings.Contains(window.Label, "Auto") || strings.Contains(window.Label, "Gemma") {
			t.Fatalf("gemini quota label should use CLI bucket names, got %#v", windows)
		}
	}
}

func TestRenderRouterTelegramAGAlias(t *testing.T) {
	registry := provider.NewRegistry()
	ag := registration("antigravity-primary-a1", "antigravity-sidecar", "ag@example.test", 10, 0)
	ag.Identity.Service = provider.ServiceAntigravity
	if err := registry.Upsert(ag); err != nil {
		t.Fatalf("upsert provider: %v", err)
	}
	engine, err := NewEngine(validPolicy(), registry, quota.NewLedger())
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}

	body := renderRouterTelegramCommand(engine, "ag", time.Date(2026, 5, 11, 1, 0, 0, 0, time.Local))
	for _, want := range []string{
		"Pangaea Providers · Antigravity",
		"Command: /ag",
		"antigravity-primary-a1",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("ag body missing %q\n--- body ---\n%s", want, body)
		}
	}
}

func TestRouterTelegramCommandUpdateSendsProviderResponse(t *testing.T) {
	var sent telegram.SendMessageRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/sendMessage") {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		_ = json.NewDecoder(r.Body).Decode(&sent)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	registry := provider.NewRegistry()
	if err := registry.Upsert(registration("codex-primary-a1", "codex-cli", "codex@example.test", 10, 0)); err != nil {
		t.Fatalf("upsert provider: %v", err)
	}
	engine, err := NewEngine(validPolicy(), registry, quota.NewLedger())
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	notifier := &routerTelegramNotifier{
		cfg:    RouterTelegramNotifierOptions{Enabled: true, BotToken: "T", ChatID: "100"},
		client: &telegram.Client{BotToken: "T", Endpoint: srv.URL, HTTP: srv.Client()},
	}

	notifier.handleCommandUpdate(context.Background(), engine, telegram.Update{
		UpdateID: 11,
		Message: &telegram.Message{
			MessageID: 7,
			Chat:      telegram.Chat{ID: 100},
			Text:      "/codex",
		},
	})

	if sent.ChatID != "100" || sent.ReplyToMessageID != 7 || sent.ParseMode != "HTML" {
		t.Fatalf("unexpected send request: %#v", sent)
	}
	if !strings.Contains(sent.Text, "Pangaea Providers · Codex") || !strings.Contains(sent.Text, "codex-primary-a1") {
		t.Fatalf("unexpected command response: %s", sent.Text)
	}
	history := engine.NotifierHistory(10)
	if len(history) == 0 || history[0].Type != "command:codex" || history[0].Status != "sent" {
		t.Fatalf("command delivery not recorded: %#v", history)
	}
}
