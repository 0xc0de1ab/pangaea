package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/0xc0de1ab/pangaea/internal/common"
	"github.com/0xc0de1ab/pangaea/pkg/formats"
	"github.com/dh-kam/refutils/flagsbinder"
	"github.com/spf13/cobra"
)

// newInspectCmd reads a credentials file and prints the redacted Summary +
// ValidationResult. Operator-facing: the primary goal is "why is my file
// not selected as truth?".
func newInspectCmd() *cobra.Command {
	opts := struct {
		Format    string `flag:"format" usage:"format name (defaults to first registered)"`
		LiveCheck bool   `flag:"live-check" usage:"issue a live HTTP probe to the issuing service"`
	}{}
	binder := flagsbinder.NewViperCobraFlagsBinder().
		String("format", "", "format name (defaults to first registered)").
		Bool("live-check", false, "issue a live HTTP probe to the issuing service")

	cmd := &cobra.Command{
		Use:           "inspect <path>",
		Short:         common.CLIShortInspect,
		SilenceUsage:  true,
		SilenceErrors: true,
		PreRunE: func(cmd *cobra.Command, args []string) error {
			if err := binder.BindCommand(cmd, &opts, args...); err != nil {
				_ = cmd.Usage()
				return err
			}
			if len(args) != 1 {
				_ = cmd.Usage()
				return fmt.Errorf("inspect requires exactly one path argument")
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			formatName := opts.Format
			if formatName == "" {
				names := formats.List()
				if len(names) == 0 {
					return fmt.Errorf("%w: no formats registered", common.ErrFormatNotRegistered)
				}
				formatName = names[0]
			}
			f, ok := formats.Get(formatName)
			if !ok {
				return fmt.Errorf("%w: %s", common.ErrFormatNotRegistered, formatName)
			}
			raw, err := os.ReadFile(args[0])
			if err != nil {
				return err
			}
			snap, err := f.Parse(raw)
			if err != nil {
				return err
			}
			res, err := f.Validate(cmd.Context(), snap, formats.ValidateOpts{LiveCheck: opts.LiveCheck})
			if err != nil {
				return err
			}
			summary := f.Redact(snap)
			out := map[string]any{
				"path":       args[0],
				"format":     f.Name(),
				"validation": res,
				"summary":    summary,
			}
			enc := json.NewEncoder(cmd.OutOrStdout())
			enc.SetIndent("", "  ")
			return enc.Encode(out)
		},
	}
	binder.SetTo(cmd.Flags())
	return cmd
}
