package kubeapi

import (
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
)

type (
	KubernetesDeploymentCheckerFunc func(core.Context, model.KubernetesDeploymentCheckParams) (bool, error)
)
