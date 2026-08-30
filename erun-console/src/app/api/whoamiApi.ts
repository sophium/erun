// RTK Query endpoint for GET /v1/whoami: the caller's own identity and
// effective permission set. The console had no whoami/capability plumbing
// before the invite-requests queue — its Approve/Decline
// actions are the first console surface gated on a per-action capability
// rather than on tenant.type, so this is the shape that lands rather than a
// component-local guess. See app/capabilities.ts for the matching helper and
// erun-console/AGENTS.md's "Permission degradation".
import { asOptionalString, asString, isRecord } from 'erun-kit';

import { platformApi } from './platformApi';

// PlatformCapability mirrors erun-common's own type exactly (Go:
// erun-common/platform_capabilities.go): a canonical route template such as
// `/v1/invite-requests/{invite_request_id}/approve`, never a concrete
// request URL.
export interface PlatformCapability {
  method: string;
  path: string;
}

// Whoami is GET /v1/whoami's response. capabilities is deliberately
// undefined (not an empty array) when the platform could not resolve a set
// at all — see erun-common's PlatformCapabilities.Known() doc: an unknown
// set is not an empty one, and a client must not conflate them.
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
