package model

import (
	"time"

	"github.com/uptrace/bun"
)

type EnvironmentType string

const (
	EnvironmentTypeLocalAgent  EnvironmentType = "local-agent"
	EnvironmentTypeRemoteAgent EnvironmentType = "remote-agent"
	EnvironmentTypeRuntime     EnvironmentType = "runtime"
)

// EnvironmentStatus is the provisioning lifecycle of a hosted environment
// (#605): a row is `registered` when created, then the server-side deploy
// executor moves it `provisioning` → `running`/`failed`. A running or failed
// environment moves to `deleting` when a teardown is requested, then either
// disappears (the row is hard-deleted once the namespace is confirmed torn
// down) or moves to `deletion-blocked` naming why it did not. `running` must
// never survive a delete attempt: a namespace that is merely being asked to
// tear down is not "up and serving" anymore, and a blocked one has stopped
// being trustworthy state for a caller to act on.
type EnvironmentStatus string

const (
	EnvironmentStatusRegistered      EnvironmentStatus = "registered"
	EnvironmentStatusProvisioning    EnvironmentStatus = "provisioning"
	EnvironmentStatusRunning         EnvironmentStatus = "running"
	EnvironmentStatusFailed          EnvironmentStatus = "failed"
	EnvironmentStatusDeleting        EnvironmentStatus = "deleting"
	EnvironmentStatusDeletionBlocked EnvironmentStatus = "deletion-blocked"
)

// Environment is the per-tenant system of record for an erun environment, the
// DB-backed shape of eruncommon.EnvConfig. Its link to a context is a real
// tenant-scoped foreign key (ContextID), not a kubernetes-context string match.
type Environment struct {
	bun.BaseModel     `bun:"table:environments,alias:e"`
	EnvironmentID     string          `json:"environmentId" bun:"environment_id,pk,scanonly"`
	TenantID          string          `json:"tenantId" bun:"tenant_id,scanonly"`
	Name              string          `json:"name" bun:"name"`
	Type              EnvironmentType `json:"type" bun:"type"`
	KubernetesContext string          `json:"kubernetesContext,omitempty" bun:"kubernetes_context,nullzero"`
	ContextID         string          `json:"contextId,omitempty" bun:"context_id,nullzero"`
	RuntimeVersion    string          `json:"runtimeVersion,omitempty" bun:"runtime_version,nullzero"`
	// Status is DB-owned: it defaults to `registered` on insert and is moved by
	// the server-side deploy executor, so it is scan-only here (never inserted or
	// updated through the create path). ProvisionError carries the failure detail
	// when Status is `failed`.
	Status         EnvironmentStatus `json:"status" bun:"status,scanonly"`
	ProvisionError string            `json:"provisionError,omitempty" bun:"provision_error,scanonly,nullzero"`
	// DeployedVersion is the version the last successful deploy actually
	// installed, as opposed to RuntimeVersion, which is the declared pin. Owned
	// by the deploy executor, so it is scan-only; a failed deploy leaves it on
	// the version still running in the cluster.
	DeployedVersion string `json:"deployedVersion,omitempty" bun:"deployed_version,scanonly,nullzero"`
	// ExposeError carries why the deploy Job's best-effort chained exposure
	// (DNS + Ingress) did not succeed, distinct from ProvisionError: it never
	// moves Status away from `running`, since exposure failing does not mean
	// the deployed workload is unhealthy (#1086). Owned by the deploy executor,
	// so scan-only; empty means exposure succeeded, was never attempted, or the
	// environment predates chaining an expose at all.
	ExposeError string `json:"exposeError,omitempty" bun:"expose_error,scanonly,nullzero"`
	// DeleteError carries why a delete attempt did not tear the namespace down
	// (the namespace's own conditions, verbatim, when it's stuck on an
	// unsatisfiable finalizer) when Status is `deletion-blocked`. Owned by the
	// delete executor, so scan-only. It is NOT cleared when a retry claims the
	// row: an attempt's outcome overwrites it, so the recorded blocker stays
	// readable for the whole time a teardown is stuck rather than blinking out
	// on every reconciler tick (#1166).
	DeleteError string `json:"deleteError,omitempty" bun:"delete_error,scanonly,nullzero"`
	// DeleteAttempts counts how many delete attempts have claimed this row.
	// Incremented by the claim, so it survives an attempt that dies without
	// reporting. It is what bounds the reconciler's retries: past a cap the
	// environment needs intervention, and re-attempting it forever only hides
	// that. Owned by the claim, so scan-only.
	DeleteAttempts int       `json:"deleteAttempts,omitempty" bun:"delete_attempts,scanonly"`
	CreatedAt      time.Time `json:"createdAt" bun:"created_at,scanonly"`
	UpdatedAt      time.Time `json:"updatedAt" bun:"updated_at,scanonly"`
}
