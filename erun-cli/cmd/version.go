package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
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

// versionCommandInfo keeps erun's own build identity separate from the version of
// whatever project the caller happens to stand in. Collapsing the two made
// `erun version` report a project's VERSION as erun's, which reads as a runtime
// stranded many releases behind the `latest stable:` line right beneath it.
type versionCommandInfo struct {
	Build           common.BuildInfo
	ProjectName     string
	ProjectVersion  string
	VersionFilePath string
}

type versionCommandResult struct {
	Erun           versionBuildResult    `json:"erun"`
	Project        *versionProjectResult `json:"project,omitempty"`
	LatestStable   string                `json:"latestStable,omitempty"`
	LatestSnapshot string                `json:"latestSnapshot,omitempty"`
}

type versionBuildResult struct {
	Version string `json:"version"`
	Commit  string `json:"commit,omitempty"`
	Date    string `json:"date,omitempty"`
}

type versionProjectResult struct {
	Name        string `json:"name,omitempty"`
	Version     string `json:"version"`
	VersionFile string `json:"versionFile,omitempty"`
}

// newVersionCmd returns a Cobra command that prints the build information.
func newVersionCmd(resolveBuildInfo func() (versionCommandInfo, error), resolveRegistryVersions common.RuntimeRegistryVersionResolverFunc) *cobra.Command {
	var noRegistry bool
	cmd := &cobra.Command{
		Use:   "version",
		Short: "Print build information",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := commandContext(cmd)
			info, err := resolveBuildInfo()
			if err != nil {
				return err
			}

			ctx.TraceCommand("", "erun", "version")
			if strings.TrimSpace(info.VersionFilePath) != "" {
				ctx.Logger.Debug("resolved project version from " + info.VersionFilePath)
			}

			versions := common.RuntimeRegistryVersions{}
			if !noRegistry {
				versions = resolveRegistryVersionsForVersionCommand(cmd.Context(), ctx, resolveRegistryVersions)
			}
			if ctx.Output == common.OutputJSON {
				return ctx.WriteResult(versionCommandResultFor(info, versions))
			}
			return writeVersionLines(ctx, info, versions)
		},
	}
	addDryRunFlag(cmd)
	cmd.Flags().BoolVar(&noRegistry, "no-registry", false, "Skip remote registry version lookup")
	cmd.Example = "  erun version\n  erun -v version\n  erun version --dry-run"
	cmd.Long = fmt.Sprintf("%s\n\nThe `erun` line is always erun's own build version, so it is comparable with the `latest stable:` line. A project VERSION resolved from the current directory is reported separately on its own `project` line.\n\nVerbosity levels:\n  -v    print trace logs for command flow and side effects\n\nDry-run:\n  --dry-run runs the same resolution flow but skips mutating operations", cmd.Short)
	return cmd
}

func versionCommandResultFor(info versionCommandInfo, versions common.RuntimeRegistryVersions) versionCommandResult {
	build := common.NormalizeBuildInfo(info.Build)
	result := versionCommandResult{
		Erun: versionBuildResult{
			Version: build.Version,
			Commit:  build.Commit,
			Date:    build.Date,
		},
		LatestStable:   strings.TrimSpace(versions.LatestStable),
		LatestSnapshot: strings.TrimSpace(versions.LatestSnapshot),
	}
	if version := strings.TrimSpace(info.ProjectVersion); version != "" {
		result.Project = &versionProjectResult{
			Name:        strings.TrimSpace(info.ProjectName),
			Version:     version,
			VersionFile: strings.TrimSpace(info.VersionFilePath),
		}
	}
	return result
}

func writeVersionLines(ctx common.Context, info versionCommandInfo, versions common.RuntimeRegistryVersions) error {
	if _, err := fmt.Fprintln(ctx.Stdout, common.FormatVersionLine(info.Build)); err != nil {
		return err
	}
	if line := formatProjectVersionLine(info); line != "" {
		if _, err := fmt.Fprintln(ctx.Stdout, line); err != nil {
			return err
		}
	}
	if stable := strings.TrimSpace(versions.LatestStable); stable != "" {
		if _, err := fmt.Fprintln(ctx.Stdout, "latest stable: "+stable); err != nil {
			return err
		}
	}
	if snapshot := strings.TrimSpace(versions.LatestSnapshot); snapshot != "" {
		if _, err := fmt.Fprintln(ctx.Stdout, "latest snapshot: "+snapshot); err != nil {
			return err
		}
	}
	return nil
}

func formatProjectVersionLine(info versionCommandInfo) string {
	version := strings.TrimSpace(info.ProjectVersion)
	if version == "" {
		return ""
	}
	if name := strings.TrimSpace(info.ProjectName); name != "" {
		return "project " + name + " " + version
	}
	return "project " + version
}

// A registry lookup failure is not fatal: the local build version is still worth
// printing, so the error is traced and the lines are simply omitted.
func resolveRegistryVersionsForVersionCommand(ctx context.Context, commandCtx common.Context, resolveRegistryVersions common.RuntimeRegistryVersionResolverFunc) common.RuntimeRegistryVersions {
	if resolveRegistryVersions == nil {
		return common.RuntimeRegistryVersions{}
	}
	versions, err := resolveRegistryVersions(ctx)
	if err != nil {
		commandCtx.Logger.Debug("resolve runtime registry versions: " + err.Error())
		return common.RuntimeRegistryVersions{}
	}
	return versions
}

func resolveVersionCommandBuildInfo(findProjectRoot common.ProjectFinderFunc) (versionCommandInfo, error) {
	info := versionCommandInfo{Build: currentBuildInfo()}

	buildDir, err := os.Getwd()
	if err != nil {
		return info, err
	}

	_, projectRoot, err := findProjectRoot()
	if err != nil {
		if !errors.Is(err, common.ErrNotInGitRepository) {
			return info, err
		}
		projectRoot = ""
	}

	version, _, versionFilePath, err := common.ResolveDockerBuildVersion(buildDir, projectRoot, "")
	if err != nil {
		if errors.Is(err, errVersionFileNotFound) {
			return info, nil
		}
		return info, err
	}

	info.ProjectVersion = version
	info.VersionFilePath = versionFilePath
	info.ProjectName = projectNameForVersionFile(versionFilePath)
	return info, nil
}

// The project's name is taken from the directory holding the VERSION file, which
// is what an operator recognizes it by.
func projectNameForVersionFile(versionFilePath string) string {
	dir := strings.TrimSpace(filepath.Dir(strings.TrimSpace(versionFilePath)))
	if dir == "" || dir == "." || dir == string(filepath.Separator) {
		return ""
	}
	name := filepath.Base(dir)
	if name == "." || name == string(filepath.Separator) {
		return ""
	}
	return name
}
