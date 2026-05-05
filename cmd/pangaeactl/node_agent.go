package main

import (
	"context"
	"fmt"
	"time"

	"github.com/0xc0de1ab/pangaea/internal/common"
	"github.com/0xc0de1ab/pangaea/internal/control"
	"github.com/0xc0de1ab/pangaea/internal/nodeagent"
	containerruntime "github.com/0xc0de1ab/pangaea/internal/runtime"
	"github.com/spf13/cobra"
)

type nodeAgentRunOptions struct {
	ConfigPath          string
	RouterControlURL    string
	NodeID              string
	HostName            string
	HeartbeatInterval   time.Duration
	RuntimeKind         string
	RuntimeVersion      string
	RuntimeRootless     bool
	ReconcileContainers bool
	ReconcileInterval   time.Duration
	DockerBin           string
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
	cmd.AddCommand(newNodeAgentReconcileProviderCmd())
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
	cmd.Flags().BoolVar(&opts.ReconcileContainers, "reconcile-containers", false, "periodically reconcile configured provider containers")
	cmd.Flags().DurationVar(&opts.ReconcileInterval, "reconcile-interval", 5*time.Minute, "provider container reconcile interval")
	cmd.Flags().StringVar(&opts.DockerBin, "docker-bin", "docker", "docker CLI binary when --reconcile-containers uses Docker")
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
		providers := cfg.Providers
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
		return runNodeAgentControl(ctx, opts, providers)
	}
	return runNodeAgentControl(ctx, opts, nil)
}

func runNodeAgentControl(ctx context.Context, opts nodeAgentRunOptions, providers []nodeagent.ProviderSpec) error {
	var rt containerruntime.Runtime
	if opts.ReconcileContainers {
		switch opts.RuntimeKind {
		case "docker", "":
			rt = containerruntime.NewDockerRuntime(opts.DockerBin)
		default:
			return fmt.Errorf("unsupported --runtime-kind %q", opts.RuntimeKind)
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
		ProviderSpecs:     providers,
		ContainerRuntime:  rt,
		ReconcileInterval: opts.ReconcileInterval,
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

type nodeAgentReconcileProviderOptions struct {
	ConfigPath  string
	ProviderID  string
	NodeID      string
	HostName    string
	RuntimeKind string
	DockerBin   string
	DryRun      bool
}

func newNodeAgentReconcileProviderCmd() *cobra.Command {
	opts := nodeAgentReconcileProviderOptions{}
	cmd := &cobra.Command{
		Use:           "reconcile-provider",
		Short:         "create or start one configured provider container",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runNodeAgentReconcileProvider(cmd.Context(), opts)
		},
	}
	cmd.Flags().StringVar(&opts.ConfigPath, "config", "", "node-agent provider spec YAML path")
	cmd.Flags().StringVar(&opts.ProviderID, "provider", "", "provider id to reconcile")
	cmd.Flags().StringVar(&opts.NodeID, "node-id", "", "override node id")
	cmd.Flags().StringVar(&opts.HostName, "host-name", "", "override host name")
	cmd.Flags().StringVar(&opts.RuntimeKind, "runtime-kind", "docker", "container runtime kind")
	cmd.Flags().StringVar(&opts.DockerBin, "docker-bin", "docker", "docker CLI binary")
	cmd.Flags().BoolVar(&opts.DryRun, "dry-run", false, "validate and build container spec without touching runtime")
	return cmd
}

func runNodeAgentReconcileProvider(ctx context.Context, opts nodeAgentReconcileProviderOptions) error {
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
	nodeID := opts.NodeID
	if nodeID == "" {
		nodeID = cfg.Node.ID
	}
	hostName := opts.HostName
	if hostName == "" {
		hostName = cfg.Node.HostName
	}
	if opts.DryRun {
		_, err := nodeagent.ContainerSpecFromProviderSpec(spec, nodeID, hostName)
		return err
	}
	var rt containerruntime.Runtime
	switch opts.RuntimeKind {
	case "docker", "":
		rt = containerruntime.NewDockerRuntime(opts.DockerBin)
	default:
		return fmt.Errorf("unsupported --runtime-kind %q", opts.RuntimeKind)
	}
	_, err = nodeagent.ReconcileProviderContainer(ctx, rt, spec, nodeID, hostName)
	return err
}
