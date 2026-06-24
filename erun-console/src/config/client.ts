import type { CloudContext, Environment, Tenant, TenantConfigView } from './types';

// Base URL of the erun-backend-api. The console calls the API directly — there
// is no separate BFF service in this increment (the API already carries the
// OIDC/JWKS auth middleware, the tenant boundary, and GET /v1/config). Defaults
// to same-origin so the SPA can be served behind the same auth edge that fronts
// the API.
const API_BASE = import.meta.env.VITE_API_BASE ?? '';

// Error raised when the config fetch fails. `status` is the HTTP status when the
// server responded (e.g. 401 for an unauthenticated/expired token), or undefined
// for a transport-level failure (network down, CORS, etc.). The ConfigView keys
// its sign-in prompt off `status === 401`.
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

// Fetch the tenant's erun read model from `GET ${API_BASE}/v1/config` with the
// caller's bearer token. Resolves to the parsed `{ tenant, environments,
// contexts }`; rejects with a ConfigFetchError (carrying the HTTP status when
// one is available) on any non-2xx response or transport failure.
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
