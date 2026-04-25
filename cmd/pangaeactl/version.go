package main

import (
	"fmt"
	"runtime"

	"github.com/0xc0de1ab/pangaea/internal/common"
	"github.com/spf13/cobra"
)

// newVersionCmd prints the build version plus Go runtime/platform. Used by
// operators to verify that every node is on the same binary.
func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: common.CLIShortVersion,
		Run: func(cmd *cobra.Command, _ []string) {
			fmt.Fprintf(cmd.OutOrStdout(), "pangaeactl %s %s/%s %s\n",
				version, runtime.GOOS, runtime.GOARCH, runtime.Version(),
			)
		},
	}
}
