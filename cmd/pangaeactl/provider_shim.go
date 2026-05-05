package main

import (
	"context"
	"fmt"
	"time"

	"github.com/0xc0de1ab/pangaea/internal/common"
	"github.com/0xc0de1ab/pangaea/internal/providershim"
	"github.com/0xc0de1ab/pangaea/internal/providersim"
	"github.com/spf13/cobra"
)

type providerShimRunOptions struct {
	RouterControlURL  string
	Simulator         bool
	HeartbeatInterval time.Duration
}

func newProviderShimCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "provider-shim",
		Short:         common.CLIShortShim,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	cmd.AddCommand(newProviderShimRunCmd())
	return cmd
}

func newProviderShimRunCmd() *cobra.Command {
	opts := providerShimRunOptions{}
	cmd := &cobra.Command{
		Use:           "run",
		Short:         "connect a provider shim to the v2 router control plane",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runProviderShim(cmd.Context(), opts)
		},
	}
	cmd.Flags().StringVar(&opts.RouterControlURL, "router-control", "", "router control WebSocket URL")
	cmd.Flags().BoolVar(&opts.Simulator, "simulator", false, "run the built-in simulator shim")
	cmd.Flags().DurationVar(&opts.HeartbeatInterval, "heartbeat-interval", 30*time.Second, "control heartbeat interval")
	return cmd
}

func runProviderShim(ctx context.Context, opts providerShimRunOptions) error {
	if opts.RouterControlURL == "" {
		return fmt.Errorf("--router-control is required")
	}
	if !opts.Simulator {
		return fmt.Errorf("--simulator is currently required")
	}
	sim, err := providersim.New(providersim.Options{Mode: providersim.ModeAPICompatible})
	if err != nil {
		return err
	}
	return providershim.RunSimulatorControlClient(ctx, providershim.ControlClientOptions{
		ControlURL:        opts.RouterControlURL,
		HeartbeatInterval: opts.HeartbeatInterval,
		Simulator:         sim,
	})
}
