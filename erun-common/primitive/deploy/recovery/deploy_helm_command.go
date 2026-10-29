package recovery

import (
	"fmt"
	"io"
	"strings"

	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/seam/helmdeploy"
	"github.com/sophium/erun/erun-common/infra/helm"
)

func HelmReleaseRecoveryCommand(p helmdeploy.HelmReleaseRecoveryParams) helm.CommandSpec {
	args := []string{}
	if p.Verbosity >= core.VerbosityDebug {
		args = append(args, "--v=4")
	}
	if strings.TrimSpace(p.KubernetesContext) != "" {
		args = append(args, "--context", p.KubernetesContext)
	}
	args = append(args,
		"--namespace", p.Namespace,
		"delete",
		"secrets,configmaps",
		"-l", helmPendingReleaseOperationSelector(p.ReleaseName),
		"--ignore-not-found",
	)

	return helm.CommandSpec{
		Name: "kubectl",
		Args: args,
	}
}

func helmPendingReleaseOperationSelector(releaseName string) string {
	return "owner=helm,name=" + releaseName + ",status in (pending-install,pending-upgrade,pending-rollback)"
}

// Error names the locked release and its recovery; helm's own message names
// neither.
func (e *HelmReleasePendingOperationError) Error() string {
	if e == nil {
		return ""
	}
	message := strings.TrimSpace(e.Message)
	if message == "" && e.Err != nil {
		message = e.Err.Error()
	}
	if message == "" {
		message = "another operation (install/upgrade/rollback) is in progress"
	}
	release := strings.TrimSpace(e.ReleaseName)
	if release == "" {
		release = "the release"
	}
	diagnosis := fmt.Sprintf("helm release %q is locked by an unfinished install/upgrade/rollback, so this deploy cannot proceed", release)
	if namespace := strings.TrimSpace(e.Namespace); namespace != "" {
		diagnosis += " in namespace " + namespace
	}
	if target := e.doctorTarget(); target != "" {
		return fmt.Sprintf("%s; clear the lock with `erun doctor --clear-pending-helm %s`, or with `%s`: %s",
			diagnosis, target, e.RecoveryCommand(), message)
	}
	return fmt.Sprintf("%s; clear the lock with `%s`: %s", diagnosis, e.RecoveryCommand(), message)
}

func (e *HelmReleasePendingOperationError) doctorTarget() string {
	tenant := strings.TrimSpace(e.Tenant)
	environment := strings.TrimSpace(e.Environment)
	if tenant == "" || environment == "" {
		return ""
	}
	return tenant + " " + environment
}

func (e *HelmReleasePendingOperationError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

func (e *HelmReleasePendingOperationError) RecoveryParams(verbosity int, stdout, stderr io.Writer) helmdeploy.HelmReleaseRecoveryParams {
	if e == nil {
		return helmdeploy.HelmReleaseRecoveryParams{Verbosity: verbosity, Stdout: stdout, Stderr: stderr}
	}
	return helmdeploy.HelmReleaseRecoveryParams{
		ReleaseName:       e.ReleaseName,
		Namespace:         e.Namespace,
		KubernetesContext: e.KubernetesContext,
		Verbosity:         verbosity,
		Stdout:            stdout,
		Stderr:            stderr,
	}
}

func (e *HelmReleasePendingOperationError) RecoveryCommand() string {
	if e == nil {
		return ""
	}
	command := HelmReleaseRecoveryCommand(e.RecoveryParams(0, nil, nil))
	return core.FormatShellCommand(command.Dir, command.Name, command.Args...)
}
