// RTK Query endpoints for /v1/identity/* (issue #1209): the console's
// IdP-identity administration surface. The backend drives Zitadel's
// Management API with an org-owner PAT that never reaches the browser — the
// console only ever talks to erun-backend-api, same as every other write in
// this app.
import { asOptionalString, asString, isRecord } from 'erun-kit';

import { type NoValue, platformApi } from './platformApi';

// An identity in the platform's IdP. State mirrors Zitadel's own
// USER_STATE_* values (e.g. USER_STATE_ACTIVE, USER_STATE_INACTIVE,
// USER_STATE_INITIAL — the last one for an invite that hasn't been completed
// yet), kept as a string for forward compatibility.
export interface IdentityUser {
  id: string;
  username: string;
  state: string;
  email?: string;
  firstName?: string;
  lastName?: string;
}

function asNumber(value: unknown): number {
  return typeof value === 'number' ? value : 0;
}

function asBoolean(value: unknown): boolean {
  return value === true;
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
//
// mailDeliveryConfigured/temporaryPassword/warning report which of the two
// enrollment paths the backend actually took: with mail configured, the IdP
// emails the invite itself; without it, the backend mints temporaryPassword
// once instead so the account is usable immediately rather than stuck
// waiting on an email that can never arrive.
export interface EnrollIdentityUserResult {
  idpUser: IdentityUser;
  erunUser?: ErunUserRef;
  error?: string;
  mailDeliveryConfigured: boolean;
  temporaryPassword?: string;
  warning?: string;
}

function parseEnrollResult(raw: unknown): EnrollIdentityUserResult {
  if (!isRecord(raw) || !isRecord(raw.idpUser)) {
    throw new Error('enroll identity user response was not in the expected shape');
  }
  const erunUserRaw = raw.erunUser;
  return {
    idpUser: parseIdentityUser(raw.idpUser),
    erunUser:
      isRecord(erunUserRaw) && asString(erunUserRaw.userId) !== ''
        ? { userId: asString(erunUserRaw.userId), username: asString(erunUserRaw.username) }
        : undefined,
    error: asOptionalString(raw.error),
    mailDeliveryConfigured: asBoolean(raw.mailDeliveryConfigured),
    temporaryPassword: asOptionalString(raw.temporaryPassword),
    warning: asOptionalString(raw.warning),
  };
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

function parseOrgSettings(raw: unknown): OrgSettings {
  if (!isRecord(raw)) {
    throw new Error('org settings response was not in the expected shape');
  }
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

// The platform's outbound-mail configuration. Password is never part of the
// read shape — Zitadel does not return it, and this contract only ever
// writes it.
export interface SmtpConfig {
  host: string;
  username: string;
  senderAddress: string;
  senderName: string;
  replyToAddress?: string;
  tls: boolean;
}

// The platform's honest answer to "can this instance send mail at all".
// configured: false means no active config exists yet — the default for a
// freshly deployed platform.
export interface SmtpStatus {
  configured: boolean;
  config: SmtpConfig;
}

function parseSmtpConfig(raw: unknown): SmtpConfig {
  if (!isRecord(raw)) {
    return { host: '', username: '', senderAddress: '', senderName: '', tls: false };
  }
  return {
    host: asString(raw.host),
    username: asString(raw.user),
    senderAddress: asString(raw.senderAddress),
    senderName: asString(raw.senderName),
    replyToAddress: asOptionalString(raw.replyToAddress),
    tls: asBoolean(raw.tls),
  };
}

function parseSmtpStatus(raw: unknown): SmtpStatus {
  if (!isRecord(raw)) {
    throw new Error('smtp settings response was not in the expected shape');
  }
  return { configured: asBoolean(raw.configured), config: parseSmtpConfig(raw.config) };
}

// UpdateSmtpSettingsInput is the declarative desired state the operator
// submits. password is omitted on an update that only changes non-secret
// fields — the backend leaves Zitadel's stored password untouched — and is
// required only the first time a config is created.
export interface UpdateSmtpSettingsInput {
  host: string;
  username?: string;
  password?: string;
  senderAddress: string;
  senderName?: string;
  replyToAddress?: string;
  tls: boolean;
}

export const identityApi = platformApi.injectEndpoints({
  endpoints: (builder) => ({
    listIdentityUsers: builder.query<IdentityUser[], string>({
      query: (token) => ({ url: '/v1/identity/users', token, label: 'list identity users' }),
      transformResponse: parseIdentityUserList,
      providesTags: ['IdentityUsers'],
    }),

    // createIdentityUser enrolls a new IdP identity and its erun user mapping
    // as one action. Resolves (does not error) even when only the IdP half
    // landed, so the caller can render the partial-success result.error
    // rather than an opaque failure.
    createIdentityUser: builder.mutation<
      EnrollIdentityUserResult,
      { token: string; input: EnrollIdentityUserInput }
    >({
      query: ({ token, input }) => ({
        url: '/v1/identity/users',
        method: 'POST',
        body: input,
        token,
        label: 'enroll identity user',
      }),
      transformResponse: parseEnrollResult,
      invalidatesTags: ['IdentityUsers'],
    }),

    setIdentityUserActive: builder.mutation<
      NoValue,
      { token: string; externalId: string; active: boolean }
    >({
      query: ({ token, externalId, active }) => ({
        url: `/v1/identity/users/${encodeURIComponent(externalId)}/${active ? 'reactivate' : 'deactivate'}`,
        method: 'POST',
        token,
        label: active ? 'reactivate identity user' : 'deactivate identity user',
      }),
      invalidatesTags: ['IdentityUsers'],
    }),

    // getOrgSettings reads the platform IdP org's current login and password
    // policy, and its verified domains.
    getOrgSettings: builder.query<OrgSettings, string>({
      query: (token) => ({ url: '/v1/identity/org-settings', token, label: 'get org settings' }),
      transformResponse: parseOrgSettings,
      providesTags: ['OrgSettings'],
    }),

    // updateOrgSettings applies input and returns the org's full settings
    // after the update.
    updateOrgSettings: builder.mutation<
      OrgSettings,
      { token: string; input: UpdateOrgSettingsInput }
    >({
      query: ({ token, input }) => ({
        url: '/v1/identity/org-settings',
        method: 'PATCH',
        body: input,
        token,
        label: 'update org settings',
      }),
      transformResponse: parseOrgSettings,
      invalidatesTags: ['OrgSettings'],
    }),

    // getSmtpSettings reads the platform's active outbound-mail
    // configuration — configured: false, not an error, when none exists yet.
    getSmtpSettings: builder.query<SmtpStatus, string>({
      query: (token) => ({ url: '/v1/identity/smtp-settings', token, label: 'get smtp settings' }),
      transformResponse: parseSmtpStatus,
      providesTags: ['SmtpSettings'],
    }),

    // updateSmtpSettings converges the configuration to input and returns
    // the resulting status, same shape as the GET above.
    updateSmtpSettings: builder.mutation<
      SmtpStatus,
      { token: string; input: UpdateSmtpSettingsInput }
    >({
      query: ({ token, input }) => ({
        url: '/v1/identity/smtp-settings',
        method: 'PATCH',
        body: input,
        token,
        label: 'update smtp settings',
      }),
      transformResponse: parseSmtpStatus,
      invalidatesTags: ['SmtpSettings'],
    }),
  }),
});

export const {
  useListIdentityUsersQuery,
  useCreateIdentityUserMutation,
  useSetIdentityUserActiveMutation,
  useGetOrgSettingsQuery,
  useUpdateOrgSettingsMutation,
  useGetSmtpSettingsQuery,
  useUpdateSmtpSettingsMutation,
} = identityApi;
