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

// newConnectCmd loads client.yaml (with override flags) and runs client.Run
// until the inherited context is cancelled.
func newConnectCmd() *cobra.Command {
	opts := struct {
		ConfigPath    string `flag:"config" usage:"pangaea-client.yaml path"`
		ServerURL     string `flag:"server" usage:"override server URL (wss://host:port)"`
		ProfileFilter string `flag:"profile" usage:"filter profiles in client.yaml (comma-separated; default: all)"`
		AuthMode      string `flag:"auth-mode" usage:"override auth mode (mtls|jwt)"`
		NodeID        string `flag:"node-id" usage:"override node id"`
		FailFast      bool   `flag:"fail-fast" usage:"exit on first dial failure instead of reconnecting"`
	}{}

	binder := flagsbinder.NewViperCobraFlagsBinder().
		StringP(common.FlagConfig, "c", "pangaea-client.yaml", "pangaea-client.yaml path").
		String(common.FlagServer, "", "override server URL (wss://host:port)").
		String(common.FlagProfile, "", "filter profiles in client.yaml (comma-separated; default: all)").
		String(common.FlagAuthMode, "", "override auth mode (mtls|jwt)").
		String(common.FlagNodeID, "", "override node id").
		Bool(common.FlagFailFast, false, "exit on first dial failure instead of reconnecting")

	cmd := &cobra.Command{
		Use:           "connect",
		Short:         common.CLIShortConnect,
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
			if opts.ServerURL != "" {
				clientCfg.Server = opts.ServerURL
			}
			if opts.AuthMode != "" {
				switch config.AuthMode(opts.AuthMode) {
				case config.AuthModeMTLS, config.AuthModeJWT:
				default:
					return fmt.Errorf("--%s must be mtls or jwt", common.FlagAuthMode)
				}
				clientCfg.AuthMode = config.AuthMode(opts.AuthMode)
			}
			if opts.NodeID != "" {
				clientCfg.NodeID = opts.NodeID
			}
			if opts.ProfileFilter != "" {
				filtered, err := filterProfiles(clientCfg.Profiles, opts.ProfileFilter)
				if err != nil {
					return err
				}
				clientCfg.Profiles = filtered
			}

			logLevel, _ := cmd.Flags().GetString(common.FlagLogLevel)
			logFormat, _ := cmd.Flags().GetString(common.FlagLogFormat)
			if logLevel == "" {
				logLevel = clientCfg.Log.Level
			}
			if logFormat == "" {
				logFormat = clientCfg.Log.Format
			}
			log := logging.New(logging.Options{Level: logLevel, Format: logFormat})

			if len(clientCfg.Profiles) == 0 {
				return fmt.Errorf("%w: client.yaml must include at least one profile binding", common.ErrConfigInvalid)
			}

			return client.Run(cmd.Context(), clientCfg, client.Options{
				AgentVersion: version,
				FailFast:     opts.FailFast,
			}, log)
		},
	}

	binder.SetTo(cmd.Flags())
	return cmd
}

// filterProfiles narrows the binding list to the comma-separated names in
// filter. Names not present in the config are reported as errors so a typo
// fails loudly instead of silently producing an empty client.
func filterProfiles(all []config.ProfileBinding, filter string) ([]config.ProfileBinding, error) {
	want := map[string]bool{}
	for _, n := range strings.Split(filter, ",") {
		n = strings.TrimSpace(n)
		if n != "" {
			want[n] = true
		}
	}
	out := make([]config.ProfileBinding, 0, len(want))
	for _, b := range all {
		if want[b.Name] {
			out = append(out, b)
			delete(want, b.Name)
		}
	}
	if len(want) > 0 {
		missing := make([]string, 0, len(want))
		for n := range want {
			missing = append(missing, n)
		}
		return nil, fmt.Errorf("%w: profile filter names not found in client.yaml: %v", common.ErrConfigInvalid, missing)
	}
	return out, nil
}
