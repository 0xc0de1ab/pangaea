package main

import (
	"fmt"
	"net"
	"path/filepath"
	"strings"
	"time"

	"github.com/0xc0de1ab/pangaea/internal/common"
	"github.com/0xc0de1ab/pangaea/internal/pki"
	"github.com/dh-kam/refutils/flagsbinder"
	"github.com/spf13/cobra"
)

// newCACmd builds the `ca` group: init, issue-server, issue-client. The CA
// private key lives on disk in plain form (dev / small-team MVP); ops who
// need HSM-backed CAs run an external pipeline and drop the leaf certs in
// place.
func newCACmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "ca",
		Short:         common.CLIShortCA,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	cmd.AddCommand(newCAInitCmd())
	cmd.AddCommand(newCAIssueServerCmd())
	cmd.AddCommand(newCAIssueClientCmd())
	cmd.AddCommand(newCAVerifyServerCmd())
	cmd.AddCommand(newCAVerifyClientCmd())
	return cmd
}

func newCAInitCmd() *cobra.Command {
	opts := struct {
		OutDir string `flag:"out" usage:"output directory"`
		CN     string `flag:"cn" usage:"CA common name"`
		Years  int    `flag:"years" usage:"validity in years"`
	}{}
	binder := flagsbinder.NewViperCobraFlagsBinder().
		String(common.FlagOutDir, "./pki", "output directory").
		String(common.FlagCommonName, "pangaeactl Root CA", "CA common name").
		Int("years", 10, "validity in years")

	cmd := &cobra.Command{
		Use:           "init",
		Short:         "create a new ECDSA P-256 CA",
		SilenceUsage:  true,
		SilenceErrors: true,
		PreRunE: func(cmd *cobra.Command, args []string) error {
			if err := binder.BindCommand(cmd, &opts, args...); err != nil {
				_ = cmd.Usage()
				return err
			}
			if opts.Years <= 0 {
				_ = cmd.Usage()
				return fmt.Errorf("--years must be positive")
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			notAfter := time.Now().AddDate(opts.Years, 0, 0)
			if _, err := pki.NewCA(opts.OutDir, opts.CN, notAfter); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "CA written to %s (ca.crt, ca.key); notAfter=%s\n",
				opts.OutDir, notAfter.Format(time.RFC3339))
			return nil
		},
	}
	binder.SetTo(cmd.Flags())
	return cmd
}

func newCAIssueServerCmd() *cobra.Command {
	opts := struct {
		CADir  string `flag:"ca" usage:"CA directory (containing ca.crt, ca.key)"`
		OutDir string `flag:"out" usage:"output directory"`
		CN     string `flag:"cn" usage:"server CN"`
		SANs   string `flag:"san" usage:"comma-separated SANs: IP:1.2.3.4 | DNS:host.local"`
		Years  int    `flag:"years" usage:"validity in years"`
	}{}
	binder := flagsbinder.NewViperCobraFlagsBinder().
		String(common.FlagCAPath, "./pki", "CA directory (containing ca.crt, ca.key)").
		String(common.FlagOutDir, "./pki/server", "output directory").
		String(common.FlagCommonName, "pangaeactl-server", "server CN").
		String(common.FlagSAN, "", "comma-separated SANs: IP:1.2.3.4 | DNS:host.local").
		Int("years", 1, "validity in years")

	cmd := &cobra.Command{
		Use:           "issue-server",
		Short:         "issue a server leaf certificate",
		SilenceUsage:  true,
		SilenceErrors: true,
		PreRunE: func(cmd *cobra.Command, args []string) error {
			if err := binder.BindCommand(cmd, &opts, args...); err != nil {
				_ = cmd.Usage()
				return err
			}
			if opts.SANs == "" {
				_ = cmd.Usage()
				return fmt.Errorf("--%s is required (e.g. DNS:host.local,IP:127.0.0.1)", common.FlagSAN)
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			ca, err := pki.LoadCA(opts.CADir+"/ca.crt", opts.CADir+"/ca.key")
			if err != nil {
				return err
			}
			san, err := parseSANs(opts.SANs)
			if err != nil {
				return err
			}
			notAfter := time.Now().AddDate(opts.Years, 0, 0)
			if err := pki.IssueServer(ca, opts.OutDir, opts.CN, san, notAfter); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "server cert/key written to %s\n", opts.OutDir)
			return nil
		},
	}
	binder.SetTo(cmd.Flags())
	return cmd
}

