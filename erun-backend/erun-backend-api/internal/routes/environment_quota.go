package routes

import (
	"context"
	"fmt"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	"github.com/sophium/erun/erun-common/base/config"
)

// resourceQuotaExceededError marks a resourceQuota re-check failure as a
// caller-facing 409 (naming which cap/budget is short), distinct from a
// repository failure while resolving the inputs, which is a 500.
type resourceQuotaExceededError struct{ detail string }

func (e *resourceQuotaExceededError) Error() string { return e.detail }

// revalidateResourceQuota re-runs the two resource-quota checks a redeploy
// must still satisfy: the per-environment floor and the tenant's aggregate
// budget. A redeploy does not add a new environment — it is already
// counted — so the aggregate projection uses the runtime count as-is, not
// +1 the way a create does.
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

// enforceRuntimeResourceQuotaOnCreate is revalidateResourceQuota's create-time
// counterpart: the same per-environment floor and aggregate budget checks,
// against a quota createEnvironment has already fetched, and against
// runtimeCount+1 — this new environment is not counted yet, unlike a
// redeploy's recheck.
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

// minRuntimeCPUMillicores/MemoryMB/StorageGB are the smallest tenant quota
// that can host a stock runtime environment, derived from
// config.MinimumRuntimeNamespaceQuota: the erun-devops runtime pod summed
// across both its containers (a ResourceQuota counts every container in the
// pod), plus its PVCs. Below this floor Kubernetes rejects the pod at
// admission, so checking it before the deploy Job starts turns a silent
// five-minute rollout timeout (cause hidden in a ReplicaSet FailedCreate
// event) into a clear 409 naming the shortfall.
var minRuntimeCPUMillicores, minRuntimeMemoryMB, minRuntimeStorageGB = func() (int, int, int) {
	cpu, memory, storage := config.MinimumRuntimeNamespaceQuota()
	return int(cpu), int(memory), int(storage)
}()

// validateNamespaceQuotaFloor rejects a tenant quota that cannot fit the
// stock runtime pod, naming which cap is insufficient and by how much.
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

// validateAggregateResourceBudget refuses admitting a runtime environment
// that would push the tenant's projected total CPU/memory/storage past its
// aggregate budget. Every runtime environment gets the SAME
// per-environment cap (quota.MaxCPUMillicores etc.), so the projection is
// exact, not an estimate: projectedRuntimeCount * the per-environment cap.
// The caller computes the count: existingCount+1 for a new environment not
// yet persisted, or existingCount as-is for a redeploy of one already
// counted. This is a tenant-wide ceiling distinct from the per-environment
// floor validateNamespaceQuotaFloor checks above.
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

// environmentQuotaUsage reports how many environments the caller's tenant has
// and its full quota row. Creation enforces the env-count cap; the provision
// preview reports it; the deploy path threads the resource caps to the Job.
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
