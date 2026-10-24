package opentarget

import (
	"errors"
	"strings"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/primitive/open/resolve"
	"github.com/sophium/erun/erun-common/primitive/shared/opentarget"
	"github.com/sophium/erun/internal"
)

func ResolveOpenArgs(command string, args []string, resolveOpen func(model.OpenParams) (config.OpenResult, error)) (model.OpenParams, config.OpenResult, error) {
	params, err := resolve.OpenParamsForArgs(args)
	if err != nil {
		return model.OpenParams{}, config.OpenResult{}, err
	}

	params.Command = command
	params.CommandScopesTenantByFlag = true
	result, err := resolveOpen(params)
	return params, result, err
}

func ResolveOpenParams(command string, args []string, overrides model.OpenParams) (model.OpenParams, error) {
	params, err := resolve.OpenParamsForArgs(args)
	if err != nil {
		return model.OpenParams{}, err
	}
	if tenant := strings.TrimSpace(overrides.Tenant); tenant != "" {
		params.Tenant = tenant
	}
	if environment := strings.TrimSpace(overrides.Environment); environment != "" {
		params.Environment = environment
	}

	switch {
	case strings.TrimSpace(params.Tenant) == "" && strings.TrimSpace(params.Environment) == "":
		params.UseDefaultTenant = true
		params.UseDefaultEnvironment = true
	case strings.TrimSpace(params.Tenant) == "":
		params.UseDefaultTenant = true
		params.UseDefaultEnvironment = false
	case strings.TrimSpace(params.Environment) == "":
		params.UseDefaultTenant = false
		params.UseDefaultEnvironment = true
	default:
		params.UseDefaultTenant = false
		params.UseDefaultEnvironment = false
	}

	params.Command = command
	params.CommandScopesTenantByFlag = true
	return params, nil
}

func RunInitBeforeOpen(ctx core.Context, args []string, runInitForArgs func(core.Context, []string) error) error {
	ctx.Logger.Debug("running init before resolving open target")
	return runInitForArgs(ctx, args)
}

func RunInitBeforeOpenForParams(ctx core.Context, params model.OpenParams, runInitForOpen func(core.Context, model.OpenParams) error) error {
	ctx.Logger.Debug("running init before resolving open target")
	return runInitForOpen(ctx, params)
}

func ResolveOpenWithInitRetry(ctx core.Context, args []string, shouldRunInit func(error) bool, resolveOpen func(model.OpenParams) (config.OpenResult, error), runInitForArgs func(core.Context, []string) error) (config.OpenResult, bool, error) {
	params, result, err := ResolveOpenArgs(ctx.Command, args, resolveOpen)
	if !shouldRunInit(err) {
		return result, false, err
	}

	if initErr := RunInitBeforeOpen(ctx, args, runInitForArgs); initErr != nil {
		return config.OpenResult{}, true, initErr
	}

	result, err = resolveOpen(params)
	return result, true, err
}

func openerIsDefaultError(err error) bool {
	return errors.Is(err, opentarget.ErrDefaultTenantNotConfigured) ||
		errors.Is(err, opentarget.ErrDefaultEnvironmentNotConfigured) ||
		errors.Is(err, config.ErrNotInitialized)
}

func shouldInitOpenCommand(err error) bool {
	return errors.Is(err, opentarget.ErrKubernetesContextNotConfigured)
}

func ShouldRunInitForOpenCommand(err error) bool {
	return ShouldInitRootCommand(err) ||
		errors.Is(err, opentarget.ErrTenantNotFound) ||
		errors.Is(err, config.ErrEnvironmentNotFound)
}

func ShouldInitRootCommand(err error) bool {
	return openerIsDefaultError(err) ||
		shouldInitOpenCommand(err) ||
		errors.Is(err, config.ErrNotInGitRepository) ||
		internal.IsReported(err)
}
