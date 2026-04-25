// Command pangaeactl is the single-binary entry point for the
// mediating server and node clients. Subcommands: serve, connect, ca,
// inspect, status, version.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/charmbracelet/lipgloss"

	// Register all built-in formats by side effect so every subcommand
	// (serve / connect / inspect) can resolve them by name. Adding a new
	// format means adding one line here.
	_ "github.com/0xc0de1ab/pangaea/pkg/formats/claudecreds"
	_ "github.com/0xc0de1ab/pangaea/pkg/formats/codexauth"
	_ "github.com/0xc0de1ab/pangaea/pkg/formats/geminioauth"
)

var errorPrefix = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("9")).Render("ERROR:")

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	if err := newRootCmd().ExecuteContext(ctx); err != nil {
		fmt.Fprintln(os.Stderr, errorPrefix, err)
		os.Exit(1)
	}
}
