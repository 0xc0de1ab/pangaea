package bridge

import (
	"testing"

	"github.com/google/antigravity-compat-proxy/internal/models"
)

func TestResolveDefaultModelPrefersGemini3Flash(t *testing.T) {
	got := resolveDefaultModel(map[string]models.ModelDetail{
		"gemini-2.5-pro": {Model: "models/gemini-2.5-pro"},
		"gemini-3-flash": {Model: "models/gemini-3-flash"},
	})
	if got != "models/gemini-3-flash" {
		t.Fatalf("expected gemini-3-flash, got %q", got)
	}
}

func TestResolveDefaultModelPrefersGemini3FlashAgent(t *testing.T) {
	got := resolveDefaultModel(map[string]models.ModelDetail{
		"gemini-3-flash-agent": {Model: "MODEL_PLACEHOLDER_M84"},
		"gemini-3.1-pro-low":   {Model: "MODEL_PLACEHOLDER_M36"},
	})
	if got != "MODEL_PLACEHOLDER_M84" {
		t.Fatalf("expected gemini-3-flash-agent, got %q", got)
	}
}

func TestResolveDefaultModelHonorsEnvironment(t *testing.T) {
	t.Setenv("ANTIGRAVITY_DEFAULT_MODEL", "gemini-2.5-pro")
	got := resolveDefaultModel(map[string]models.ModelDetail{
		"gemini-2.5-pro": {Model: "models/gemini-2.5-pro"},
		"gemini-3-flash": {Model: "models/gemini-3-flash"},
	})
	if got != "models/gemini-2.5-pro" {
		t.Fatalf("expected env-selected model, got %q", got)
	}
}

func TestUserVisibleModelsUsesCascadeConfigAsAllowlist(t *testing.T) {
	quota := &models.QuotaInfo{RemainingFraction: 0.8, ResetTime: "2026-05-09T09:22:59Z"}
	got := userVisibleModels(map[string]models.ModelDetail{
		"chat_20706":           {Model: "MODEL_CHAT_20706", QuotaInfo: &models.QuotaInfo{RemainingFraction: 1}},
		"gemini-3-flash-agent": {Model: "MODEL_PLACEHOLDER_M84"},
		"tab_flash_lite":       {Model: "MODEL_PLACEHOLDER_M19", QuotaInfo: &models.QuotaInfo{RemainingFraction: 1}},
	}, []models.ClientModelConfig{
		{
			Label:        "Gemini 3 Flash",
			ModelOrAlias: &models.ModelOrAlias{Model: "MODEL_PLACEHOLDER_M84"},
			QuotaInfo:    quota,
		},
	})

	if len(got) != 1 {
		t.Fatalf("expected only GUI-visible model, got %#v", got)
	}
	model := got["gemini-3-flash-agent"]
	if model.Label != "Gemini 3 Flash" || model.QuotaInfo != quota {
		t.Fatalf("expected cascade label/quota enrichment, got %#v", model)
	}
	if _, ok := got["chat_20706"]; ok {
		t.Fatalf("internal chat model was not filtered: %#v", got)
	}
}
