package routes

import (
	"context"
	"fmt"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	"github.com/sophium/erun/erun-common/base/config/runtimeresources"
)

// resourceQuotaExceededError marks a caller-facing 409, distinct from a repository 500.
type resourceQuotaExceededError struct{ detail string }

func (e *resourceQuotaExceededError) Error() string { return e.detail }

// revalidateResourceQuota uses the runtime count as-is: a redeployed environment
// is already counted.
func (r EnvironmentRoutes) revalidateResourceQuota(ctx context.Context) error {
	quota, err := r.quotas.Get(ctx)
	if err != nil {
		return err
	}
	if err := validateNamespaceQuotaFloor(quota); err != nil {
		return &resourceQuotaExceededError{detail: err.Error()}
	}
	runtimeCount, err := r.environments.CountByType(ctx, model.EnvironmentTypeRuntime)
	if err != nil {
		return err
	}
	if err := validateAggregateResourceBudget(runtimeCount, quota); err != nil {
		return &resourceQuotaExceededError{detail: err.Error()}
	}
	return nil
}

// enforceRuntimeResourceQuotaOnCreate projects runtimeCount+1 for the new environment.
func (r EnvironmentRoutes) enforceRuntimeResourceQuotaOnCreate(ctx context.Context, quota model.TenantQuota) error {
	if err := validateNamespaceQuotaFloor(quota); err != nil {
		return &resourceQuotaExceededError{detail: err.Error()}
	}
	runtimeCount, err := r.environments.CountByType(ctx, model.EnvironmentTypeRuntime)
	if err != nil {
		return err
	}
	if err := validateAggregateResourceBudget(runtimeCount+1, quota); err != nil {
		return &resourceQuotaExceededError{detail: err.Error()}
	}
	return nil
}

// The runtime floor sums every container in the pod plus its PVCs, as a
// ResourceQuota does; checking it early turns a silent rollout timeout into a 409.
var minRuntimeCPUMillicores, minRuntimeMemoryMB, minRuntimeStorageGB = func() (int, int, int) {
	cpu, memory, storage := runtimeresources.MinimumRuntimeNamespaceQuota()
	return int(cpu), int(memory), int(storage)
}()

// validateNamespaceQuotaFloor names which cap is short and by how much.
func validateNamespaceQuotaFloor(quota model.TenantQuota) error {
	switch {
	case quota.MaxCPUMillicores < minRuntimeCPUMillicores:
		return fmt.Errorf("tenant CPU quota (%dm) is below the runtime environment's minimum (%dm), short by %dm; raise maxCpuMillicores", quota.MaxCPUMillicores, minRuntimeCPUMillicores, minRuntimeCPUMillicores-quota.MaxCPUMillicores)
	case quota.MaxMemoryMB < minRuntimeMemoryMB:
		return fmt.Errorf("tenant memory quota (%dMi) is below the runtime environment's minimum (%dMi), short by %dMi; raise maxMemoryMb", quota.MaxMemoryMB, minRuntimeMemoryMB, minRuntimeMemoryMB-quota.MaxMemoryMB)
	case quota.MaxStorageGB < minRuntimeStorageGB:
		return fmt.Errorf("tenant storage quota (%dGi) is below the runtime environment's minimum (%dGi), short by %dGi; raise maxStorageGb", quota.MaxStorageGB, minRuntimeStorageGB, minRuntimeStorageGB-quota.MaxStorageGB)
	}
	return nil
}

// validateAggregateResourceBudget is exact because every runtime environment gets
// the same per-environment cap.
func validateAggregateResourceBudget(projected int, quota model.TenantQuota) error {
	switch {
	case projected*quota.MaxCPUMillicores > quota.MaxTotalCPUMillicores:
		return fmt.Errorf("tenant CPU budget (%dm) would be exceeded: %d runtime environment(s) at %dm each project to %dm; raise maxTotalCpuMillicores or lower maxCpuMillicores", quota.MaxTotalCPUMillicores, projected, quota.MaxCPUMillicores, projected*quota.MaxCPUMillicores)
	case projected*quota.MaxMemoryMB > quota.MaxTotalMemoryMB:
		return fmt.Errorf("tenant memory budget (%dMi) would be exceeded: %d runtime environment(s) at %dMi each project to %dMi; raise maxTotalMemoryMb or lower maxMemoryMb", quota.MaxTotalMemoryMB, projected, quota.MaxMemoryMB, projected*quota.MaxMemoryMB)
	case projected*quota.MaxStorageGB > quota.MaxTotalStorageGB:
		return fmt.Errorf("tenant storage budget (%dGi) would be exceeded: %d runtime environment(s) at %dGi each project to %dGi; raise maxTotalStorageGb or lower maxStorageGb", quota.MaxTotalStorageGB, projected, quota.MaxStorageGB, projected*quota.MaxStorageGB)
	}
	return nil
}

func environmentQuotaUsage(ctx context.Context, environments EnvironmentRepository, quotas TenantQuotaRepository) (count int, quota model.TenantQuota, err error) {
	quota, err = quotas.Get(ctx)
	if err != nil {
		return 0, model.TenantQuota{}, err
	}
	count, err = environments.Count(ctx)
	if err != nil {
		return 0, model.TenantQuota{}, err
	}
	return count, quota, nil
}
