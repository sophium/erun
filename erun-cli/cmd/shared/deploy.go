package shared

import (
	"github.com/sophium/erun/erun-common/primitive/deploy"
	"github.com/sophium/erun/erun-common/primitive/shared"
)

func ResolveDeployTargetArgs(command string, args []string, target deploy.DeployTarget) (deploy.DeployTarget, error) {
	params, err := ResolveOpenParams(command, args, shared.OpenParams{
		Tenant:      target.Tenant,
		Environment: target.Environment,
	})
	if err != nil {
		return deploy.DeployTarget{}, err
	}
	target.Tenant = params.Tenant
	target.Environment = params.Environment
	return target, nil
}
