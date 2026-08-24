// Typed client for /v1/identity/* (issue #1209): the console's IdP-identity
// administration surface. The backend drives Zitadel's Management API with
// an org-owner PAT that never reaches the browser — the console only ever
// talks to erun-backend-api, same as every other write in this app.
import { ConfigFetchError } from '../config/client';

const API_BASE = import.meta.env.VITE_API_BASE ?? '';

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null;
}

function asString(value: unknown): string {
  return typeof value === 'string' ? value : '';
}

function asOptionalString(value: unknown): string | undefined {
  return typeof value === 'string' && value.length > 0 ? value : undefined;
}

function asNumber(value: unknown): number {
  return typeof value === 'number' ? value : 0;
}

function asBoolean(value: unknown): boolean {
  return value === true;
}

function authedFetch(
  path: string,
  token: string,
  init?: Omit<RequestInit, 'headers'> & { headers?: Record<string, string> },
): Promise<Response> {
  return fetch(`${API_BASE}${path}`, {
    ...init,
    headers: {
      Authorization: `Bearer ${token}`,
      Accept: 'application/json',
      ...init?.headers,
    },
  }).catch(() => {
    throw new ConfigFetchError('could not reach the erun API', undefined);
  });
}

// An identity in the platform's IdP. State mirrors Zitadel's own
// USER_STATE_* values (e.g. USER_STATE_ACTIVE, USER_STATE_INACTIVE,
// USER_STATE_INITIAL — the last one for an invite that hasn't been
// completed yet), kept as a string for forward compatibility.
export interface IdentityUser {
  id: string;
  username: string;
  state: string;
  email?: string;
  firstName?: string;
  lastName?: string;
}

function parseIdentityUser(raw: Record<string, unknown>): IdentityUser {
  return {
    id: asString(raw.id),
    username: asString(raw.username),
    state: asString(raw.state),
    email: asOptionalString(raw.email),
    firstName: asOptionalString(raw.firstName),
    lastName: asOptionalString(raw.lastName),
  };
}

function parseIdentityUserList(value: unknown): IdentityUser[] {
  if (!Array.isArray(value)) {
    return [];
  }
  return value.filter(isRecord).map(parseIdentityUser);
}

// listIdentityUsers lists every identity in the platform's IdP.
export async function listIdentityUsers(token: string): Promise<IdentityUser[]> {
  const response = await authedFetch('/v1/identity/users', token);
  if (!response.ok) {
    throw new ConfigFetchError(
      `list identity users failed (${String(response.status)})`,
      response.status,
    );
  }
  const body: unknown = await response.json();
  return parseIdentityUserList(body);
}

export interface EnrollIdentityUserInput {
  username: string;
  email: string;
  firstName?: string;
  lastName?: string;
}

// An enrolled erun user's minimal identity, distinct from IdentityUser (the
// IdP side of the same enrollment).
export interface ErunUserRef {
  userId: string;
  username: string;
}

// EnrollIdentityUserResult always carries idpUser once the IdP half of
// enrollment succeeded. erunUser is absent and error is set when the
// erun-side mapping failed after the IdP identity was created — see
// service.IdentityService.Enroll on the backend for why that half-landed
// state is reported rather than swallowed.
export interface EnrollIdentityUserResult {
  idpUser: IdentityUser;
  erunUser?: ErunUserRef;
  error?: string;
}

// createIdentityUser enrolls a new IdP identity and its erun user mapping as
// one action. Resolves (does not throw) even when only the IdP half landed,
// so the caller can render the partial-success result.error rather than an
// opaque failure.
export async function createIdentityUser(
  token: string,
  input: EnrollIdentityUserInput,
): Promise<EnrollIdentityUserResult> {
  const response = await authedFetch('/v1/identity/users', token, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(input),
  });
  if (!response.ok) {
    throw new ConfigFetchError(
      `enroll identity user failed (${String(response.status)})`,
      response.status,
    );
  }
  const body: unknown = await response.json();
  if (!isRecord(body) || !isRecord(body.idpUser)) {
    throw new ConfigFetchError('enroll identity user response was not in the expected shape');
  }
  const erunUserRaw = body.erunUser;
  return {
    idpUser: parseIdentityUser(body.idpUser),
    erunUser:
      isRecord(erunUserRaw) && asString(erunUserRaw.userId) !== ''
        ? { userId: asString(erunUserRaw.userId), username: asString(erunUserRaw.username) }
        : undefined,
    error: asOptionalString(body.error),
  };
}

