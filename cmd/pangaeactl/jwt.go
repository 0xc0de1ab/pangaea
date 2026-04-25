package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/0xc0de1ab/pangaea/internal/common"
	"github.com/0xc0de1ab/pangaea/internal/jwtauth"
	"github.com/dh-kam/refutils/flagsbinder"
	"github.com/spf13/cobra"
)

func newJWTCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "jwt",
		Short:         common.CLIShortJWT,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	cmd.AddCommand(newJWTInitCmd())
	cmd.AddCommand(newJWTIssueCmd())
	cmd.AddCommand(newJWTVerifyCmd())
	return cmd
}

func newJWTInitCmd() *cobra.Command {
	opts := struct {
		OutPath string `flag:"out" usage:"output secret key path"`
	}{}
	binder := flagsbinder.NewViperCobraFlagsBinder().
		String(common.FlagOutDir, "./jwt.secret", "output secret key path")

	cmd := &cobra.Command{
		Use:           "init",
		Short:         "generate a JWT HMAC secret key",
		SilenceUsage:  true,
		SilenceErrors: true,
		PreRunE: func(cmd *cobra.Command, args []string) error {
			if err := binder.BindCommand(cmd, &opts, args...); err != nil {
				_ = cmd.Usage()
				return err
			}
			if opts.OutPath == "" {
				_ = cmd.Usage()
				return fmt.Errorf("--out is required")
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			secret, err := jwtauth.GenerateSecret()
			if err != nil {
				return err
			}
			if err := jwtauth.WriteSecretFile(opts.OutPath, secret); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "JWT secret written to %s\n", opts.OutPath)
			return nil
		},
	}
	binder.SetTo(cmd.Flags())
	return cmd
}

func newJWTIssueCmd() *cobra.Command {
	opts := struct {
		SecretPath string   `flag:"secret-key" usage:"JWT secret key file"`
		NodeID     string   `flag:"node-id" usage:"JWT subject / node id"`
		Profiles   []string `flag:"profile" usage:"allowed profile names"`
		Issuer     string   `flag:"issuer" usage:"JWT issuer"`
		Audience   string   `flag:"audience" usage:"JWT audience"`
		TTL        string   `flag:"ttl" usage:"token lifetime"`
		OutPath    string   `flag:"out" usage:"write token to file instead of stdout"`
	}{}
	binder := flagsbinder.NewViperCobraFlagsBinder().
		String("secret-key", "", "JWT secret key file").
		String(common.FlagNodeID, "", "JWT subject / node id").
		StringSlice(common.FlagProfile, nil, "allowed profile names").
		String("issuer", "", "JWT issuer").
		String("audience", "", "JWT audience").
		String("ttl", "24h", "token lifetime").
		String(common.FlagOutDir, "", "write token to file instead of stdout")

	cmd := &cobra.Command{
		Use:           "issue",
		Short:         "issue a JWT for one node and one or more profiles",
		SilenceUsage:  true,
		SilenceErrors: true,
		PreRunE: func(cmd *cobra.Command, args []string) error {
			if err := binder.BindCommand(cmd, &opts, args...); err != nil {
				_ = cmd.Usage()
				return err
			}
			if opts.SecretPath == "" || opts.NodeID == "" || opts.Issuer == "" || opts.Audience == "" || len(opts.Profiles) == 0 {
				_ = cmd.Usage()
				return fmt.Errorf("--secret-key, --node-id, --issuer, --audience, and at least one --profile are required")
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			ttl, err := time.ParseDuration(opts.TTL)
			if err != nil {
				return fmt.Errorf("invalid --ttl: %w", err)
			}
			secret, err := jwtauth.LoadSecretFile(opts.SecretPath)
			if err != nil {
				return err
			}
			token, err := jwtauth.Issue(secret, opts.NodeID, opts.Profiles, opts.Issuer, opts.Audience, time.Now(), ttl)
			if err != nil {
				return err
			}
			if opts.OutPath != "" {
				return os.WriteFile(opts.OutPath, []byte(token+"\n"), 0o600)
			}
			fmt.Fprintln(cmd.OutOrStdout(), token)
			return nil
		},
	}
	binder.SetTo(cmd.Flags())
	return cmd
}

func newJWTVerifyCmd() *cobra.Command {
	opts := struct {
		SecretPath string `flag:"secret-key" usage:"JWT secret key file"`
		Token      string `flag:"token" usage:"JWT token or @path"`
		Issuer     string `flag:"issuer" usage:"expected JWT issuer"`
		Audience   string `flag:"audience" usage:"expected JWT audience"`
	}{}
	binder := flagsbinder.NewViperCobraFlagsBinder().
		String("secret-key", "", "JWT secret key file").
		String(common.FlagToken, "", "JWT token or @path to a token file").
		String("issuer", "", "expected JWT issuer").
		String("audience", "", "expected JWT audience")

	cmd := &cobra.Command{
		Use:           "verify",
		Short:         "verify a JWT and print its claims",
		SilenceUsage:  true,
		SilenceErrors: true,
		PreRunE: func(cmd *cobra.Command, args []string) error {
			if err := binder.BindCommand(cmd, &opts, args...); err != nil {
				_ = cmd.Usage()
				return err
			}
			if opts.SecretPath == "" || opts.Token == "" || opts.Issuer == "" || opts.Audience == "" {
				_ = cmd.Usage()
				return fmt.Errorf("--secret-key, --token, --issuer, and --audience are required")
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			secret, err := jwtauth.LoadSecretFile(opts.SecretPath)
			if err != nil {
				return err
			}
			token, err := readTokenArg(opts.Token)
			if err != nil {
				return err
			}
			claims, err := jwtauth.Verify(secret, token, opts.Issuer, opts.Audience, time.Now())
			if err != nil {
				return err
			}
			out := map[string]any{
				"subject":    claims.Subject,
				"issuer":     claims.Issuer,
				"audience":   claims.Audience,
				"profiles":   claims.Profiles,
				"issued_at":  claims.IssuedAt.Time.Format(time.RFC3339),
				"expires_at": claims.ExpiresAt.Time.Format(time.RFC3339),
			}
			enc := json.NewEncoder(cmd.OutOrStdout())
			enc.SetIndent("", "  ")
			return enc.Encode(out)
		},
	}
	binder.SetTo(cmd.Flags())
	return cmd
}

func readTokenArg(arg string) (string, error) {
	if strings.HasPrefix(arg, "@") {
		raw, err := os.ReadFile(strings.TrimPrefix(arg, "@"))
		if err != nil {
			return "", err
		}
		return strings.TrimSpace(string(raw)), nil
	}
	return strings.TrimSpace(arg), nil
}
