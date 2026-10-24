package sshd

import (
	opentargetpkg "github.com/sophium/erun/cmd/shared/opentarget"
	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
)

func ResolveOpenWithInitRetryForParams(ctx core.Context, params model.OpenParams, shouldRunInit func(error) bool, resolveOpen func(model.OpenParams) (config.OpenResult, error), runInitForOpen func(core.Context, model.OpenParams) error) (config.OpenResult, bool, error) {
	result, err := resolveOpen(params)
	if !shouldRunInit(err) {
		return result, false, err
	}

	if initErr := opentargetpkg.RunInitBeforeOpenForParams(ctx, params, runInitForOpen); initErr != nil {
		return config.OpenResult{}, true, initErr
	}

	result, err = resolveOpen(params)
	return result, true, err
}