async function setIdentityUserActive(
  token: string,
  externalId: string,
  active: boolean,
): Promise<void> {
  const action = active ? 'reactivate' : 'deactivate';
  const response = await authedFetch(
    `/v1/identity/users/${encodeURIComponent(externalId)}/${action}`,
    token,
    { method: 'POST' },
  );
  if (!response.ok) {
    throw new ConfigFetchError(
      `${action} identity user failed (${String(response.status)})`,
      response.status,
    );
  }
}

// deactivateIdentityUser prevents externalId's next sign-in.
export function deactivateIdentityUser(token: string, externalId: string): Promise<void> {
  return setIdentityUserActive(token, externalId, false);
}

// reactivateIdentityUser reverses deactivateIdentityUser.
export function reactivateIdentityUser(token: string, externalId: string): Promise<void> {
  return setIdentityUserActive(token, externalId, true);
}

// The org settings an operator actually changes. verifiedDomains is
// read-only here — verifying a domain is a DNS/HTTP challenge flow this
// surface does not drive.
export interface OrgSettings {
  forceMfa: boolean;
  minPasswordLength: number;
  passwordRequiresUppercase: boolean;
  passwordRequiresLowercase: boolean;
  passwordRequiresNumber: boolean;
  passwordRequiresSymbol: boolean;
  verifiedDomains: string[];
}

function parseOrgSettings(raw: Record<string, unknown>): OrgSettings {
  return {
    forceMfa: asBoolean(raw.forceMfa),
    minPasswordLength: asNumber(raw.minPasswordLength),
    passwordRequiresUppercase: asBoolean(raw.passwordRequiresUppercase),
    passwordRequiresLowercase: asBoolean(raw.passwordRequiresLowercase),
    passwordRequiresNumber: asBoolean(raw.passwordRequiresNumber),
    passwordRequiresSymbol: asBoolean(raw.passwordRequiresSymbol),
    verifiedDomains: Array.isArray(raw.verifiedDomains)
      ? raw.verifiedDomains.filter((d) => typeof d === 'string')
      : [],
  };
}

// getOrgSettings reads the platform IdP org's current login and password
// policy, and its verified domains.
export async function getOrgSettings(token: string): Promise<OrgSettings> {
  const response = await authedFetch('/v1/identity/org-settings', token);
  if (!response.ok) {
    throw new ConfigFetchError(
      `get org settings failed (${String(response.status)})`,
      response.status,
    );
  }
  const body: unknown = await response.json();
  if (!isRecord(body)) {
    throw new ConfigFetchError('org settings response was not in the expected shape');
  }
  return parseOrgSettings(body);
}

// UpdateOrgSettingsInput carries only the fields the operator wants to
// change; every other field is left at its current value server-side.
export interface UpdateOrgSettingsInput {
  forceMfa?: boolean;
  minPasswordLength?: number;
  passwordRequiresUppercase?: boolean;
  passwordRequiresLowercase?: boolean;
  passwordRequiresNumber?: boolean;
  passwordRequiresSymbol?: boolean;
}

// updateOrgSettings applies input and returns the org's full settings after
// the update.
export async function updateOrgSettings(
  token: string,
  input: UpdateOrgSettingsInput,
): Promise<OrgSettings> {
  const response = await authedFetch('/v1/identity/org-settings', token, {
    method: 'PATCH',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(input),
  });
  if (!response.ok) {
    throw new ConfigFetchError(
      `update org settings failed (${String(response.status)})`,
      response.status,
    );
  }
  const body: unknown = await response.json();
  if (!isRecord(body)) {
    throw new ConfigFetchError('org settings response was not in the expected shape');
  }
  return parseOrgSettings(body);
}
