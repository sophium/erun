package build

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/primitive/shared"
)

const (
	linuxPackageTestStage  = "test"
	linuxPackageBuildStage = "build"
)

// linuxPackageCheckTarget is `test` when declared, otherwise `build`, so untested packages still compile.
func linuxPackageCheckTarget(pkg model.LinuxPackageSpec) (string, error) {
	for _, stage := range []string{linuxPackageTestStage, linuxPackageBuildStage} {
		if shared.DockerfileDeclaresStage(pkg.DockerfilePath, stage) {
			return stage, nil
		}
	}
	return "", fmt.Errorf("linux package %s declares neither a `test` nor a `build` stage, so `erun build` has nothing to run: %s", pkg.Name, pkg.DockerfilePath)
}

func CheckLinuxPackagesBuildable(packages []model.LinuxPackageSpec) error {
	for _, pkg := range packages {
		if _, err := linuxPackageCheckTarget(pkg); err != nil {
			return err
		}
	}
	return nil
}

func linuxPackageCheckArgs(pkg model.LinuxPackageSpec, target, cgroupParent string) []string {
	args := []string{"build", "--progress=plain", "--target", target, "--output", "type=cacheonly"}
	return append(AppendCgroupParentArg(args, cgroupParent), "-f", pkg.DockerfilePath, ".")
}

func linuxPackageDistArgs(pkg model.LinuxPackageSpec, version, platform, dest, cgroupParent string) []string {
	args := []string{"build", "--progress=plain", "--platform", platform, "--target", shared.LinuxPackageDistStage, "--build-arg", "ERUN_VERSION=" + version, "--output", "type=local,dest=" + dest}
	return append(AppendCgroupParentArg(args, cgroupParent), "-f", pkg.DockerfilePath, ".")
}

// AppendCgroupParentArg nests a build's RUN containers under the environment's CPU cap;
// without it a heavy test stage escapes the sidecar's limit.
func AppendCgroupParentArg(args []string, cgroupParent string) []string {
	if cgroupParent == "" {
		return args
	}
	return append(args, "--cgroup-parent", cgroupParent)
}

// Below debug verbosity output is captured, so the heartbeat is the only liveness signal.
func runLinuxPackageDockerBuild(ctx core.Context, pkg model.LinuxPackageSpec, label string, args []string) error {
	done := ctx.Progress.Begin(label)
	defer done()
	if _, err := runDockerBuildOnce(args, pkg.ContextDir, "", false, ctx.Verbosity, ctx.Stdout, ctx.Stderr); err != nil {
		return fmt.Errorf("linux package %s: %w", pkg.Name, err)
	}
	return nil
}

// RunLinuxPackageChecks runs every package's tests on a plain build. A recorded pass for the
// fingerprint is reused; only a pass is recorded.
func RunLinuxPackageChecks(ctx core.Context, packages []model.LinuxPackageSpec) error {
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

func linuxPackageTestOutcome(target, tested string) string {
	if target != linuxPackageTestStage {
		return "none declared"
	}
	return tested
}

func reportLinuxPackageReuse(ctx core.Context, pkg model.LinuxPackageSpec, target string) {
	if target != linuxPackageTestStage {
		ctx.Trace(pkg.Name + ": its " + target + " stage is reused (fingerprint fp-" + pkg.Fingerprint + ")")
	}
	ctx.Info(pkg.Name + ": test: " + linuxPackageTestOutcome(target, "reused (fingerprint fp-"+pkg.Fingerprint+")"))
}

func traceLinuxPackageRunReason(ctx core.Context, pkg model.LinuxPackageSpec, target string) {
	switch {
	case pkg.NoReuseReason != "":
		ctx.Trace(pkg.Name + ": running its " + target + " stage: " + pkg.NoReuseReason)
	case pkg.Fingerprint != "":
		ctx.Trace(pkg.Name + ": running its " + target + " stage: no recorded pass for fingerprint fp-" + pkg.Fingerprint)
	}
}

// ExportReleaseLinuxPackages builds every package's `dist` stage for every release platform,
// inside the publish so a failure stops the release before anything is public.
func ExportReleaseLinuxPackages(ctx core.Context, spec model.ReleaseSpec) error {
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