func newCAIssueClientCmd() *cobra.Command {
	opts := struct {
		CADir  string `flag:"ca" usage:"CA directory"`
		OutDir string `flag:"out" usage:"output directory"`
		CN     string `flag:"cn" usage:"client CN (REQUIRED; must match node_id in profiles.yaml)"`
		Years  int    `flag:"years" usage:"validity in years"`
	}{}
	binder := flagsbinder.NewViperCobraFlagsBinder().
		String(common.FlagCAPath, "./pki", "CA directory").
		String(common.FlagOutDir, "./pki/client", "output directory").
		String(common.FlagCommonName, "", "client CN (REQUIRED; must match node_id in profiles.yaml)").
		Int("years", 1, "validity in years")

	cmd := &cobra.Command{
		Use:           "issue-client",
		Short:         "issue a client leaf certificate",
		SilenceUsage:  true,
		SilenceErrors: true,
		PreRunE: func(cmd *cobra.Command, args []string) error {
			if err := binder.BindCommand(cmd, &opts, args...); err != nil {
				_ = cmd.Usage()
				return err
			}
			if opts.CN == "" {
				_ = cmd.Usage()
				return fmt.Errorf("--%s is required and must match a node_id", common.FlagCommonName)
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			ca, err := pki.LoadCA(opts.CADir+"/ca.crt", opts.CADir+"/ca.key")
			if err != nil {
				return err
			}
			notAfter := time.Now().AddDate(opts.Years, 0, 0)
			if err := pki.IssueClient(ca, opts.OutDir, opts.CN, notAfter); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "client cert/key written to %s (CN=%s)\n", opts.OutDir, opts.CN)
			return nil
		},
	}
	binder.SetTo(cmd.Flags())
	return cmd
}

// parseSANs parses a comma-separated list of "IP:1.2.3.4" and "DNS:host.local"
// entries into a pki.SAN. At least one entry is required.
func parseSANs(s string) (pki.SAN, error) {
	if s == "" {
		return pki.SAN{}, fmt.Errorf("%w: %s", common.ErrConfigInvalid, common.MsgSANEmpty)
	}
	var san pki.SAN
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		switch {
		case strings.HasPrefix(part, "IP:"):
			ip := net.ParseIP(strings.TrimPrefix(part, "IP:"))
			if ip == nil {
				return pki.SAN{}, fmt.Errorf("invalid IP SAN: %q", part)
			}
			san.IPs = append(san.IPs, ip)
		case strings.HasPrefix(part, "DNS:"):
			san.DNS = append(san.DNS, strings.TrimPrefix(part, "DNS:"))
		default:
			return pki.SAN{}, fmt.Errorf("SAN entry must start with IP: or DNS:, got %q", part)
		}
	}
	return san, nil
}

func newCAVerifyServerCmd() *cobra.Command {
	opts := struct {
		CADir      string `flag:"ca" usage:"CA directory"`
		CertPath   string `flag:"cert" usage:"server certificate path"`
		ServerName string `flag:"server-name" usage:"server name to verify against SANs"`
	}{}
	binder := flagsbinder.NewViperCobraFlagsBinder().
		String(common.FlagCAPath, "./pki", "CA directory").
		String("cert", "", "server certificate path (default: <ca>/server/server.crt)").
		String("server-name", "", "server name to verify against SANs")

	cmd := &cobra.Command{
		Use:           "verify-server",
		Short:         "verify a server leaf certificate against the CA",
		SilenceUsage:  true,
		SilenceErrors: true,
		PreRunE: func(cmd *cobra.Command, args []string) error {
			if err := binder.BindCommand(cmd, &opts, args...); err != nil {
				_ = cmd.Usage()
				return err
			}
			if opts.ServerName == "" {
				_ = cmd.Usage()
				return fmt.Errorf("--server-name is required")
			}
			if opts.CertPath == "" {
				opts.CertPath = filepath.Join(opts.CADir, "server", "server.crt")
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			cert, err := pki.VerifyServerCert(filepath.Join(opts.CADir, "ca.crt"), opts.CertPath, opts.ServerName, time.Now())
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "server cert verified: cn=%s notAfter=%s\n", cert.Subject.CommonName, cert.NotAfter.Format(time.RFC3339))
			return nil
		},
	}
	binder.SetTo(cmd.Flags())
	return cmd
}

func newCAVerifyClientCmd() *cobra.Command {
	opts := struct {
		CADir    string `flag:"ca" usage:"CA directory"`
		CertPath string `flag:"cert" usage:"client certificate path"`
		CN       string `flag:"cn" usage:"expected client CN"`
	}{}
	binder := flagsbinder.NewViperCobraFlagsBinder().
		String(common.FlagCAPath, "./pki", "CA directory").
		String("cert", "", "client certificate path (default: <ca>/client/client.crt)").
		String(common.FlagCommonName, "", "expected client CN")

	cmd := &cobra.Command{
		Use:           "verify-client",
		Short:         "verify a client leaf certificate against the CA",
		SilenceUsage:  true,
		SilenceErrors: true,
		PreRunE: func(cmd *cobra.Command, args []string) error {
			if err := binder.BindCommand(cmd, &opts, args...); err != nil {
				_ = cmd.Usage()
				return err
			}
			if opts.CertPath == "" {
				opts.CertPath = filepath.Join(opts.CADir, "client", "client.crt")
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			cert, err := pki.VerifyClientCert(filepath.Join(opts.CADir, "ca.crt"), opts.CertPath, opts.CN, time.Now())
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "client cert verified: cn=%s notAfter=%s\n", cert.Subject.CommonName, cert.NotAfter.Format(time.RFC3339))
			return nil
		},
	}
	binder.SetTo(cmd.Flags())
	return cmd
}
