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
		t.Fatalf("expected simulator required error")
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
}
