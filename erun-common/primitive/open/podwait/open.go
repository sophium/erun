package podwait

import (
	"io"
	"os"

	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/infra/cloud"
	"github.com/sophium/erun/erun-common/infra/kube"
	"github.com/sophium/erun/erun-common/primitive/shared/opentarget"
)

func WaitForShellDeployment(req configpkg.ShellLaunchParams) error {
	if configpkg.CurrentExecutionMode(kube.KubectlDeploymentWaitExecutionOperation) == configpkg.ExecutionModeLibrary {
		if err := kube.LibraryWaitForDeploymentAvailable(req.KubernetesContext, req.Namespace, configpkg.RuntimeReleaseName(req.Tenant), cloud.DefaultShellLaunchWaitTimeout); err != nil {
			return EnrichShellDeploymentError(req, err, opentarget.RunOpenKubectl)
		}
		return nil
	}
	if err := opentarget.RunOpenKubectl(cloud.KubectlDeploymentWaitArgs(req), io.Discard, os.Stderr); err != nil {
		return EnrichShellDeploymentError(req, err, opentarget.RunOpenKubectl)
	}
	return nil
}
