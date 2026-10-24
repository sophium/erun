package version

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/sophium/erun/cmd/shared"
	"github.com/sophium/erun/cmd/shared/clicmd"
	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/base/seam"
	"github.com/sophium/erun/erun-common/primitive/shared/buildversion"
	"github.com/spf13/cobra"
)

// VersionCommandInfo keeps erun's build version separate from the current project's VERSION.
type VersionCommandInfo struct {
	Build           model.BuildInfo
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

// NewVersionCmd returns a Cobra command that prints the build information.
func NewVersionCmd(resolveBuildInfo func() (VersionCommandInfo, error), resolveRegistryVersions seam.RuntimeRegistryVersionResolverFunc) *cobra.Command {
	var noRegistry bool
	cmd := &cobra.Command{
		Use:   "version",
		Short: "Print build information",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := clicmd.CommandContext(cmd)
			info, err := resolveBuildInfo()
			if err != nil {
				return err
			}

			ctx.TraceCommand("", "erun", "version")
			if strings.TrimSpace(info.VersionFilePath) != "" {
				ctx.Logger.Debug("resolved project version from " + info.VersionFilePath)
			}

			versions := model.RuntimeRegistryVersions{}
			if !noRegistry {
				versions = resolveRegistryVersionsForVersionCommand(cmd.Context(), ctx, resolveRegistryVersions)
			}
			if ctx.Output == model.OutputJSON {
				return ctx.WriteResult(versionCommandResultFor(info, versions))
			}
			return writeVersionLines(ctx, info, versions)
		},
	}
	clicmd.AddDryRunFlag(cmd)
	cmd.Flags().BoolVar(&noRegistry, "no-registry", false, "Skip remote registry version lookup")
	cmd.Example = "  erun version\n  erun -v version\n  erun version --dry-run"
	cmd.Long = fmt.Sprintf("%s\n\nThe `erun` line is always erun's own build version, so it is comparable with the `latest stable:` line. A project VERSION resolved from the current directory is reported separately on its own `project` line.\n\nVerbosity levels:\n  -v    print trace logs for command flow and side effects\n\nDry-run:\n  --dry-run runs the same resolution flow but skips mutating operations", cmd.Short)
	return cmd
}

func versionCommandResultFor(info VersionCommandInfo, versions model.RuntimeRegistryVersions) versionCommandResult {
	build := core.NormalizeBuildInfo(info.Build)
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

func writeVersionLines(ctx core.Context, info VersionCommandInfo, versions model.RuntimeRegistryVersions) error {
	if _, err := fmt.Fprintln(ctx.Stdout, core.FormatVersionLine(info.Build)); err != nil {
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

func formatProjectVersionLine(info VersionCommandInfo) string {
	version := strings.TrimSpace(info.ProjectVersion)
	if version == "" {
		return ""
	}
	if name := strings.TrimSpace(info.ProjectName); name != "" {
		return "project " + name + " " + version
	}
	return "project " + version
}

// A registry lookup failure is traced, not fatal.
func resolveRegistryVersionsForVersionCommand(ctx context.Context, commandCtx core.Context, resolveRegistryVersions seam.RuntimeRegistryVersionResolverFunc) model.RuntimeRegistryVersions {
	if resolveRegistryVersions == nil {
		return model.RuntimeRegistryVersions{}
	}
	versions, err := resolveRegistryVersions(ctx)
	if err != nil {
		commandCtx.Logger.Debug("resolve runtime registry versions: " + err.Error())
		return model.RuntimeRegistryVersions{}
	}
	return versions
}

func ResolveVersionCommandBuildInfo(findProjectRoot seam.ProjectFinderFunc) (VersionCommandInfo, error) {
	info := VersionCommandInfo{Build: shared.CurrentBuildInfo()}

	buildDir, err := os.Getwd()
	if err != nil {
		return info, err
	}

	_, projectRoot, err := findProjectRoot()
	if err != nil {
		if !errors.Is(err, config.ErrNotInGitRepository) {
			return info, err
		}
		projectRoot = ""
	}

	version, _, versionFilePath, err := buildversion.ResolveDockerBuildVersion(buildDir, projectRoot, "")
	if err != nil {
		if errors.Is(err, ErrVersionFileNotFound) {
			return info, nil
		}
		return info, err
	}

	info.ProjectVersion = version
	info.VersionFilePath = versionFilePath
	info.ProjectName = projectNameForVersionFile(versionFilePath)
	return info, nil
}

// projectNameForVersionFile names the project after the directory holding VERSION.
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
