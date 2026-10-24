package doctor

import (
	"fmt"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/infra/cloud"
	"github.com/sophium/erun/erun-common/orchestration/diagnosis"
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

// RunDoctorInspection reads docker storage on the environment's build daemon,
// refusing with DoctorDockerDaemonUnavailableError when there is none.
func RunDoctorInspection(ctx core.Context, runner diagnosis.RuntimeContainerCommandRunnerFunc, req config.ShellLaunchParams) (DoctorInspectionResult, error) {
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

// RunDoctorAction prunes the environment's build daemon, with store readings
// around the prune so a no-op prune is reported as one.
func RunDoctorAction(ctx core.Context, runner diagnosis.RuntimeContainerCommandRunnerFunc, req config.ShellLaunchParams, action DoctorAction) (DoctorDockerResult, error) {
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

// waitForDoctorDaemon waits for a pod-hosted daemon; a host daemon needs no cluster.
func waitForDoctorDaemon(ctx core.Context, req config.ShellLaunchParams, daemon DoctorDockerDaemon) error {
	if daemon.Kind == DoctorDockerDaemonHost {
		return nil
	}
	return cloud.TraceAndWaitForRuntime(ctx, req)
}
