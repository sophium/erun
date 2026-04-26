package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	common "github.com/sophium/erun/erun-common"
	"github.com/spf13/cobra"
)

// These variables are replaced at build time via -ldflags to embed release metadata.
var (
	buildVersion = "dev"
	buildCommit  = ""
	buildDate    = ""
)

func currentBuildInfo() common.BuildInfo {
	return common.NormalizeBuildInfo(common.BuildInfo{
		Version: buildVersion,
		Commit:  buildCommit,
		Date:    buildDate,
	})
}

func buildInfo() (string, string, string) {
	info := currentBuildInfo()
	return info.Version, info.Commit, info.Date
}

func setBuildInfo(v, c, d string) {
	buildVersion = v
	buildCommit = c
	buildDate = d
}

// newVersionCmd returns a Cobra command that prints the build information.
func newVersionCmd(resolveBuildInfo func() (common.BuildInfo, string, error), resolveRegistryVersions common.RuntimeRegistryVersionResolverFunc) *cobra.Command {
	var noRegistry bool
	cmd := &cobra.Command{
		Use:   "version",
		Short: "Print build information",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := commandContext(cmd)
			info, versionFilePath, err := resolveBuildInfo()
			if err != nil {
				return err
			}

			ctx.TraceCommand("", "erun", "version")
			if strings.TrimSpace(versionFilePath) != "" {
				ctx.Logger.Debug("resolved version from " + versionFilePath)
			}

			if _, writeErr := fmt.Fprintln(ctx.Stdout, common.FormatVersionLine(info)); writeErr != nil {
				return writeErr
			}
			if noRegistry {
				return nil
			}
			return writeRegistryVersions(cmd.Context(), ctx, resolveRegistryVersions)
		},
	}
	addDryRunFlag(cmd)
	cmd.Flags().BoolVar(&noRegistry, "no-registry", false, "Skip remote registry version lookup")
	cmd.Example = "  erun version\n  erun -v version\n  erun version --dry-run"
	cmd.Long = fmt.Sprintf("%s\n\nVerbosity levels:\n  -v    print trace logs for command flow and side effects\n\nDry-run:\n  --dry-run runs the same resolution flow but skips mutating operations", cmd.Short)
	return cmd
}

func writeRegistryVersions(ctx context.Context, commandCtx common.Context, resolveRegistryVersions common.RuntimeRegistryVersionResolverFunc) error {
	if resolveRegistryVersions == nil {
		return nil
	}
	versions, err := resolveRegistryVersions(ctx)
	if err != nil {
		commandCtx.Logger.Debug("resolve runtime registry versions: " + err.Error())
		return nil
	}
	if stable := strings.TrimSpace(versions.LatestStable); stable != "" {
		if _, err := fmt.Fprintln(commandCtx.Stdout, "latest stable: "+stable); err != nil {
			return err
		}
	}
	if snapshot := strings.TrimSpace(versions.LatestSnapshot); snapshot != "" {
		if _, err := fmt.Fprintln(commandCtx.Stdout, "latest snapshot: "+snapshot); err != nil {
			return err
		}
	}
	return nil
}

func resolveVersionCommandBuildInfo(findProjectRoot common.ProjectFinderFunc) (common.BuildInfo, string, error) {
	info := currentBuildInfo()

	buildDir, err := os.Getwd()
	if err != nil {
		return info, "", err
	}

	_, projectRoot, err := findProjectRoot()
	if err != nil {
		if !errors.Is(err, common.ErrNotInGitRepository) {
			return info, "", err
		}
		projectRoot = ""
	}

	version, _, versionFilePath, err := common.ResolveDockerBuildVersion(buildDir, projectRoot)
	if err != nil {
		if errors.Is(err, errVersionFileNotFound) {
			return info, "", nil
		}
		return info, "", err
	}

	info.Version = version
	return common.NormalizeBuildInfo(info), versionFilePath, nil
}
