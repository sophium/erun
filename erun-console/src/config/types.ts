// Types mirroring the wire shape of `GET /v1/config` on erun-backend-api — the
// per-tenant erun read model, denormalized to the on-disk erun config shape.
// See erun-docs/docs/agent-reference/api-protocol.md § Endpoints.

export interface Tenant {
  tenantId: string;
  name: string;
  // "COMPANY" | "OPERATIONS" on the backend; kept as a string so an unknown
  // future type still renders rather than failing the parse.
  type: string;
}

// The provisioning lifecycle of a hosted environment: `registered` (row exists,
// nothing deployed) → `provisioning` → `running` | `failed`, plus the teardown
// states a delete moves through: `deleting` while an attempt is in flight and
// `deletion-blocked` when one finished without tearing the namespace down.
// Parsed leniently so an unknown future state renders no badge rather than
// failing the parse — which is exactly why the teardown states had to be added
// explicitly: without them a deleting environment rendered with no badge at
// all, indistinguishable from an ordinary one, and the console offered Deploy
// on it (#1170).
export type EnvironmentStatus =
  | 'registered'
  | 'provisioning'
  | 'running'
  | 'failed'
  | 'deleting'
  | 'deletion-blocked';

// The statuses for which a delete has been requested and is still outstanding.
// A mutating lifecycle action on one of these is refused by the API with 409,
// so the console must not offer it.
export const TEARDOWN_STATUSES: readonly EnvironmentStatus[] = ['deleting', 'deletion-blocked'];

// isTearingDown reports whether a delete is outstanding for this environment.
export function isTearingDown(environment: { status?: EnvironmentStatus }): boolean {
  return environment.status !== undefined && TEARDOWN_STATUSES.includes(environment.status);
}

export interface Environment {
  environmentId: string;
  name: string;
  // "runtime" | "remote-agent" | "local-agent"; kept as a string for the
  // same forward-compatibility reason as Tenant.type.
  type: string;
  kubernetesContext?: string;
  contextId?: string;
  runtimeVersion?: string;
  status?: EnvironmentStatus;
  provisionError?: string;
  // The version the last successful deploy actually installed, distinct from
  // runtimeVersion (the declared pin) — a failed or in-flight deploy leaves
  // this on whatever version is still running in the cluster.
  deployedVersion?: string;
  // Why a delete attempt did not tear the namespace down, when status is
  // `deletion-blocked`: the namespace's own conditions, verbatim. Surfacing it
  // is the whole point of showing the teardown state — it names the finalizer
  // actually holding the namespace, which is what an operator needs.
  deleteError?: string;
}

// The provisioning lifecycle a context moves through: `provisioning` → `running`
// | `failed`. A union of the known states, parsed leniently so an unknown future
// state still renders.
export type ContextStatus = 'provisioning' | 'running' | 'failed';

export interface CloudContext {
  contextId: string;
  name: string;
  // "aws" today; a string for forward compatibility.
  provider: string;
  region: string;
  kubernetesContext?: string;
  cloudProviderAlias?: string;
  instanceType?: string;
  // Omitted by the read model for contexts registered before live provisioning existed.
  status?: ContextStatus;
  provisionError?: string;
}

export interface TenantConfigView {
  tenant: Tenant;
  environments: Environment[];
  contexts: CloudContext[];
}
