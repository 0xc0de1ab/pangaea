package main

import (
	"context"
	"testing"
	"time"
)

func TestRunProviderShimRequiresRouterControlURL(t *testing.T) {
	err := runProviderShim(context.Background(), providerShimRunOptions{Simulator: true})
	if err == nil {
		t.Fatalf("expected router control url error")
	}
}

func TestRunProviderShimRequiresSimulatorForNow(t *testing.T) {
	err := runProviderShim(context.Background(), providerShimRunOptions{RouterControlURL: "ws://127.0.0.1/unused"})
	if err == nil {
		t.Fatalf("expected provider mode required error")
	}
}

func TestProviderShimRunCommandExists(t *testing.T) {
	cmd := newProviderShimRunCmd()
	if cmd.Use != "run" {
		t.Fatalf("expected run command, got %q", cmd.Use)
	}
	flag := cmd.Flags().Lookup("heartbeat-interval")
	if flag == nil {
		t.Fatalf("expected heartbeat-interval flag")
	}
	if _, err := time.ParseDuration(flag.DefValue); err != nil {
		t.Fatalf("heartbeat interval default is not a duration: %v", err)
	}
	if cmd.Flags().Lookup("router-data") == nil {
		t.Fatalf("expected router-data flag")
	}
	if cmd.Flags().Lookup("stream-token-key") == nil {
		t.Fatalf("expected stream-token-key flag")
	}
	for _, name := range []string{"api-compatible", "provider-id", "provider-instance-id", "node-id", "host-name", "service", "account", "upstream-base-url", "upstream-dialect", "upstream-api-key", "model", "model-alias"} {
		if cmd.Flags().Lookup(name) == nil {
			t.Fatalf("expected %s flag", name)
		}
	}
}

func TestBuildAPICompatibleProvider(t *testing.T) {
	apiProvider, err := buildAPICompatibleProvider(providerShimRunOptions{
		ProviderID:         "deepseek-api",
		ProviderInstanceID: "deepseek-api-0001",
		NodeID:             "node-a1",
		HostName:           "snowbox",
		Service:            "deepseek",
		Account:            "deepseek@example.test",
		UpstreamBaseURL:    "https://api.example.test",
		UpstreamDialect:    "openai",
		Model:              "deepseek-chat",
		ModelAlias:         "deepseek-default",
	})
	if err != nil {
		t.Fatalf("build api-compatible provider: %v", err)
	}
	registration, err := apiProvider.Registration()
	if err != nil {
		t.Fatalf("registration: %v", err)
	}
	if registration.Identity.Kind != "api-compatible" || registration.Identity.Service != "deepseek" {
		t.Fatalf("unexpected registration identity: %#v", registration.Identity)
	}
	if len(registration.Models) != 1 || registration.Models[0].Aliases[0] != "deepseek-default" {
		t.Fatalf("unexpected model registration: %#v", registration.Models)
	}
}

func TestBuildAPICompatibleProviderRequiresFields(t *testing.T) {
	_, err := buildAPICompatibleProvider(providerShimRunOptions{
		ProviderID:         "deepseek-api",
		ProviderInstanceID: "deepseek-api-0001",
		NodeID:             "node-a1",
		HostName:           "snowbox",
		Service:            "deepseek",
		UpstreamDialect:    "openai",
		Model:              "deepseek-chat",
	})
	if err == nil {
		t.Fatalf("expected upstream base url error")
	}
}
