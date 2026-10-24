package eruncommon

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
)

// LinuxPackageSpec is one linux package component: a Dockerfile under
// <tenant>-devops/linux/<name>/, built with the project root as its context.
// It never produces an image. A plain build runs it through its `test` stage
// to a cache-only output; `erun build --release` exports its `dist` stage for
// every release platform and uploads the .deb files at the root of that export
// to the GitHub release for the tag.
type LinuxPackageSpec struct {
	Name           string `json:"name"`
	ContextDir     string `json:"contextDir"`
	DockerfilePath string `json:"dockerfilePath"`
	// Fingerprint, Platform and Reuse are a plain build's reuse decision
	// (applyLinuxPackageReuse): Reuse skips the check stage because a pass is
	// recorded for this fingerprint on the docker daemon's platform. A release
	// never sets them.
	Fingerprint string `json:"fingerprint,omitempty"`
	Platform    string `json:"platform,omitempty"`
	Reuse       bool   `json:"reuse,omitempty"`
	// noReuseReason is why a plain build can neither reuse nor record a pass
	// for the package, so the trace says why it runs.
	noReuseReason string
}

const (
	linuxPackageTestStage  = "test"
	linuxPackageBuildStage = "build"
	linuxPackageDistStage  = "dist"
)

// newLinuxPackageSpecs builds the package specs for contexts, with the project
// root as their docker build context. Without a resolvable project root the
// convention <root>/<tenant>-devops/linux/<name> names it.
func newLinuxPackageSpecs(projectRoot string, contexts []LinuxPackageContext) []LinuxPackageSpec {
	specs := make([]LinuxPackageSpec, 0, len(contexts))
	for _, context := range contexts {
		contextDir := filepath.Clean(strings.TrimSpace(projectRoot))
		if strings.TrimSpace(projectRoot) == "" {
			contextDir = filepath.Dir(filepath.Dir(filepath.Dir(context.Dir)))
		}
		specs = append(specs, LinuxPackageSpec{
			Name:           filepath.Base(context.Dir),
			ContextDir:     contextDir,
			DockerfilePath: context.DockerfilePath,
		})
	}
	return specs
}

func dockerfileDeclaresStage(dockerfilePath, stage string) bool {
	data, err := os.ReadFile(dockerfilePath)
	if err != nil {
		return false
	}
	pattern := regexp.MustCompile(`(?im)^\s*FROM\s+.*\bAS\s+` + regexp.QuoteMeta(stage) + `(?:\s|$)`)
	return pattern.Match(data)
}

// linuxPackageCheckTarget is the stage a plain build runs: `test` when the
// package declares one, otherwise `build`, so a package without tests is still
// compiled on every build. A package with neither has nothing a build can run.
func linuxPackageCheckTarget(pkg LinuxPackageSpec) (string, error) {
	for _, stage := range []string{linuxPackageTestStage, linuxPackageBuildStage} {
		if dockerfileDeclaresStage(pkg.DockerfilePath, stage) {
			return stage, nil
		}
	}
	return "", fmt.Errorf("linux package %s declares neither a `test` nor a `build` stage, so `erun build` has nothing to run: %s", pkg.Name, pkg.DockerfilePath)
}

func checkLinuxPackagesBuildable(packages []LinuxPackageSpec) error {
	for _, pkg := range packages {
		if _, err := linuxPackageCheckTarget(pkg); err != nil {
			return err
		}
	}
	return nil
}

func checkLinuxPackagesReleasable(packages []LinuxPackageSpec) error {
	for _, pkg := range packages {
		if !dockerfileDeclaresStage(pkg.DockerfilePath, linuxPackageDistStage) {
			return fmt.Errorf("linux package %s declares no `dist` stage, so a release has no .deb files to publish: %s", pkg.Name, pkg.DockerfilePath)
		}
	}
	return nil
}

// linuxPackageCheckArgs builds the package's tests (or, without any, its
// build) and exports nothing: no image, no tag, nothing to push.
func linuxPackageCheckArgs(pkg LinuxPackageSpec, target, cgroupParent string) []string {
	args := []string{"build", "--progress=plain", "--target", target, "--output", "type=cacheonly"}
	return append(AppendCgroupParentArg(args, cgroupParent), "-f", pkg.DockerfilePath, ".")
}

