package cli

import (
	"github.com/ppiankov/hivebus/internal/spec"
	"github.com/spf13/cobra"
)

func newSpecCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "spec",
		Short: "Print the v0 Hivebus protocol contract",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return writeJSON(cmd.OutOrStdout(), spec.V0())
		},
	}
}
