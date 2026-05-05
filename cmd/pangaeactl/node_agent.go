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
	ConfigPath        string
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
	cmd.AddCommand(newNodeAgentBootstrapAuthCmd())
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
	cmd.Flags().StringVar(&opts.ConfigPath, "config", "", "node-agent provider spec YAML path")
	cmd.Flags().StringVar(&opts.RouterControlURL, "router-control", "", "router control WebSocket URL")
	cmd.Flags().StringVar(&opts.NodeID, "node-id", "", "stable node id; defaults to --host-name or OS host name")
	cmd.Flags().StringVar(&opts.HostName, "host-name", "", "physical host name to report to the router")
	cmd.Flags().DurationVar(&opts.HeartbeatInterval, "heartbeat-interval", 30*time.Second, "node heartbeat interval")
	cmd.Flags().StringVar(&opts.RuntimeKind, "runtime-kind", "", "container runtime kind")
	cmd.Flags().StringVar(&opts.RuntimeVersion, "runtime-version", "", "container runtime version")
	cmd.Flags().BoolVar(&opts.RuntimeRootless, "runtime-rootless", false, "report container runtime as rootless")
	return cmd
}

func runNodeAgent(ctx context.Context, opts nodeAgentRunOptions) error {
	if opts.RouterControlURL == "" {
		return fmt.Errorf("--router-control is required")
	}
	if opts.ConfigPath != "" {
		cfg, err := nodeagent.LoadConfigFile(opts.ConfigPath)
		if err != nil {
			return err
		}
		if opts.NodeID == "" {
			opts.NodeID = cfg.Node.ID
		}
		if opts.HostName == "" {
			opts.HostName = cfg.Node.HostName
		}
		if opts.RuntimeKind == "" {
			opts.RuntimeKind = cfg.Runtime.Kind
		}
		if opts.RuntimeVersion == "" {
			opts.RuntimeVersion = cfg.Runtime.Version
		}
		if !opts.RuntimeRootless {
			opts.RuntimeRootless = cfg.Runtime.Rootless
		}
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

type nodeAgentBootstrapAuthOptions struct {
	ConfigPath string
	ProviderID string
}

func newNodeAgentBootstrapAuthCmd() *cobra.Command {
	opts := nodeAgentBootstrapAuthOptions{}
	cmd := &cobra.Command{
		Use:           "bootstrap-auth",
		Short:         "copy configured provider auth into the provider runtime path",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runNodeAgentBootstrapAuth(cmd.Context(), opts)
		},
	}
	cmd.Flags().StringVar(&opts.ConfigPath, "config", "", "node-agent provider spec YAML path")
	cmd.Flags().StringVar(&opts.ProviderID, "provider", "", "provider id to bootstrap")
	return cmd
}

func runNodeAgentBootstrapAuth(ctx context.Context, opts nodeAgentBootstrapAuthOptions) error {
	if opts.ConfigPath == "" {
		return fmt.Errorf("--config is required")
	}
	if opts.ProviderID == "" {
		return fmt.Errorf("--provider is required")
	}
	cfg, err := nodeagent.LoadConfigFile(opts.ConfigPath)
	if err != nil {
		return err
	}
	spec, ok := cfg.ProviderByID(opts.ProviderID)
	if !ok {
		return fmt.Errorf("provider %q not found", opts.ProviderID)
	}
	_, err = nodeagent.BootstrapAuthCopy(ctx, spec.Auth)
	return err
}
