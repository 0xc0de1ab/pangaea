package main

import (
	"context"

	"github.com/dh-kam/refutils/flagsbinder"
	"github.com/google/antigravity-compat-proxy/internal/bridge"
	"github.com/spf13/cobra"
)

type updateOptions struct {
	CoreAddr string `flag:"core-addr" usage:"Address of the Antigravity core (optional)"`
}

func newUpdateCommand() *cobra.Command {
	opts := &updateOptions{}
	binder := flagsbinder.NewViperCobraFlagsBinder().
		String("core-addr", "http://localhost:40037", "Address of the Antigravity core")

	cmd := &cobra.Command{
		Use:   "update",
		Short: "Automatically download and update Antigravity backend bundle",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := binder.BindCommand(cmd, opts, args...); err != nil {
				return usageError(cmd, err)
			}
			return runUpdate(opts)
		},
	}
	binder.SetTo(cmd.Flags())
	return cmd
}

func runUpdate(opts *updateOptions) error {
	br := bridge.NewEngineBridge(opts.CoreAddr, &dummyAuth{})
	return br.UpdateBackend(context.Background())
}

type dummyAuth struct{}

func (d *dummyAuth) GetLatestToken() (string, error)                              { return "dummy", nil }
func (d *dummyAuth) WatchTokenChanges(ctx context.Context) (<-chan string, error) { return nil, nil }