func linuxPackageDistArgs(pkg LinuxPackageSpec, version, platform, dest, cgroupParent string) []string {
	args := []string{"build", "--progress=plain", "--platform", platform, "--target", linuxPackageDistStage, "--build-arg", "ERUN_VERSION=" + version, "--output", "type=local,dest=" + dest}
	return append(AppendCgroupParentArg(args, cgroupParent), "-f", pkg.DockerfilePath, ".")
}

// AppendCgroupParentArg nests a build's RUN containers under the environment's
// CPU cap (buildContainerCPUCapCgroupParent), as every image build does. A
// package's test stage is as heavy as any image's, and without it the build
// escapes the sidecar's limit and can take the whole node.
func AppendCgroupParentArg(args []string, cgroupParent string) []string {
	if cgroupParent == "" {
		return args
	}
	return append(args, "--cgroup-parent", cgroupParent)
}

func linuxPackageExportDir(root string, pkg LinuxPackageSpec, platform string) string {
	return filepath.Join(root, pkg.Name, filepath.FromSlash(platform))
}

// releaseLinuxPackageDir is where a release exports its packages: outside the
// worktree, which a release keeps clean, and per project and version.
func releaseLinuxPackageDir(projectRoot, version string) string {
	cacheDir, err := os.UserCacheDir()
	if err != nil || strings.TrimSpace(cacheDir) == "" {
		cacheDir = os.TempDir()
	}
	return filepath.Join(cacheDir, "erun", "release", filepath.Base(filepath.Clean(projectRoot)), version, "linux")
}

// runLinuxPackageDockerBuild runs one package build on the run's heartbeat:
// below debug verbosity its BuildKit output is captured, so the heartbeat is
// the only sign a long test stage is still working.
func runLinuxPackageDockerBuild(ctx core.Context, pkg LinuxPackageSpec, label string, args []string) error {
	done := ctx.Progress.Begin(label)
	defer done()
	if _, err := runDockerBuildOnce(args, pkg.ContextDir, "", false, ctx.Verbosity, ctx.Stdout, ctx.Stderr); err != nil {
		return fmt.Errorf("linux package %s: %w", pkg.Name, err)
	}
	return nil
}

// runLinuxPackageChecks runs every package's tests on a plain build and reports
// each outcome the way an image build does. A package whose fingerprint has a
// recorded pass reports `reused` (`none declared` without a `test` stage) and
// runs nothing; one that passes records its fingerprint, and one that fails
// records nothing.
func runLinuxPackageChecks(ctx core.Context, packages []LinuxPackageSpec) error {
	ctx, stopProgress := withBuildProgress(ctx)
	defer stopProgress()
	cgroupParent := core.BuildContainerCPUCapCgroupParent()
	for _, pkg := range packages {
		target, err := linuxPackageCheckTarget(pkg)
		if err != nil {
			return err
		}
		if pkg.Reuse && pkg.Fingerprint != "" {
			reportLinuxPackageReuse(ctx, pkg, target)
			continue
		}
		traceLinuxPackageRunReason(ctx, pkg, target)
		args := linuxPackageCheckArgs(pkg, target, cgroupParent)
		ctx.TraceCommand(pkg.ContextDir, "docker", args...)
		if ctx.DryRun {
			continue
		}
		_, finish := ctx.StartTimingStep(pkg.Name)
		err = runLinuxPackageDockerBuild(ctx, pkg, pkg.Name, args)
		finish(err)
		if err != nil {
			return err
		}
		ctx.Info(pkg.Name + ": test: " + linuxPackageTestOutcome(target, "passed"))
		if pkg.Fingerprint == "" || pkg.Platform == "" {
			continue
		}
		// The pass already happened; failing to remember it costs only a rerun.
		if err := recordLinuxPackageTestPass(pkg.Name, pkg.Fingerprint, pkg.Platform); err != nil {
			ctx.Trace(pkg.Name + ": could not record the pass for fingerprint fp-" + pkg.Fingerprint + ", so the next build runs it again: " + err.Error())
		}
	}
	return nil
}

// linuxPackageTestOutcome is the outcome line for a package's check target: a
// package without a `test` stage has no tests to have passed or to reuse, on
// the build that ran its `build` stage and on every build that reuses it.
func linuxPackageTestOutcome(target, tested string) string {
	if target != linuxPackageTestStage {
		return "none declared"
	}
	return tested
}

