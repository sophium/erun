package inittarget

import (
	"errors"
	"strings"

	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/base/seam"
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

func InitParamsForOpenArgs(store configpkg.OpenStore, args []string) (seam.BootstrapInitParams, error) {
	params, err := resolve.OpenParamsForArgs(args)
	if err != nil {
		return seam.BootstrapInitParams{}, err
	}
	return InitParamsForOpenTarget(store, params)
}

func InitParamsForOpenTarget(store configpkg.OpenStore, params model.OpenParams) (seam.BootstrapInitParams, error) {
	tenant := strings.TrimSpace(params.Tenant)
	environment := strings.TrimSpace(params.Environment)

	switch {
	case tenant != "" && environment != "":
		return seam.BootstrapInitParams{Tenant: tenant, Environment: environment}, nil
	case tenant != "":
		return seam.BootstrapInitParams{Tenant: tenant}, nil
	case environment != "":
		return initParamsForOpenEnvironmentOnly(store, environment)
	}

	return initParamsForOpenDefaults(store)
}

func initParamsForOpenEnvironmentOnly(store configpkg.OpenStore, environment string) (seam.BootstrapInitParams, error) {
	resolvedTenant, err := opentarget.LoadOpenDefaultTenant(store)
	if err != nil {
		if errors.Is(err, opentarget.ErrDefaultTenantNotConfigured) || errors.Is(err, configpkg.ErrNotInitialized) {
			return seam.BootstrapInitParams{Environment: environment, ResolveTenant: true}, nil
		}
		return seam.BootstrapInitParams{}, err
	}
	return seam.BootstrapInitParams{Tenant: resolvedTenant, Environment: environment}, nil
}

func initParamsForOpenDefaults(store configpkg.OpenStore) (seam.BootstrapInitParams, error) {
	resolvedTenant, err := opentarget.LoadOpenDefaultTenant(store)
	if err != nil {
		if errors.Is(err, opentarget.ErrDefaultTenantNotConfigured) || errors.Is(err, configpkg.ErrNotInitialized) {
			return seam.BootstrapInitParams{ResolveTenant: true}, nil
		}
		return seam.BootstrapInitParams{}, err
	}

	defaultEnvironment, err := loadOpenDefaultEnvironment(store, resolvedTenant)
	if err != nil {
		if errors.Is(err, opentarget.ErrDefaultEnvironmentNotConfigured) || errors.Is(err, configpkg.ErrNotInitialized) {
			return seam.BootstrapInitParams{Tenant: resolvedTenant}, nil
		}
		return seam.BootstrapInitParams{}, err
	}

	return seam.BootstrapInitParams{Tenant: resolvedTenant, Environment: defaultEnvironment}, nil
}
