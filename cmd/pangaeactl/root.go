package main

import (
	"github.com/0xc0de1ab/pangaea/internal/common"
	"github.com/dh-kam/refutils/flagsbinder"
	"github.com/spf13/cobra"
)

// version follows vSEMVER-YYYYMM.seq. Release and CI builds override this via
// -ldflags; the checked-in default marks the current release line.
var version = "v0.9.0-202605.1"

// rootGlobals holds persistent-flag values bound at root level. Subcommands
// reach them via cmd.Flags().GetString — but this file owns the binder so
// the flags themselves are declared in one place.
type rootGlobals struct {
	LogLevel  string `flag:"log-level" usage:"log level (debug|info|warn|error)"`
	LogFormat string `flag:"log-format" usage:"log format (json|text)"`
}

func newRootCmd() *cobra.Command {
	opts := rootGlobals{}
	binder := flagsbinder.NewViperCobraFlagsBinder().
		String(common.FlagLogLevel, "", "log level (debug|info|warn|error)").
		String(common.FlagLogFormat, "", "log format (json|text)")

	root := &cobra.Command{
		Use:           "pangaeactl",
		Short:         common.CLIShortRoot,
		SilenceUsage:  true,
		SilenceErrors: true,
		Version:       version,
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			return binder.Bind(cmd.Flags(), &opts, args...)
		},
	}
	binder.SetTo(root.PersistentFlags())

	root.AddCommand(newServeCmd())
	root.AddCommand(newConnectCmd())
	root.AddCommand(newReverseClientCmd())
	root.AddCommand(newReverseConnectCmd())
	root.AddCommand(newSetupCmd())
	root.AddCommand(newSetupProviderCmd())
	root.AddCommand(newCACmd())
	root.AddCommand(newJWTCmd())
	root.AddCommand(newInspectCmd())
	root.AddCommand(newStatusCmd())
	root.AddCommand(newVersionCmd())
	root.AddCommand(newRouterCmd())
	root.AddCommand(newProviderShimCmd())
	root.AddCommand(newNodeAgentCmd())
	return root
}
