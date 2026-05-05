package main

import (
	"context"
	"testing"
	"time"
)

func TestRunNodeAgentRequiresRouterControlURL(t *testing.T) {
	err := runNodeAgent(context.Background(), nodeAgentRunOptions{NodeID: "node-a1", HostName: "host-a1"})
	if err == nil {
		t.Fatalf("expected router control url error")
	}
}

func TestNodeAgentRunCommandExists(t *testing.T) {
	cmd := newNodeAgentRunCmd()
	if cmd.Use != "run" {
		t.Fatalf("expected run command, got %q", cmd.Use)
	}
	for _, name := range []string{"router-control", "node-id", "host-name", "heartbeat-interval", "runtime-kind", "runtime-version", "runtime-rootless"} {
		if cmd.Flags().Lookup(name) == nil {
			t.Fatalf("expected %s flag", name)
		}
	}
	if _, err := time.ParseDuration(cmd.Flags().Lookup("heartbeat-interval").DefValue); err != nil {
		t.Fatalf("heartbeat interval default is not a duration: %v", err)
	}
}
