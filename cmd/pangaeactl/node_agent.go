package main

import (
	"context"
	"fmt"
	"time"

	"github.com/0xc0de1ab/pangaea/internal/common"
	"github.com/0xc0de1ab/pangaea/internal/control"
	"github.com/0xc0de1ab/pangaea/internal/nodeagent"
	"github.com/spf13/cobra"
)

type nodeAgentRunOptions struct {
	RouterControlURL  string
	NodeID            string
	HostName          string
	HeartbeatInterval time.Duration
	RuntimeKind       string
	RuntimeVersion    string
	RuntimeRootless   bool
}

func newNodeAgentCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "node-agent",
		Short:         common.CLIShortNode,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	cmd.AddCommand(newNodeAgentRunCmd())
	return cmd
}

func newNodeAgentRunCmd() *cobra.Command {
	opts := nodeAgentRunOptions{}
	cmd := &cobra.Command{
		Use:           "run",
		Short:         "connect a node agent to the v2 router control plane",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runNodeAgent(cmd.Context(), opts)
		},
	}
	cmd.Flags().StringVar(&opts.RouterControlURL, "router-control", "", "router control WebSocket URL")
	cmd.Flags().StringVar(&opts.NodeID, "node-id", "", "stable node id; defaults to --host-name or OS host name")
	cmd.Flags().StringVar(&opts.HostName, "host-name", "", "physical host name to report to the router")
	cmd.Flags().DurationVar(&opts.HeartbeatInterval, "heartbeat-interval", 30*time.Second, "node heartbeat interval")
	cmd.Flags().StringVar(&opts.RuntimeKind, "runtime-kind", "docker", "container runtime kind")
	cmd.Flags().StringVar(&opts.RuntimeVersion, "runtime-version", "", "container runtime version")
	cmd.Flags().BoolVar(&opts.RuntimeRootless, "runtime-rootless", false, "report container runtime as rootless")
	return cmd
}

func runNodeAgent(ctx context.Context, opts nodeAgentRunOptions) error {
	if opts.RouterControlURL == "" {
		return fmt.Errorf("--router-control is required")
	}
	return nodeagent.RunControlClient(ctx, nodeagent.ControlClientOptions{
		ControlURL:        opts.RouterControlURL,
		NodeID:            opts.NodeID,
		HostName:          opts.HostName,
		AgentVersion:      version,
		HeartbeatInterval: opts.HeartbeatInterval,
		Runtime: control.RuntimeInfo{
			Kind:     opts.RuntimeKind,
			Version:  opts.RuntimeVersion,
			Rootless: opts.RuntimeRootless,
		},
	})
}
