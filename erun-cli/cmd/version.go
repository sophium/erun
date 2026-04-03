package cmd

import (
	eruncommon "github.com/sophium/erun/erun-common"
	"github.com/spf13/cobra"
)

// These variables are replaced at build time via -ldflags to embed release metadata.
var buildInfo = eruncommon.BuildInfo{Version: "dev"}

// CurrentBuildInfo exposes the current CLI build metadata in the shared common format.
func CurrentBuildInfo() eruncommon.BuildInfo {
	return eruncommon.NormalizeBuildInfo(buildInfo)
}

// BuildInfo exposes the current CLI build metadata.
func BuildInfo() (string, string, string) {
	info := CurrentBuildInfo()
	return info.Version, info.Commit, info.Date
}

// SetBuildInfo overrides the CLI build metadata. Primarily useful for tests.
func SetBuildInfo(v, c, d string) {
	buildInfo = eruncommon.BuildInfo{
		Version: v,
		Commit:  c,
		Date:    d,
	}
}

// NewVersionCmd returns a Cobra command that prints the build information.
func NewVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print build information",
		Run: func(cmd *cobra.Command, args []string) {
			cmd.Printf("%s\n", eruncommon.FormatVersionLine(CurrentBuildInfo()))
		},
	}
}
