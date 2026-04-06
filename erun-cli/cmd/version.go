package cmd

import (
	"errors"
	"fmt"
	"os"
	"strings"

	eruncommon "github.com/sophium/erun/erun-common"
	"github.com/spf13/cobra"
)

// These variables are replaced at build time via -ldflags to embed release metadata.
var (
	buildVersion = "dev"
	buildCommit  = ""
	buildDate    = ""
)

// CurrentBuildInfo exposes the current CLI build metadata in the shared common format.
func CurrentBuildInfo() eruncommon.BuildInfo {
	return eruncommon.NormalizeBuildInfo(eruncommon.BuildInfo{
		Version: buildVersion,
		Commit:  buildCommit,
		Date:    buildDate,
	})
}

// BuildInfo exposes the current CLI build metadata.
func BuildInfo() (string, string, string) {
	info := CurrentBuildInfo()
	return info.Version, info.Commit, info.Date
}

// SetBuildInfo overrides the CLI build metadata. Primarily useful for tests.
func SetBuildInfo(v, c, d string) {
	buildVersion = v
	buildCommit = c
	buildDate = d
}

// NewVersionCmd returns a Cobra command that prints the build information.
func NewVersionCmd(deps Dependencies, verbosity *int) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "version",
		Short: "Print build information",
		RunE: func(cmd *cobra.Command, args []string) error {
			info, versionFilePath, err := resolveVersionCommandBuildInfo(deps)
			if err != nil {
				return err
			}

			notes := []string{"decision: printing version information"}
			if strings.TrimSpace(versionFilePath) != "" {
				notes = append(notes, "decision: resolved version="+info.Version+" from VERSION "+versionFilePath)
			} else {
				notes = append(notes, "decision: VERSION file not found; using embedded build metadata")
			}
			emitCommandTrace(cmd, cmd.ErrOrStderr(), CommandTrace{
				Name: "erun",
				Args: []string{"version"},
			}, notes...)
			if isDryRunCommand(cmd) {
				return nil
			}

			logger := eruncommon.NewLoggerWithWriters(internalTraceVerbosity(cmd), cmd.OutOrStdout(), cmd.ErrOrStderr())
			logger.Info(eruncommon.FormatVersionLine(info))
			return nil
		},
	}
	addDryRunFlag(cmd)
	cmd.Example = "  erun version\n  erun -v version\n  erun version --dry-run"
	cmd.Long = fmt.Sprintf("%s\n\nVerbosity levels:\n  -v    print the resolved command plan before execution\n  -vv   add decision notes\n  -vvv  include internal trace logs when available\n\nDry-run:\n  --dry-run prints the resolved command plan without executing it\n  --dry-run -v adds decision notes\n  --dry-run -vv adds internal trace logs when available", cmd.Short)
	return cmd
}

func resolveVersionCommandBuildInfo(deps Dependencies) (eruncommon.BuildInfo, string, error) {
	info := CurrentBuildInfo()

	buildDir, err := os.Getwd()
	if err != nil {
		return info, "", err
	}

	projectRoot, err := resolveDockerBuildProjectRoot(deps)
	if err != nil {
		return info, "", err
	}

	version, _, versionFilePath, err := resolveDockerBuildVersion(buildDir, projectRoot)
	if err != nil {
		if errors.Is(err, errVersionFileNotFound) {
			return info, "", nil
		}
		return info, "", err
	}

	info.Version = version
	return eruncommon.NormalizeBuildInfo(info), versionFilePath, nil
}
