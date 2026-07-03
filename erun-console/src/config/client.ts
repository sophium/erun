import type { CloudContext, ContextStatus, Environment, Tenant, TenantConfigView } from './types';

// No separate BFF in this increment — the console calls the auth-carrying
// erun-backend-api directly. Same-origin default lets the SPA sit behind the
// same auth edge that fronts the API.
const API_BASE = import.meta.env.VITE_API_BASE ?? '';

// ConfigFetchError carries the HTTP `status` when the server responded (undefined
// for a transport-level failure); ConfigView keys its sign-in prompt off
// `status === 401`.
export class ConfigFetchError extends Error {
  readonly status?: number;

  constructor(message: string, status?: number) {
    super(message);
    this.name = 'ConfigFetchError';
    this.status = status;
  }
}

function asString(value: unknown): string {
  return typeof value === 'string' ? value : '';
}

function asOptionalString(value: unknown): string | undefined {
  return typeof value === 'string' && value.length > 0 ? value : undefined;
}

// An unknown or absent status maps to undefined so the UI shows no badge rather
// than a misleading one.
function asContextStatus(value: unknown): ContextStatus | undefined {
  return value === 'provisioning' || value === 'running' || value === 'failed' ? value : undefined;
}

function parseTenant(raw: Record<string, unknown>): Tenant {
  return {
    tenantId: asString(raw.tenantId),
    name: asString(raw.name),
    type: asString(raw.type),
  };
}

function parseEnvironment(raw: Record<string, unknown>): Environment {
  return {
    environmentId: asString(raw.environmentId),
    name: asString(raw.name),
    type: asString(raw.type),
    kubernetesContext: asOptionalString(raw.kubernetesContext),
    contextId: asOptionalString(raw.contextId),
    runtimeVersion: asOptionalString(raw.runtimeVersion),
  };
}

function parseContext(raw: Record<string, unknown>): CloudContext {
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

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null;
}

function parseList<T>(value: unknown, parse: (raw: Record<string, unknown>) => T): T[] {
  if (!Array.isArray(value)) {
    return [];
  }
  return value.filter(isRecord).map(parse);
}

function parseConfig(body: unknown): TenantConfigView {
  if (!isRecord(body) || !isRecord(body.tenant)) {
    throw new ConfigFetchError('config response was not in the expected shape');
  }
  return {
    tenant: parseTenant(body.tenant),
    environments: parseList(body.environments, parseEnvironment),
    contexts: parseList(body.contexts, parseContext),
  };
}

// fetchConfig loads the tenant's erun read model for the console's read view.
export async function fetchConfig(token: string): Promise<TenantConfigView> {
  let response: Response;
  try {
    response = await fetch(`${API_BASE}/v1/config`, {
      headers: {
        Authorization: `Bearer ${token}`,
        Accept: 'application/json',
      },
    });
  } catch {
    throw new ConfigFetchError('could not reach the erun API', undefined);
  }

  if (!response.ok) {
    throw new ConfigFetchError(
      `config request failed (${String(response.status)})`,
      response.status,
    );
  }

  const body: unknown = await response.json();
  return parseConfig(body);
}

function authHeaders(token: string): Record<string, string> {
  return {
    Authorization: `Bearer ${token}`,
    Accept: 'application/json',
  };
}

async function authedFetch(
  path: string,
  token: string,
  init?: Omit<RequestInit, 'headers'> & { headers?: Record<string, string> },
): Promise<Response> {
  try {
    return await fetch(`${API_BASE}${path}`, {
      ...init,
      headers: { ...authHeaders(token), ...init?.headers },
    });
  } catch {
    throw new ConfigFetchError('could not reach the erun API', undefined);
  }
}

// The BYO-cloud credentials an operator registers under an alias. `provider`
// defaults to aws server-side; `credentials` is an opaque provider-specific JSON
// string the API encrypts at rest (never returned).
export interface CloudProviderAliasInput {
  provider?: string;
  credentials: string;
}

// setCloudProviderAlias registers or updates the tenant's BYO-cloud credentials
// under `alias`.
export async function setCloudProviderAlias(
  token: string,
  alias: string,
  input: CloudProviderAliasInput,
): Promise<void> {
  const response = await authedFetch(
    `/v1/cloud-provider-aliases/${encodeURIComponent(alias)}`,
    token,
    {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(input),
    },
  );
  if (!response.ok) {
    throw new ConfigFetchError(
      `alias request failed (${String(response.status)})`,
      response.status,
    );
  }
}

// The fields needed to register (provision) a cloud context.
export interface CreateContextInput {
  name: string;
  cloudProviderAlias: string;
  region: string;
}

// createContext registers a cloud context and kicks off its live provisioning;
// it returns while still `provisioning`, so poll getContext to follow it to
// `running`/`failed`.
export async function createContext(
  token: string,
  input: CreateContextInput,
): Promise<CloudContext> {
  const response = await authedFetch('/v1/contexts', token, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(input),
  });
  if (!response.ok) {
    throw new ConfigFetchError(
      `create context request failed (${String(response.status)})`,
      response.status,
    );
  }
  const body: unknown = await response.json();
  if (!isRecord(body) || !isRecord(body.context)) {
    throw new ConfigFetchError('create context response was not in the expected shape');
  }
  return parseContext(body.context);
}

// getContext fetches one cloud context by id; the console polls it after
// createContext until `status` reaches `running`/`failed`.
export async function getContext(token: string, contextId: string): Promise<CloudContext> {
  const response = await authedFetch(`/v1/contexts/${encodeURIComponent(contextId)}`, token);
  if (!response.ok) {
    throw new ConfigFetchError(
      `context request failed (${String(response.status)})`,
      response.status,
    );
  }
  const body: unknown = await response.json();
  if (!isRecord(body)) {
    throw new ConfigFetchError('context response was not in the expected shape');
  }
  return parseContext(body);
}
