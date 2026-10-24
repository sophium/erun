package opentarget

import (
	"github.com/sophium/erun/erun-common/base/model"
)

func ResolveDeployTargetArgs(command string, args []string, target model.DeployTarget) (model.DeployTarget, error) {
	params, err := ResolveOpenParams(command, args, model.OpenParams{
		Tenant:      target.Tenant,
		Environment: target.Environment,
	})
	if err != nil {
		return model.DeployTarget{}, err
	}
	target.Tenant = params.Tenant
	target.Environment = params.Environment
	return target, nil
}
