package cli

import (
	"encoding/json"
	"io"
	"runtime/debug"

	"github.com/spf13/cobra"
)

type versionOutput struct {
	Version   string `json:"version"`
	Commit    string `json:"commit"`
	BuildDate string `json:"build_date"`
}

// resolveVersion prefers the LDFLAGS-injected version (from `make build`); when that is
// absent — a plain `go install module@version` build — it falls back to the module version
// embedded in the binary by the Go toolchain, so a released binary reports its real version
// instead of the "dev" default.
func resolveVersion() string {
	if Version != "dev" {
		return Version
	}
	if info, ok := debug.ReadBuildInfo(); ok {
		if v := info.Main.Version; v != "" && v != "(devel)" {
			return v
		}
	}
	return Version
}

func newVersionCommand() *cobra.Command {
	var jsonOutput bool

	cmd := &cobra.Command{
		Use:   "version",
		Short: "Print build information",
		RunE: func(cmd *cobra.Command, _ []string) error {
			resolved := resolveVersion()
			out := versionOutput{
				Version:   resolved,
				Commit:    Commit,
				BuildDate: BuildDate,
			}

			if jsonOutput {
				return writeJSON(cmd.OutOrStdout(), out)
			}

			_, err := io.WriteString(
				cmd.OutOrStdout(),
				"hivebus "+resolved+" ("+Commit+") built "+BuildDate+"\n",
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
