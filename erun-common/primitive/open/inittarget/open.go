package inittarget

import (
	"errors"
	"strings"

	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/base/seam/bootstrap"
	"github.com/sophium/erun/erun-common/primitive/open/resolve"
	"github.com/sophium/erun/erun-common/primitive/shared/opentarget"
)

func loadOpenDefaultEnvironment(store configpkg.OpenStore, tenant string) (string, error) {
	tenantConfig, _, err := store.LoadTenantConfig(tenant)
	if errors.Is(err, configpkg.ErrNotInitialized) {
		return "", opentarget.ErrDefaultEnvironmentNotConfigured
	}
	if err != nil {
		return "", err
	}
	if tenantConfig.DefaultEnvironment == "" {
		return "", opentarget.ErrDefaultEnvironmentNotConfigured
	}
	return tenantConfig.DefaultEnvironment, nil
}

func InitParamsForOpenArgs(store configpkg.OpenStore, args []string) (bootstrap.BootstrapInitParams, error) {
	params, err := resolve.OpenParamsForArgs(args)
	if err != nil {
		return bootstrap.BootstrapInitParams{}, err
	}
	return InitParamsForOpenTarget(store, params)
}

func InitParamsForOpenTarget(store configpkg.OpenStore, params model.OpenParams) (bootstrap.BootstrapInitParams, error) {
	tenant := strings.TrimSpace(params.Tenant)
	environment := strings.TrimSpace(params.Environment)

	switch {
	case tenant != "" && environment != "":
		return bootstrap.BootstrapInitParams{Tenant: tenant, Environment: environment}, nil
	case tenant != "":
		return bootstrap.BootstrapInitParams{Tenant: tenant}, nil
	case environment != "":
		return initParamsForOpenEnvironmentOnly(store, environment)
	}

	return initParamsForOpenDefaults(store)
}

func initParamsForOpenEnvironmentOnly(store configpkg.OpenStore, environment string) (bootstrap.BootstrapInitParams, error) {
	resolvedTenant, err := opentarget.LoadOpenDefaultTenant(store)
	if err != nil {
		if errors.Is(err, opentarget.ErrDefaultTenantNotConfigured) || errors.Is(err, configpkg.ErrNotInitialized) {
			return bootstrap.BootstrapInitParams{Environment: environment, ResolveTenant: true}, nil
		}
		return bootstrap.BootstrapInitParams{}, err
	}
	return bootstrap.BootstrapInitParams{Tenant: resolvedTenant, Environment: environment}, nil
}

func initParamsForOpenDefaults(store configpkg.OpenStore) (bootstrap.BootstrapInitParams, error) {
	resolvedTenant, err := opentarget.LoadOpenDefaultTenant(store)
	if err != nil {
		if errors.Is(err, opentarget.ErrDefaultTenantNotConfigured) || errors.Is(err, configpkg.ErrNotInitialized) {
			return bootstrap.BootstrapInitParams{ResolveTenant: true}, nil
		}
		return bootstrap.BootstrapInitParams{}, err
	}

	defaultEnvironment, err := loadOpenDefaultEnvironment(store, resolvedTenant)
	if err != nil {
		if errors.Is(err, opentarget.ErrDefaultEnvironmentNotConfigured) || errors.Is(err, configpkg.ErrNotInitialized) {
			return bootstrap.BootstrapInitParams{Tenant: resolvedTenant}, nil
		}
		return bootstrap.BootstrapInitParams{}, err
	}

	return bootstrap.BootstrapInitParams{Tenant: resolvedTenant, Environment: defaultEnvironment}, nil
}
