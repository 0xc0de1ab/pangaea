package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var (
	rootCmd = &cobra.Command{
		Use:           "proxy",
		Short:         "Antigravity Compatibility Proxy",
		Long:          `A proxy server that provides an OpenAI-compatible API for Antigravity, handling process management and authentication token scraping.`,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	version = "v0.1.0" // Default version, can be overridden by build flags
)

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
		os.Exit(1)
	}
}

func init() {
	// Add subcommands
	rootCmd.AddCommand(newServeCommand())
	rootCmd.AddCommand(newStatusCommand())
	rootCmd.AddCommand(newUpdateCommand())
	rootCmd.AddCommand(newMonitorCommand())
	rootCmd.AddCommand(newModelsCommand())
	rootCmd.AddCommand(newAccountCommand())
	rootCmd.AddCommand(newChatCommand())
	rootCmd.AddCommand(generateKeysCmd)
 // This one was already added, I'll refactor it later if needed
}

func usageError(cmd *cobra.Command, err error) error {
	cmd.Usage()
	return err
}
