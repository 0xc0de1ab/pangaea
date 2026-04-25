package main

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"

	"github.com/0xc0de1ab/pangaea/internal/common"
	"github.com/dh-kam/refutils/flagsbinder"
	"github.com/spf13/cobra"
)

// newStatusCmd queries the server's unix-socket status endpoint. Server
// must be running on the same host with $XDG_RUNTIME_DIR/pangaea.sock
// available (or the path passed via --socket).
func newStatusCmd() *cobra.Command {
	opts := struct {
		Socket string `flag:"socket" usage:"unix socket path (defaults to $XDG_RUNTIME_DIR/pangaea.sock)"`
	}{}
	binder := flagsbinder.NewViperCobraFlagsBinder().
		String("socket", "", "unix socket path (defaults to $XDG_RUNTIME_DIR/pangaea.sock)")

	cmd := &cobra.Command{
		Use:           "status",
		Short:         common.CLIShortStatus,
		SilenceUsage:  true,
		SilenceErrors: true,
		PreRunE: func(cmd *cobra.Command, args []string) error {
			if err := binder.BindCommand(cmd, &opts, args...); err != nil {
				_ = cmd.Usage()
				return err
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			socket := opts.Socket
			if socket == "" {
				socket = defaultStatusSocket()
			}
			httpClient := &http.Client{
				Transport: &http.Transport{
					DialContext: func(ctx context.Context, _ string, _ string) (net.Conn, error) {
						var d net.Dialer
						return d.DialContext(ctx, "unix", socket)
					},
				},
				Timeout: 3 * time.Second,
			}
			req, err := http.NewRequestWithContext(cmd.Context(), http.MethodGet, "http://unix/status", nil)
			if err != nil {
				return err
			}
			resp, err := httpClient.Do(req)
			if err != nil {
				return fmt.Errorf("status: %w", err)
			}
			defer resp.Body.Close()
			if _, err := io.Copy(cmd.OutOrStdout(), resp.Body); err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout())
			return nil
		},
	}
	binder.SetTo(cmd.Flags())
	return cmd
}
