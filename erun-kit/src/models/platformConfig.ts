// Lenient types and parsers for erun-backend-api's `GET /v1/config` wire shape.

export interface Tenant {
  tenantId: string;
  name: string;
  // Kept as a string so an unknown future type still renders.
  type: string;
}

// Unknown states parse leniently; the teardown states are explicit so a deleting
// environment is not offered Deploy.
export type EnvironmentStatus =
  | 'registered'
  | 'provisioning'
  | 'running'
  | 'failed'
  | 'deleting'
  | 'deletion-blocked';

// The API refuses mutating lifecycle actions on these with 409.
export const TEARDOWN_STATUSES: readonly EnvironmentStatus[] = ['deleting', 'deletion-blocked'];

export function isTearingDown(environment: { status?: EnvironmentStatus }): boolean {
  return environment.status !== undefined && TEARDOWN_STATUSES.includes(environment.status);
}

export interface Environment {
  environmentId: string;
  name: string;
  type: string;
  // The row's owning tenant; differs from the caller's for an OPERATIONS caller.
  tenantId?: string;
  kubernetesContext?: string;
  contextId?: string;
  runtimeVersion?: string;
  status?: EnvironmentStatus;
  provisionError?: string;
  // What the last successful deploy installed, unlike the declared runtimeVersion.
  deployedVersion?: string;
  // The namespace's conditions verbatim, naming the finalizer holding it.
  deleteError?: string;
  // Present only once `erun expose mcp` has succeeded; absent means not exposed.
  exposedHostname?: string;
}

export type ContextStatus = 'provisioning' | 'running' | 'failed';

export interface CloudContext {
  contextId: string;
  name: string;
  provider: string;
  region: string;
  kubernetesContext?: string;
  cloudProviderAlias?: string;
  instanceType?: string;
  // Omitted for contexts registered before live provisioning existed.
  status?: ContextStatus;
  provisionError?: string;
}

export interface TenantConfigView {
  tenant: Tenant;
  environments: Environment[];
  contexts: CloudContext[];
  // Operations-only to change, but every tenant reads it to show the current window.
  inviteRequestRateLimitWindowSeconds: number;
}

// The parsers never throw on unknown fields; an unknown enum maps to undefined.

export function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null;
}

export function asString(value: unknown): string {
  return typeof value === 'string' ? value : '';
}

export function asOptionalString(value: unknown): string | undefined {
  return typeof value === 'string' && value.length > 0 ? value : undefined;
}

export function asNumber(value: unknown): number {
  return typeof value === 'number' ? value : 0;
}

export function asContextStatus(value: unknown): ContextStatus | undefined {
  return value === 'provisioning' || value === 'running' || value === 'failed' ? value : undefined;
}

export function asEnvironmentStatus(value: unknown): EnvironmentStatus | undefined {
  return value === 'registered' ||
    value === 'provisioning' ||
    value === 'running' ||
    value === 'failed' ||
    value === 'deleting' ||
    value === 'deletion-blocked'
    ? value
    : undefined;
}

export function parseTenant(raw: Record<string, unknown>): Tenant {
  return {
    tenantId: asString(raw.tenantId),
    name: asString(raw.name),
    type: asString(raw.type),
  };
}

export function parseEnvironment(raw: Record<string, unknown>): Environment {
  return {
    environmentId: asString(raw.environmentId),
    name: asString(raw.name),
    type: asString(raw.type),
    tenantId: asOptionalString(raw.tenantId),
    kubernetesContext: asOptionalString(raw.kubernetesContext),
    contextId: asOptionalString(raw.contextId),
    runtimeVersion: asOptionalString(raw.runtimeVersion),
    status: asEnvironmentStatus(raw.status),
    provisionError: asOptionalString(raw.provisionError),
    deployedVersion: asOptionalString(raw.deployedVersion),
    deleteError: asOptionalString(raw.deleteError),
    exposedHostname: asOptionalString(raw.exposedHostname),
  };
}

export function parseCloudContext(raw: Record<string, unknown>): CloudContext {
  return {
    contextId: asString(raw.contextId),
    name: asString(raw.name),
    provider: asString(raw.provider),
    region: asString(raw.region),
    kubernetesContext: asOptionalString(raw.kubernetesContext),
    cloudProviderAlias: asOptionalString(raw.cloudProviderAlias),
    instanceType: asOptionalString(raw.instanceType),
    status: asContextStatus(raw.status),
    provisionError: asOptionalString(raw.provisionError),
  };
}

export function parseList<T>(value: unknown, parse: (raw: Record<string, unknown>) => T): T[] {
  if (!Array.isArray(value)) {
    return [];
  }
  return value.filter(isRecord).map(parse);
}

// TenantConfigParseError separates a malformed body from a transport failure.
export class TenantConfigParseError extends Error {
  constructor(message: string) {
    super(message);
    this.name = 'TenantConfigParseError';
  }
}

export function parseTenantConfigView(body: unknown): TenantConfigView {
  if (!isRecord(body) || !isRecord(body.tenant)) {
    throw new TenantConfigParseError('config response was not in the expected shape');
  }
  return {
    tenant: parseTenant(body.tenant),
    environments: parseList(body.environments, parseEnvironment),
    contexts: parseList(body.contexts, parseCloudContext),
    inviteRequestRateLimitWindowSeconds: asNumber(body.inviteRequestRateLimitWindowSeconds),
  };
}
