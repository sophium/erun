package helmrun

import (
	"github.com/sophium/erun/erun-common/base/seam/helmdeploy"
	"github.com/sophium/erun/erun-common/base/seam/kubeapi"
	"github.com/sophium/erun/erun-common/infra/kube"
)

func WrapHelmChartDeployerWithNamespaceEnsure(ensure kubeapi.NamespaceEnsurerFunc, deploy helmdeploy.HelmChartDeployerFunc) helmdeploy.HelmChartDeployerFunc {
	if deploy == nil {
		deploy = DeployHelmChart
	}
	if ensure == nil {
		return deploy
	}

	return func(params helmdeploy.HelmDeployParams) error {
		if err := ensure(params.KubernetesContext, params.Namespace); err != nil {
			return err
		}
		if err := kube.EnsureKubernetesResourceQuota(params.KubernetesContext, params.Namespace, params.NamespaceQuota); err != nil {
			return err
		}
		return deploy(params)
	}
}
