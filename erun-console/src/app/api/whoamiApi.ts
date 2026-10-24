// RTK Query endpoint for GET /v1/whoami: identity and effective capabilities.
import { asOptionalString, asString, isRecord } from 'erun-kit';

import { platformApi } from './platformApi';

// A canonical route template, never a concrete request URL.
export interface PlatformCapability {
  method: string;
  path: string;
}

// capabilities is undefined, not empty, when the set is unknown; never conflate the two.
export interface Whoami {
  tenantId: string;
  userId: string;
  username?: string;
  roles?: string[];
  capabilities?: PlatformCapability[];
  issuer: string;
  subject: string;
}

function asStringArray(value: unknown): string[] | undefined {
  if (!Array.isArray(value)) {
    return undefined;
  }
  return value.filter((entry): entry is string => typeof entry === 'string');
}

function parseCapability(raw: Record<string, unknown>): PlatformCapability {
  return { method: asString(raw.method), path: asString(raw.path) };
}

function parseCapabilities(value: unknown): PlatformCapability[] | undefined {
  if (!Array.isArray(value)) {
    return undefined;
  }
  return value.filter(isRecord).map(parseCapability);
}

function parseWhoami(raw: unknown): Whoami {
  if (!isRecord(raw)) {
    throw new Error('whoami response was not in the expected shape');
  }
  return {
    tenantId: asString(raw.tenantId),
    userId: asString(raw.userId),
    username: asOptionalString(raw.username),
    roles: asStringArray(raw.roles),
    capabilities: parseCapabilities(raw.capabilities),
    issuer: asString(raw.issuer),
    subject: asString(raw.subject),
  };
}

export const whoamiApi = platformApi.injectEndpoints({
  endpoints: (builder) => ({
    getWhoami: builder.query<Whoami, string>({
      query: (token) => ({ url: '/v1/whoami', token, label: 'whoami' }),
      transformResponse: parseWhoami,
    }),
  }),
});

export const { useGetWhoamiQuery } = whoamiApi;
