package main

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/0xc0de1ab/pangaea/internal/common"
	"github.com/0xc0de1ab/pangaea/internal/config"
	"github.com/0xc0de1ab/pangaea/internal/logging"
	"github.com/0xc0de1ab/pangaea/internal/server"
	"github.com/dh-kam/refutils/flagsbinder"
	"github.com/spf13/cobra"
)

// newServeCmd loads server.yaml + profiles.yaml and hands off to server.Run.
// SIGHUP reloads profiles; SIGINT/SIGTERM tear down via the inherited ctx.
func newServeCmd() *cobra.Command {
	opts := struct {
		ConfigPath  string   `flag:"config" usage:"pangaea-server.yaml path"`
		AuthMode    string   `flag:"auth-mode" usage:"override auth mode (mtls|jwt)"`
		AlsoClients []string `flag:"also-client" usage:"comma-separated profile names to also run as in-process client"`
	}{}

	binder := flagsbinder.NewViperCobraFlagsBinder().
		StringP(common.FlagConfig, "c", "pangaea-server.yaml", "pangaea-server.yaml path").
		String(common.FlagAuthMode, "", "override auth mode (mtls|jwt)").
		StringSlice(common.FlagAlsoClient, nil, "comma-separated profile names to also run as in-process client")

	cmd := &cobra.Command{
		Use:           "serve",
		Short:         common.CLIShortServe,
		SilenceUsage:  true,
		SilenceErrors: true,
		PreRunE: func(cmd *cobra.Command, args []string) error {
			if err := binder.BindCommand(cmd, &opts, args...); err != nil {
				_ = cmd.Usage()
				return err
			}
			if opts.ConfigPath == "" {
				_ = cmd.Usage()
				return fmt.Errorf("--%s is required", common.FlagConfig)
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			serverCfg, err := config.LoadServer(opts.ConfigPath)
			if err != nil {
				return err
			}
			if opts.AuthMode != "" {
				switch config.AuthMode(opts.AuthMode) {
				case config.AuthModeMTLS, config.AuthModeJWT:
				default:
					return fmt.Errorf("--%s must be mtls or jwt", common.FlagAuthMode)
				}
				serverCfg.AuthMode = config.AuthMode(opts.AuthMode)
			}
			pf, err := config.LoadProfiles(serverCfg.ProfilesFile)
			if err != nil {
				return fmt.Errorf("load profiles: %w", err)
			}
			ps := config.NewProfileStore(pf)

			logLevel, _ := cmd.Flags().GetString(common.FlagLogLevel)
			logFormat, _ := cmd.Flags().GetString(common.FlagLogFormat)
			if logLevel == "" {
				logLevel = serverCfg.Log.Level
			}
			if logFormat == "" {
				logFormat = serverCfg.Log.Format
			}
			log := logging.New(logging.Options{Level: logLevel, Format: logFormat})

			for _, name := range opts.AlsoClients {
				if _, ok := ps.Get(name); !ok {
					return fmt.Errorf("%w: %s", common.ErrProfileNotFound, name)
				}
			}

			ctx := cmd.Context()
			sigCh := make(chan os.Signal, 4)
			signal.Notify(sigCh, syscall.SIGHUP)
			defer signal.Stop(sigCh)
			go func() {
				for {
					select {
					case <-ctx.Done():
						return
					case s := <-sigCh:
						if s == syscall.SIGHUP {
							if err := ps.Reload(serverCfg.ProfilesFile); err != nil {
								fmt.Fprintln(cmd.ErrOrStderr(), "reload failed (keeping previous config):", err)
							}
						}
					}
				}
			}()

			srvOpts := server.Options{
				ServerVersion: version,
				StatusSocket:  defaultStatusSocket(),
				AlsoClients:   opts.AlsoClients,
				ProfilesPath:  serverCfg.ProfilesFile,
			}
			if len(opts.AlsoClients) > 0 {
				if !serverCfg.SelfNode.Enabled {
					return fmt.Errorf("--%s requires server.self_node.enabled=true and self-node PKI paths", common.FlagAlsoClient)
				}
				srvOpts.SelfClientFn = server.SelfClientFactory(serverCfg, ps, log, version)
			}

			return server.Run(ctx, serverCfg, ps, srvOpts, log)
		},
	}

	binder.SetTo(cmd.Flags())
	return cmd
}

// defaultStatusSocket returns $XDG_RUNTIME_DIR/pangaea.sock or
// falls back to /tmp so the status endpoint is reachable from the
// `status` subcommand on the same host.
func defaultStatusSocket() string {
	if dir := os.Getenv("XDG_RUNTIME_DIR"); dir != "" {
		return dir + "/pangaea.sock"
	}
	return "/tmp/pangaea.sock"
}
