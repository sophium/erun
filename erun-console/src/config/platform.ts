// Runtime platform discovery: GET /v1/platform, unauthenticated, so the console
// can resolve its own OIDC issuer + client id and brand before sign-in rather
// than baking them into the built image (#603 "installable as an independent
// instance" — one image must be able to serve any PaaS instance). Every field
// is optional and empty-string when the backend has it unset; an older backend
// that predates this endpoint (404) or an unreachable one both resolve to
// `undefined` here so the caller can fall back to the VITE_* local-dev config.

const API_BASE = import.meta.env.VITE_API_BASE ?? '';

export interface PlatformConfig {
  issuer: string;
  apiUrl: string;
  consoleUrl: string;
  consoleClientId: string;
  cliClientId: string;
  brand: string;
  docsUrl: string;
  tagline: string;
  logoUrl: string;
}

function asString(value: unknown): string {
  return typeof value === 'string' ? value : '';
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null;
}

// fetchPlatformConfig returns undefined when the endpoint is absent (404), the
// backend is unreachable, or the response is not JSON-shaped — every case a
// caller should treat the same way: fall back, don't hard-fail.
export async function fetchPlatformConfig(): Promise<PlatformConfig | undefined> {
  let response: Response;
  try {
    response = await fetch(`${API_BASE}/v1/platform`, { headers: { Accept: 'application/json' } });
  } catch {
    return undefined;
  }
  if (!response.ok) {
    return undefined;
  }
  let body: unknown;
  try {
    body = await response.json();
  } catch {
    return undefined;
  }
  if (!isRecord(body)) {
    return undefined;
  }
  return {
    issuer: asString(body.issuer),
    apiUrl: asString(body.apiUrl),
    consoleUrl: asString(body.consoleUrl),
    consoleClientId: asString(body.consoleClientId),
    cliClientId: asString(body.cliClientId),
    brand: asString(body.brand),
    docsUrl: asString(body.docsUrl),
    tagline: asString(body.tagline),
    logoUrl: asString(body.logoUrl),
  };
}
