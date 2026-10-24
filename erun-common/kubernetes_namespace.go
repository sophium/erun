package eruncommon

import (
	"github.com/sophium/erun/erun-common/infra/kube"
)

func WrapHelmChartDeployerWithNamespaceEnsure(ensure NamespaceEnsurerFunc, deploy HelmChartDeployerFunc) HelmChartDeployerFunc {
	if deploy == nil {
		deploy = DeployHelmChart
	}
	if ensure == nil {
		return deploy
	}

	return func(params HelmDeployParams) error {
		if err := ensure(params.KubernetesContext, params.Namespace); err != nil {
			return err
		}
		if err := kube.EnsureKubernetesResourceQuota(params.KubernetesContext, params.Namespace, params.NamespaceQuota); err != nil {
			return err
		}
		return deploy(params)
	}
}
