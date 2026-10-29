package resolve

import (
	"fmt"

	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/config/projectroot"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/primitive/shared/opentarget"
)

func OpenParamsForArgs(args []string) (model.OpenParams, error) {
	switch len(args) {
	case 0:
		return model.OpenParams{
			UseDefaultTenant:      true,
			UseDefaultEnvironment: true,
		}, nil
	case 1:
		return model.OpenParams{
			Environment:      args[0],
			UseDefaultTenant: true,
		}, nil
	case 2:
		return model.OpenParams{
			Tenant:      args[0],
			Environment: args[1],
		}, nil
	default:
		return model.OpenParams{}, fmt.Errorf("accepts 0 to 2 arg(s), received %d", len(args))
	}
}

func ResolveOpen(store configpkg.OpenStore, params model.OpenParams) (configpkg.OpenResult, error) {
	return opentarget.ResolveOpenWithFinder(store, projectroot.FindProjectRoot, params)
}
