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

export interface Environment {
  environmentId: string;
  name: string;
  // "runtime" | "remote-agent" | "local-agent"; kept as a string for the
  // same forward-compatibility reason as Tenant.type.
  type: string;
  kubernetesContext?: string;
  contextId?: string;
  runtimeVersion?: string;
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
