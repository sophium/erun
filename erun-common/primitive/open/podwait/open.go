package podwait

import (
	"io"
	"os"

	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/config/execmode"
	"github.com/sophium/erun/erun-common/base/config/runtimetarget"
	"github.com/sophium/erun/erun-common/infra/cloud/runtimewait"
	"github.com/sophium/erun/erun-common/infra/kube"
	"github.com/sophium/erun/erun-common/primitive/shared/opentarget"
)

func WaitForShellDeployment(req configpkg.ShellLaunchParams) error {
	if execmode.CurrentExecutionMode(kube.KubectlDeploymentWaitExecutionOperation) == execmode.ExecutionModeLibrary {
		if err := kube.LibraryWaitForDeploymentAvailable(req.KubernetesContext, req.Namespace, runtimetarget.RuntimeReleaseName(req.Tenant), runtimewait.DefaultShellLaunchWaitTimeout); err != nil {
			return EnrichShellDeploymentError(req, err, opentarget.RunOpenKubectl)
		}
		return nil
	}
	if err := opentarget.RunOpenKubectl(runtimewait.KubectlDeploymentWaitArgs(req), io.Discard, os.Stderr); err != nil {
		return EnrichShellDeploymentError(req, err, opentarget.RunOpenKubectl)
	}
	return nil
}
