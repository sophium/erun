package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

var (
	// These variables are replaced at build time via -ldflags to embed release metadata.
	version = "dev"
	commit  = ""
	date    = ""
)

// BuildInfo exposes the current CLI build metadata.
func BuildInfo() (string, string, string) {
	return version, commit, date
}

// SetBuildInfo overrides the CLI build metadata. Primarily useful for tests.
func SetBuildInfo(v, c, d string) {
	version, commit, date = v, c, d
}

// NewVersionCmd returns a Cobra command that prints the build information.
func NewVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print build information",
		Run: func(cmd *cobra.Command, args []string) {
			cmd.Printf("erun %s", version)
			tail := ""
			if commit != "" {
				tail = fmt.Sprintf(" (%s", commit)
				if date != "" {
					tail += fmt.Sprintf(" built %s", date)
				}
				tail += ")"
			}
			cmd.Printf("%s\n", tail)
		},
	}
}
