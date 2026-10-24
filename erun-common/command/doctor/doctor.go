package doctor

import (
	"fmt"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/infra/cloud"
	"github.com/sophium/erun/erun-common/orchestration"
)

type (
	DoctorAction string
)

const (
	DoctorActionPruneImages     DoctorAction = "prune_images"
	DoctorActionPruneBuildCache DoctorAction = "prune_build_cache"
	DoctorActionPruneContainers DoctorAction = "prune_containers"
)

type DoctorInspectionResult struct {
	Daemon DoctorDockerDaemon
	Stdout string
	Stderr string
}

func DoctorActions() []DoctorAction {
	return []DoctorAction{
		DoctorActionPruneImages,
		DoctorActionPruneBuildCache,
		DoctorActionPruneContainers,
	}
}

func DoctorActionPromptLabel(action DoctorAction, result config.OpenResult) string {
	target := result.Tenant + "/" + result.Environment
	switch action {
	case DoctorActionPruneImages:
		return fmt.Sprintf("Prune unused Docker images in %s?", target)
	case DoctorActionPruneBuildCache:
		return fmt.Sprintf("Prune unused BuildKit cache in %s?", target)
	case DoctorActionPruneContainers:
		return fmt.Sprintf("Prune stopped Docker containers in %s?", target)
	default:
		return fmt.Sprintf("Run doctor action %q in %s?", action, target)
	}
}

func DoctorActionDescription(action DoctorAction) string {
	switch action {
	case DoctorActionPruneImages:
		return "Remove unused Docker images"
	case DoctorActionPruneBuildCache:
		return "Remove unused BuildKit cache"
	case DoctorActionPruneContainers:
		return "Remove stopped Docker containers"
	default:
		return string(action)
	}
}

// RunDoctorInspection reads the environment's docker storage: disk and inode
// usage at the daemon's root, and what the daemon's own stores reclaim. The
// daemon is resolved from the environment (ResolveDoctorDockerDaemon) and is
// named in the result, so the caller can say which daemon every figure below it
// came from; an environment with no daemon holding build images refuses with
// DoctorDockerDaemonUnavailableError rather than reading whatever daemon
// happens to be reachable.
func RunDoctorInspection(ctx core.Context, runner orchestration.RuntimeContainerCommandRunnerFunc, req config.ShellLaunchParams) (DoctorInspectionResult, error) {
	daemon := ResolveDoctorDockerDaemon(req)
	if daemon.Unavailable() {
		return DoctorInspectionResult{Daemon: daemon}, DoctorDockerDaemonUnavailableError{Daemon: daemon}
	}
	if err := waitForDoctorDaemon(ctx, req, daemon); err != nil {
		return DoctorInspectionResult{Daemon: daemon}, err
	}
	result, err := runDoctorDockerSteps(ctx, runner, req, daemon, "doctor-inspect", doctorInspectionSteps())
	return DoctorInspectionResult{Daemon: daemon, Stdout: result.Stdout, Stderr: result.Stderr}, err
}

// RunDoctorAction prunes the daemon that actually holds this environment's
// build images, through the transport that daemon is reachable by. It returns
// the daemon beside the output so the caller can name it, and the store
// readings taken around the prune so a prune that freed nothing can be reported
// as that instead of as a success.
func RunDoctorAction(ctx core.Context, runner orchestration.RuntimeContainerCommandRunnerFunc, req config.ShellLaunchParams, action DoctorAction) (DoctorDockerResult, error) {
	daemon := ResolveDoctorDockerDaemon(req)
	if daemon.Unavailable() {
		return DoctorDockerResult{Daemon: daemon}, DoctorDockerDaemonUnavailableError{Daemon: daemon}
	}
	steps, err := doctorActionSteps(action)
	if err != nil {
		return DoctorDockerResult{Daemon: daemon}, err
	}
	if err := waitForDoctorDaemon(ctx, req, daemon); err != nil {
		return DoctorDockerResult{Daemon: daemon}, err
	}
	return runDoctorDockerSteps(ctx, runner, req, daemon, "doctor-"+string(action), steps)
}

// waitForDoctorDaemon waits for the pod a pod-hosted daemon lives in to be
// available before exec'ing into it. A daemon this process reaches directly
// (the host kind) needs no wait and no cluster: waiting on a deployment a host
// environment does not have would fail the read for a reason that is not about
// the daemon at all.
func waitForDoctorDaemon(ctx core.Context, req config.ShellLaunchParams, daemon DoctorDockerDaemon) error {
	if daemon.Kind == DoctorDockerDaemonHost {
		return nil
	}
	return cloud.TraceAndWaitForRuntime(ctx, req)
}
