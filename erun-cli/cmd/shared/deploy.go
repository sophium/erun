package shared

import (
	common "github.com/sophium/erun/erun-common"
)

func ResolveDeployTargetArgs(command string, args []string, target common.DeployTarget) (common.DeployTarget, error) {
	params, err := ResolveOpenParams(command, args, common.OpenParams{
		Tenant:      target.Tenant,
		Environment: target.Environment,
	})
	if err != nil {
		return common.DeployTarget{}, err
	}
	target.Tenant = params.Tenant
	target.Environment = params.Environment
	return target, nil
}
