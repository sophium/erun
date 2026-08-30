// Types and lenient parsers mirroring the wire shape of `GET /v1/config` on
// erun-backend-api — the per-tenant erun read model. This is the
// erun-backend-api contract itself, not a console-only concept, so it lives
// here rather than in erun-console: any transport that talks to the hosted
// platform API resolves the same model from the same bytes. See
// erun-docs/docs/agent-reference/api-protocol.md § Endpoints.

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
// so callers must not offer it.
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
  // The platform-wide POST /v1/invite-requests admission window (issue
  // #1682 §9), changed only through PATCH /v1/config/invite-request-rate-limit
  // — an operations-only write. Every tenant reads it (the console's
  // rate-limit editor is gated on tenant type, not on this field being
  // present), since a COMPANY tenant's requests panel still needs to show
  // the current window even though it cannot change it.
  inviteRequestRateLimitWindowSeconds: number;
}

// The lenient parsers below turn untyped JSON into the shapes above without
// throwing on an unrecognised field or enum value — an unknown status maps to
// undefined (no badge) rather than a misleading one, and a forward-compatible
// backend never breaks an older console build.

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
    kubernetesContext: asOptionalString(raw.kubernetesContext),
    contextId: asOptionalString(raw.contextId),
    runtimeVersion: asOptionalString(raw.runtimeVersion),
    status: asEnvironmentStatus(raw.status),
    provisionError: asOptionalString(raw.provisionError),
    deployedVersion: asOptionalString(raw.deployedVersion),
    deleteError: asOptionalString(raw.deleteError),
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

// TenantConfigParseError distinguishes a malformed `GET /v1/config` body from
// a transport-level failure, mirroring the desktop's WailsQueryError shape
// closely enough that a caller can treat both the same way.
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