func reportLinuxPackageReuse(ctx core.Context, pkg LinuxPackageSpec, target string) {
	if target != linuxPackageTestStage {
		ctx.Trace(pkg.Name + ": its " + target + " stage is reused (fingerprint fp-" + pkg.Fingerprint + ")")
	}
	ctx.Info(pkg.Name + ": test: " + linuxPackageTestOutcome(target, "reused (fingerprint fp-"+pkg.Fingerprint+")"))
}

func traceLinuxPackageRunReason(ctx core.Context, pkg LinuxPackageSpec, target string) {
	switch {
	case pkg.noReuseReason != "":
		ctx.Trace(pkg.Name + ": running its " + target + " stage: " + pkg.noReuseReason)
	case pkg.Fingerprint != "":
		ctx.Trace(pkg.Name + ": running its " + target + " stage: no recorded pass for fingerprint fp-" + pkg.Fingerprint)
	}
}

// exportReleaseLinuxPackages builds every package's `dist` stage for every
// release platform into the release's package directory. It runs inside the
// publish, so a package that fails to build stops the release before anything
// is public.
func exportReleaseLinuxPackages(ctx core.Context, spec ReleaseSpec) error {
	ctx, stopProgress := withBuildProgress(ctx)
	defer stopProgress()
	cgroupParent := core.BuildContainerCPUCapCgroupParent()
	for _, pkg := range spec.LinuxPackages {
		if !ctx.DryRun {
			// A retried release must not upload what an earlier attempt exported.
			if err := os.RemoveAll(filepath.Join(spec.LinuxPackageDir, pkg.Name)); err != nil {
				return err
			}
		}
		for _, platform := range multiPlatformDockerBuilds {
			args := linuxPackageDistArgs(pkg, spec.Version, platform, linuxPackageExportDir(spec.LinuxPackageDir, pkg, platform), cgroupParent)
			ctx.TraceCommand(pkg.ContextDir, "docker", args...)
			if ctx.DryRun {
				continue
			}
			label := pkg.Name + " " + platform
			_, finish := ctx.StartTimingStep(label)
			err := runLinuxPackageDockerBuild(ctx, pkg, label, args)
			finish(err)
			if err != nil {
				return err
			}
		}
	}
	return nil
}

// releaseLinuxPackageFiles lists the .deb files at the root of every exported
// `dist` stage. Only the root is read, so a stage that is a whole filesystem
// cannot publish a package some tool happened to cache inside it.
func releaseLinuxPackageFiles(spec ReleaseSpec) ([]string, error) {
	files := make([]string, 0, len(spec.LinuxPackages)*len(multiPlatformDockerBuilds))
	for _, pkg := range spec.LinuxPackages {
		for _, platform := range multiPlatformDockerBuilds {
			dir := linuxPackageExportDir(spec.LinuxPackageDir, pkg, platform)
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

func linuxPackageDryRunFiles(spec ReleaseSpec) []string {
	files := make([]string, 0, len(spec.LinuxPackages)*len(multiPlatformDockerBuilds))
	for _, pkg := range spec.LinuxPackages {
		for _, platform := range multiPlatformDockerBuilds {
			files = append(files, filepath.Join(linuxPackageExportDir(spec.LinuxPackageDir, pkg, platform), "*.deb"))
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

// uploadReleaseLinuxPackages attaches the exported .deb files to the GitHub
// release for the version's tag, creating the release when it does not exist
// yet. It runs after the tag is public, because creating a release for a tag
// the remote does not have would make GitHub tag the default branch instead.
func uploadReleaseLinuxPackages(ctx core.Context, spec ReleaseSpec) error {
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
	files, err := releaseLinuxPackageFiles(spec)
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

// newLinuxPackageUploadError names what is already public, so the remedy is to
// finish the upload rather than to undo the release.
func newLinuxPackageUploadError(spec ReleaseSpec, err error) error {
	return errors.Join(err, fmt.Errorf("the linux packages for v%s were not attached to its GitHub release; the tag, images and charts are already public, so do not delete the tag: attach the .deb files under %s with `gh release upload v%s <files> --clobber` (or `gh release create v%s <files> --generate-notes` when the release does not exist)",
		spec.Version, spec.LinuxPackageDir, spec.Version, spec.Version))
}
