package main

import (
	"fmt"
	"strings"

	"github.com/0xc0de1ab/pangaea/internal/client"
	"github.com/0xc0de1ab/pangaea/internal/common"
	"github.com/0xc0de1ab/pangaea/internal/config"
	"github.com/0xc0de1ab/pangaea/internal/logging"
	"github.com/dh-kam/refutils/flagsbinder"
	"github.com/spf13/cobra"
)

func newReverseClientCmd() *cobra.Command {
	opts := struct {
		ConfigPath    string `flag:"config" usage:"pangaea-client.yaml path"`
		ProfileFilter string `flag:"profile" usage:"filter profiles in client.yaml (comma-separated; default: all)"`
		Listen        string `flag:"listen" usage:"override reverse listener address"`
		PrintAddr     bool   `flag:"print-listen-addr" usage:"print the bound reverse listen address to stdout"`
	}{}

	binder := flagsbinder.NewViperCobraFlagsBinder().
		StringP(common.FlagConfig, "c", "pangaea-client.yaml", "pangaea-client.yaml path").
		String(common.FlagProfile, "", "filter profiles in client.yaml (comma-separated; default: all)").
		String("listen", "", "override reverse listener address").
		Bool("print-listen-addr", false, "print the bound reverse listen address to stdout")

	cmd := &cobra.Command{
		Use:           "reverse-client",
		Short:         "run reverse client listener",
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
			clientCfg, err := config.LoadClient(opts.ConfigPath)
			if err != nil {
				return err
			}
			if opts.ProfileFilter != "" {
				filtered, err := filterProfiles(clientCfg.Profiles, opts.ProfileFilter)
				if err != nil {
					return err
				}
				clientCfg.Profiles = filtered
			}
			if strings.TrimSpace(opts.Listen) != "" {
				clientCfg.Reverse.Listen = strings.TrimSpace(opts.Listen)
			}
			logLevel, _ := cmd.Flags().GetString(common.FlagLogLevel)
			logFormat, _ := cmd.Flags().GetString(common.FlagLogFormat)
			if logLevel == "" {
				logLevel = clientCfg.Log.Level
			}
			if logFormat == "" {
				logFormat = clientCfg.Log.Format
			}
			onListening := func(string) {}
			if opts.PrintAddr {
				onListening = func(addr string) {
					fmt.Fprintln(cmd.OutOrStdout(), addr)
				}
			}
			log := logging.New(logging.Options{Level: logLevel, Format: logFormat})
			return client.RunReverse(cmd.Context(), clientCfg, client.Options{
				AgentVersion:       version,
				OnReverseListening: onListening,
			}, log)
		},
	}

	binder.SetTo(cmd.Flags())
	return cmd
}
