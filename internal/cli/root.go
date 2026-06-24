package cli

import "github.com/spf13/cobra"

var (
	// Version is set at build time via -ldflags.
	Version = "dev"
	// BinarySHA is set at build time via -ldflags.
	BinarySHA = "dev"
	// BinaryBuiltAt is set at build time via -ldflags.
	BinaryBuiltAt = "1970-01-01T00:00:00Z"
)

// NewRootCommand builds the hivebus CLI.
func NewRootCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "hivebus",
		Short: "Secure threaded coordination bus for agents",
		Long: `hivebus is a machine-native coordination layer for agent systems.

It models issue intake, evidence exchange, structured investigation, and
work-order derivation as typed JSON threads instead of ad hoc text blobs.`,
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	cmd.AddCommand(newVersionCommand())
	cmd.AddCommand(newSpecCommand())
	cmd.AddCommand(newSampleCommand())
	cmd.AddCommand(newAskCommand())    // WO-84: signed read-only query/answer primitive.
	cmd.AddCommand(newAnswerCommand()) // WO-95: conservative registered answerer loop.
	cmd.AddCommand(newSayCommand())    // WO-153: standalone generic boardroom send.
	cmd.AddCommand(newInboxCommand())  // WO-153: standalone generic boardroom inbox.
	cmd.AddCommand(newListenCommand()) // WO-153: bounded boardroom inbox polling.
	cmd.AddCommand(newServeCommand())
	cmd.AddCommand(newWatchCommand())

	return cmd
}

// Execute runs the hivebus CLI.
func Execute() error {
	return NewRootCommand().Execute()
}
