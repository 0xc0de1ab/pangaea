package main

import (
	"context"
	"fmt"
	"os"

	"github.com/dh-kam/refutils/flagsbinder"
	"github.com/google/antigravity-compat-proxy/internal/bridge"
	"github.com/google/antigravity-compat-proxy/internal/scraper"
	"github.com/spf13/cobra"
)

type statusOptions struct {
	DBPath   string `flag:"db-path" env:"STATE_VSCDB_PATH" usage:"Path to state.vscdb"`
	CorePort int    `flag:"core-port" usage:"Internal port of ls_core"`
	CoreCSRF string `flag:"core-csrf" env:"CORE_CSRF_TOKEN" usage:"CSRF token for internal core communication"`
}

func newStatusCommand() *cobra.Command {
	opts := &statusOptions{}
	binder := flagsbinder.NewViperCobraFlagsBinder().
		String("db-path", os.ExpandEnv("$HOME/.antigravity-server/data/User/globalStorage/state.vscdb"), "Path to state.vscdb").
		Int("core-port", 5505, "Internal port of ls_core").
		String("core-csrf", "proxy-secret-token", "CSRF token for internal core communication")

	cmd := &cobra.Command{
		Use:           "status",
		Short:         "Display the health of ls_core and the current token status",
		SilenceUsage:  true,
		SilenceErrors: true,
		PreRunE: func(cmd *cobra.Command, args []string) error {
			return binder.BindCommand(cmd, opts, args...)
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			return runStatus(cmd.Context(), opts)
		},
	}
	binder.SetTo(cmd.Flags())
	return cmd
}

func runStatus(ctx context.Context, opts *statusOptions) error {
	fmt.Println("--- Antigravity Status ---")

	// Check Token status via Scraper
	sc := scraper.NewSQLiteScraper(opts.DBPath)
	token, err := sc.GetLatestToken()

	// Check ls_core health via bridge
	coreAddr := fmt.Sprintf("http://localhost:%d", opts.CorePort)
	br := bridge.NewEngineBridge(coreAddr, sc)
	br.SetCoreCSRF(opts.CoreCSRF)

	modelsList, errBridge := br.GetModels(ctx)
	if errBridge != nil {
		fmt.Printf("ls_core: UNHEALTHY (%v)\n", errBridge)
	} else {
		fmt.Printf("ls_core: HEALTHY (Models: %d found)\n", len(modelsList))
		if len(modelsList) > 0 {
			fmt.Printf("Example Models: %v\n", modelsList[:3])
		}
	}

	if err != nil {
		fmt.Printf("Token:   ERROR (%v)\n", err)
	} else if token == "" {
		fmt.Printf("Token:   MISSING (Not found in DB)\n")
	} else {
		fmt.Printf("Token:   OK (Length: %d)\n", len(token))
	}

	return nil
}
