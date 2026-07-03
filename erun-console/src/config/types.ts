// Types mirroring the JSON shape of `GET /v1/config` on erun-backend-api.
// See erun-docs/docs/agent-reference/api-protocol.md § Endpoints:
// the endpoint returns the per-tenant erun read model as
// `{ tenant, environments[], contexts[] }`, denormalized to the on-disk
// erun config shape. All reads are tenant-scoped by row-level security.

export interface Tenant {
  tenantId: string;
  name: string;
  // "COMPANY" | "OPERATIONS" on the backend; kept as a string so an unknown
  // future type still renders rather than failing the parse.
  type: string;
}

export interface Environment {
  environmentId: string;
  name: string;
  // "runtime" | "remote-agent" | "local-agent"; kept as a string for the
  // same forward-compatibility reason as Tenant.type.
  type: string;
  // Optional fields — the backend omits them when unset (see POST /v1/environments).
  kubernetesContext?: string;
  contextId?: string;
  runtimeVersion?: string;
}

// The provisioning lifecycle a context moves through once `POST /v1/contexts`
// kicks off the live bootstrap: `provisioning` → `running`
// (success) | `failed`. Kept as a string union for the known states but parsed
// leniently from the wire (see parseContext) so an unknown future state still
// renders rather than failing the parse.
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
  // Provisioning status from `GET /v1/contexts/{id}` (omitted by the read model
  // for contexts registered before live provisioning existed).
  status?: ContextStatus;
  // The failure reason when `status === 'failed'`.
  provisionError?: string;
}

// The console's read model over the per-tenant erun config.
export interface TenantConfigView {
  tenant: Tenant;
  environments: Environment[];
  contexts: CloudContext[];
}
