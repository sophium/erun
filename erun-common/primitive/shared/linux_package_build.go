package shared

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
)

const (
	LinuxPackageDistStage = "dist"
)

// NewLinuxPackageSpecs builds the package specs for contexts, rooted at the project root.
func NewLinuxPackageSpecs(projectRoot string, contexts []LinuxPackageContext) []model.LinuxPackageSpec {
	specs := make([]model.LinuxPackageSpec, 0, len(contexts))
	for _, context := range contexts {
		contextDir := filepath.Clean(strings.TrimSpace(projectRoot))
		if strings.TrimSpace(projectRoot) == "" {
			contextDir = filepath.Dir(filepath.Dir(filepath.Dir(context.Dir)))
		}
		specs = append(specs, model.LinuxPackageSpec{
			Name:           filepath.Base(context.Dir),
			ContextDir:     contextDir,
			DockerfilePath: context.DockerfilePath,
		})
	}
	return specs
}

func DockerfileDeclaresStage(dockerfilePath, stage string) bool {
	data, err := os.ReadFile(dockerfilePath)
	if err != nil {
		return false
	}
	pattern := regexp.MustCompile(`(?im)^\s*FROM\s+.*\bAS\s+` + regexp.QuoteMeta(stage) + `(?:\s|$)`)
	return pattern.Match(data)
}

func checkLinuxPackagesReleasable(packages []model.LinuxPackageSpec) error {
	for _, pkg := range packages {
		if !DockerfileDeclaresStage(pkg.DockerfilePath, LinuxPackageDistStage) {
			return fmt.Errorf("linux package %s declares no `dist` stage, so a release has no .deb files to publish: %s", pkg.Name, pkg.DockerfilePath)
		}
	}
	return nil
}

func LinuxPackageExportDir(root string, pkg model.LinuxPackageSpec, platform string) string {
	return filepath.Join(root, pkg.Name, filepath.FromSlash(platform))
}

// releaseLinuxPackageDir is outside the worktree, which a release keeps clean.
func releaseLinuxPackageDir(projectRoot, version string) string {
	cacheDir, err := os.UserCacheDir()
	if err != nil || strings.TrimSpace(cacheDir) == "" {
		cacheDir = os.TempDir()
	}
	return filepath.Join(cacheDir, "erun", "release", filepath.Base(filepath.Clean(projectRoot)), version, "linux")
}

// ReleaseLinuxPackageFiles lists the .deb files at the root of every exported dist stage only.
func ReleaseLinuxPackageFiles(spec model.ReleaseSpec) ([]string, error) {
	files := make([]string, 0, len(spec.LinuxPackages)*len(MultiPlatformDockerBuilds))
	for _, pkg := range spec.LinuxPackages {
		for _, platform := range MultiPlatformDockerBuilds {
			dir := LinuxPackageExportDir(spec.LinuxPackageDir, pkg, platform)
			matches, err := filepath.Glob(filepath.Join(dir, "*.deb"))
			if err != nil {
				return nil, err
			}
			if len(matches) == 0 {
				return nil, fmt.Errorf("linux package %s: its `dist` stage exported no .deb file at its root for %s (%s)", pkg.Name, platform, dir)
			}
			files = append(files, matches...)
		}
	}
	slices.Sort(files)
	return files, nil
}

func linuxPackageDryRunFiles(spec model.ReleaseSpec) []string {
	files := make([]string, 0, len(spec.LinuxPackages)*len(MultiPlatformDockerBuilds))
	for _, pkg := range spec.LinuxPackages {
		for _, platform := range MultiPlatformDockerBuilds {
			files = append(files, filepath.Join(LinuxPackageExportDir(spec.LinuxPackageDir, pkg, platform), "*.deb"))
		}
	}
	return files
}

func githubReleaseUploadArgs(tag string, files []string) []string {
	args := append([]string{"release", "upload", tag}, files...)
	return append(args, "--clobber")
}

func githubReleaseCreateArgs(tag, version string, files []string) []string {
	args := append([]string{"release", "create", tag}, files...)
	args = append(args, "--title", "Release "+version)
	if strings.Contains(version, "-rc.") || strings.Contains(version, "-pr.") {
		args = append(args, "--prerelease")
	}
	return append(args, "--generate-notes")
}

// UploadReleaseLinuxPackages attaches the .deb files to the tag's GitHub release. It must run
// after the tag is public, or GitHub would tag the default branch.
func UploadReleaseLinuxPackages(ctx core.Context, spec model.ReleaseSpec) error {
	if len(spec.LinuxPackages) == 0 {
		return nil
	}
	tag := "v" + spec.Version
	ctx.Trace("stage: upload-linux-packages")
	viewArgs := []string{"release", "view", tag}
	ctx.TraceCommand(spec.ProjectRoot, "gh", viewArgs...)
	if ctx.DryRun {
		files := linuxPackageDryRunFiles(spec)
		ctx.Trace("github release " + tag + ": upload the exported .deb files when it exists, otherwise create it with them")
		ctx.TraceCommand(spec.ProjectRoot, "gh", githubReleaseUploadArgs(tag, files)...)
		ctx.TraceCommand(spec.ProjectRoot, "gh", githubReleaseCreateArgs(tag, spec.Version, files)...)
		return nil
	}
	files, err := ReleaseLinuxPackageFiles(spec)
	if err != nil {
		return newLinuxPackageUploadError(spec, err)
	}
	args := githubReleaseCreateArgs(tag, spec.Version, files)
	if runGitHubCLI(spec.ProjectRoot, viewArgs, io.Discard, io.Discard) == nil {
		args = githubReleaseUploadArgs(tag, files)
	}
	ctx.TraceCommand(spec.ProjectRoot, "gh", args...)
	if err := runGitHubCLI(spec.ProjectRoot, args, ctx.Stdout, ctx.Stderr); err != nil {
		return newLinuxPackageUploadError(spec, err)
	}
	return nil
}

func runGitHubCLI(dir string, args []string, stdout, stderr io.Writer) error {
	cmd := core.Command("gh", args...)
	cmd.Dir = dir
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	return cmd.Run()
}

// newLinuxPackageUploadError names what is already public, so the remedy is to finish the upload.
func newLinuxPackageUploadError(spec model.ReleaseSpec, err error) error {
	return errors.Join(err, fmt.Errorf("the linux packages for v%s were not attached to its GitHub release; the tag, images and charts are already public, so do not delete the tag: attach the .deb files under %s with `gh release upload v%s <files> --clobber` (or `gh release create v%s <files> --generate-notes` when the release does not exist)",
		spec.Version, spec.LinuxPackageDir, spec.Version, spec.Version))
}
