package main

import (
	"fmt"

	"github.com/0xc0de1ab/pangaea/internal/common"
	"github.com/0xc0de1ab/pangaea/internal/config"
	"github.com/0xc0de1ab/pangaea/internal/logging"
	"github.com/0xc0de1ab/pangaea/internal/reversebridge"
	"github.com/dh-kam/refutils/flagsbinder"
	"github.com/spf13/cobra"
)

func newReverseConnectCmd() *cobra.Command {
	opts := struct {
		ConfigPath    string `flag:"config" usage:"pangaea-server.yaml path"`
		ProfileFilter string `flag:"profile" usage:"filter profiles.yaml entries (comma-separated; default: all)"`
		SocketPath    string `flag:"socket" usage:"local server attach socket path"`
	}{}

	binder := flagsbinder.NewViperCobraFlagsBinder().
		StringP(common.FlagConfig, "c", "pangaea-server.yaml", "pangaea-server.yaml path").
		String(common.FlagProfile, "", "filter profiles.yaml entries (comma-separated; default: all)").
		String("socket", defaultStatusSocket(), "local server attach socket path")

	cmd := &cobra.Command{
		Use:           "reverse-connect",
		Short:         "run reverse connector bridge",
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
			pf, err := config.LoadProfiles(serverCfg.ProfilesFile)
			if err != nil {
				return fmt.Errorf("load profiles: %w", err)
			}
			ps := config.NewProfileStore(pf)
			if opts.ProfileFilter != "" {
				ps, err = reversebridge.ProfileSet(ps, opts.ProfileFilter)
				if err != nil {
					return err
				}
			}
			logLevel, _ := cmd.Flags().GetString(common.FlagLogLevel)
			logFormat, _ := cmd.Flags().GetString(common.FlagLogFormat)
			if logLevel == "" {
				logLevel = serverCfg.Log.Level
			}
			if logFormat == "" {
				logFormat = serverCfg.Log.Format
			}
			log := logging.New(logging.Options{Level: logLevel, Format: logFormat})
			return reversebridge.Run(cmd.Context(), serverCfg, ps, reversebridge.Options{
				SocketPath: opts.SocketPath,
			}, log)
		},
	}

	binder.SetTo(cmd.Flags())
	return cmd
}
