// Unauthenticated GET /v1/platform so one built image can resolve its OIDC config and
// brand at runtime. Unset fields are empty strings.

const API_BASE = import.meta.env.VITE_API_BASE ?? '';

export interface PlatformConfig {
  issuer: string;
  apiUrl: string;
  consoleUrl: string;
  consoleClientId: string;
  cliClientId: string;
  mobileClientId: string;
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

// fetchPlatformConfig returns undefined on 404, network failure or a non-JSON body; callers fall back.
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
    mobileClientId: asString(body.mobileClientId),
    brand: asString(body.brand),
    docsUrl: asString(body.docsUrl),
    tagline: asString(body.tagline),
    logoUrl: asString(body.logoUrl),
  };
}
