package cli

import (
	"encoding/json"
	"io"

	"github.com/spf13/cobra"
)

type versionOutput struct {
	Version   string `json:"version"`
	Commit    string `json:"commit"`
	BuildDate string `json:"build_date"`
}

func newVersionCommand() *cobra.Command {
	var jsonOutput bool

	cmd := &cobra.Command{
		Use:   "version",
		Short: "Print build information",
		RunE: func(cmd *cobra.Command, _ []string) error {
			out := versionOutput{
				Version:   Version,
				Commit:    Commit,
				BuildDate: BuildDate,
			}

			if jsonOutput {
				return writeJSON(cmd.OutOrStdout(), out)
			}

			_, err := io.WriteString(
				cmd.OutOrStdout(),
				"hivebus "+Version+" ("+Commit+") built "+BuildDate+"\n",
			)
			return err
		},
	}

	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Emit build information as JSON")

	return cmd
}

func writeJSON(w io.Writer, value any) error {
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")

	return encoder.Encode(value)
}
