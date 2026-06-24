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

export interface CloudContext {
  contextId: string;
  name: string;
  // "aws" today; a string for forward compatibility.
  provider: string;
  region: string;
  kubernetesContext?: string;
  cloudProviderAlias?: string;
  instanceType?: string;
}

// The console's read model over the per-tenant erun config.
export interface TenantConfigView {
  tenant: Tenant;
  environments: Environment[];
  contexts: CloudContext[];
}
