package cli

import (
	"github.com/obstalabs/hivebus/internal/spec"
	"github.com/spf13/cobra"
)

func newSampleCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "sample-case",
		Short: "Print a nullbot-to-workledger sample thread bundle",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return writeJSON(cmd.OutOrStdout(), spec.SampleCase())
		},
	}
}
