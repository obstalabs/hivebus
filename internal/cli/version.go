package cli

import (
	"encoding/json"
	"io"
	"runtime/debug"
	"strings"

	"github.com/spf13/cobra"
)

type versionOutput struct {
	Version       string `json:"version"`
	BinarySHA     string `json:"binary_sha"`
	BinaryBuiltAt string `json:"binary_built_at"`
}

// resolveVersion prefers the LDFLAGS-injected version (from `make build`); when that is
// absent — a plain `go install module@version` build — it falls back to the module version
// embedded in the binary by the Go toolchain, so a released binary reports its real version
// instead of the "dev" default.
func resolveVersion() string {
	if Version != "dev" {
		return strings.TrimPrefix(Version, "v")
	}
	if info, ok := debug.ReadBuildInfo(); ok {
		if v := info.Main.Version; v != "" && v != "(devel)" {
			return strings.TrimPrefix(v, "v")
		}
	}
	return Version
}

func currentBuildInfo() versionOutput {
	out := versionOutput{
		Version:       strings.TrimSpace(resolveVersion()),
		BinarySHA:     strings.TrimSpace(BinarySHA),
		BinaryBuiltAt: strings.TrimSpace(BinaryBuiltAt),
	}
	if out.Version == "" {
		out.Version = "dev"
	}
	if out.BinarySHA == "" {
		out.BinarySHA = "dev"
	}
	if out.BinaryBuiltAt == "" {
		out.BinaryBuiltAt = "1970-01-01T00:00:00Z"
	}
	return out
}

func newVersionCommand() *cobra.Command {
	var jsonOutput bool

	cmd := &cobra.Command{
		Use:   "version",
		Short: "Print build information",
		RunE: func(cmd *cobra.Command, _ []string) error {
			out := currentBuildInfo()

			if jsonOutput {
				return writeJSON(cmd.OutOrStdout(), out)
			}

			_, err := io.WriteString(
				cmd.OutOrStdout(),
				"hivebus "+out.Version+"\n"+
					"binary_sha "+out.BinarySHA+"\n"+
					"binary_built_at "+out.BinaryBuiltAt+"\n",
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
