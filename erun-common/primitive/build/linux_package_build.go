package build

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/primitive/shared"
)

const (
	linuxPackageTestStage  = "test"
	linuxPackageBuildStage = "build"
)

// linuxPackageCheckTarget is the stage a plain build runs: `test` when the
// package declares one, otherwise `build`, so a package without tests is still
// compiled on every build. A package with neither has nothing a build can run.
func linuxPackageCheckTarget(pkg shared.LinuxPackageSpec) (string, error) {
	for _, stage := range []string{linuxPackageTestStage, linuxPackageBuildStage} {
		if shared.DockerfileDeclaresStage(pkg.DockerfilePath, stage) {
			return stage, nil
		}
	}
	return "", fmt.Errorf("linux package %s declares neither a `test` nor a `build` stage, so `erun build` has nothing to run: %s", pkg.Name, pkg.DockerfilePath)
}

func CheckLinuxPackagesBuildable(packages []shared.LinuxPackageSpec) error {
	for _, pkg := range packages {
		if _, err := linuxPackageCheckTarget(pkg); err != nil {
			return err
		}
	}
	return nil
}

// linuxPackageCheckArgs builds the package's tests (or, without any, its
// build) and exports nothing: no image, no tag, nothing to push.
func linuxPackageCheckArgs(pkg shared.LinuxPackageSpec, target, cgroupParent string) []string {
	args := []string{"build", "--progress=plain", "--target", target, "--output", "type=cacheonly"}
	return append(AppendCgroupParentArg(args, cgroupParent), "-f", pkg.DockerfilePath, ".")
}

func linuxPackageDistArgs(pkg shared.LinuxPackageSpec, version, platform, dest, cgroupParent string) []string {
	args := []string{"build", "--progress=plain", "--platform", platform, "--target", shared.LinuxPackageDistStage, "--build-arg", "ERUN_VERSION=" + version, "--output", "type=local,dest=" + dest}
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

// runLinuxPackageDockerBuild runs one package build on the run's heartbeat:
// below debug verbosity its BuildKit output is captured, so the heartbeat is
// the only sign a long test stage is still working.
func runLinuxPackageDockerBuild(ctx core.Context, pkg shared.LinuxPackageSpec, label string, args []string) error {
	done := ctx.Progress.Begin(label)
	defer done()
	if _, err := runDockerBuildOnce(args, pkg.ContextDir, "", false, ctx.Verbosity, ctx.Stdout, ctx.Stderr); err != nil {
		return fmt.Errorf("linux package %s: %w", pkg.Name, err)
	}
	return nil
}

// RunLinuxPackageChecks runs every package's tests on a plain build and reports
// each outcome the way an image build does. A package whose fingerprint has a
// recorded pass reports `reused` (`none declared` without a `test` stage) and
// runs nothing; one that passes records its fingerprint, and one that fails
// records nothing.
func RunLinuxPackageChecks(ctx core.Context, packages []shared.LinuxPackageSpec) error {
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
		if err := RecordLinuxPackageTestPass(pkg.Name, pkg.Fingerprint, pkg.Platform); err != nil {
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

func reportLinuxPackageReuse(ctx core.Context, pkg shared.LinuxPackageSpec, target string) {
	if target != linuxPackageTestStage {
		ctx.Trace(pkg.Name + ": its " + target + " stage is reused (fingerprint fp-" + pkg.Fingerprint + ")")
	}
	ctx.Info(pkg.Name + ": test: " + linuxPackageTestOutcome(target, "reused (fingerprint fp-"+pkg.Fingerprint+")"))
}

func traceLinuxPackageRunReason(ctx core.Context, pkg shared.LinuxPackageSpec, target string) {
	switch {
	case pkg.NoReuseReason != "":
		ctx.Trace(pkg.Name + ": running its " + target + " stage: " + pkg.NoReuseReason)
	case pkg.Fingerprint != "":
		ctx.Trace(pkg.Name + ": running its " + target + " stage: no recorded pass for fingerprint fp-" + pkg.Fingerprint)
	}
}

// ExportReleaseLinuxPackages builds every package's `dist` stage for every
// release platform into the release's package directory. It runs inside the
// publish, so a package that fails to build stops the release before anything
// is public.
func ExportReleaseLinuxPackages(ctx core.Context, spec shared.ReleaseSpec) error {
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
		for _, platform := range shared.MultiPlatformDockerBuilds {
			args := linuxPackageDistArgs(pkg, spec.Version, platform, shared.LinuxPackageExportDir(spec.LinuxPackageDir, pkg, platform), cgroupParent)
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
