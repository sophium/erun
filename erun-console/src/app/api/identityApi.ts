// RTK Query endpoints for /v1/identity/*; the backend holds the IdP PAT, never the browser.
import { asOptionalString, asString, isRecord } from 'erun-kit';

import { type NoValue, platformApi } from './platformApi';

// An identity in the IdP org (not erun's membership). state mirrors Zitadel's
// USER_STATE_* as a string; isMachine marks platform service identities.
export interface IdentityUser {
  id: string;
  username: string;
  state: string;
  email?: string;
  firstName?: string;
  lastName?: string;
  isMachine: boolean;
  enrolled: boolean;
  erunUserId?: string;
  // Chosen at enrolment, independent of the IdP username; undefined unless enrolled.
  erunUsername?: string;
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
    isMachine: asBoolean(raw.isMachine),
    enrolled: asBoolean(raw.enrolled),
    erunUserId: asOptionalString(raw.erunUserId),
    erunUsername: asOptionalString(raw.erunUsername),
  };
}

function parseIdentityUserList(value: unknown): IdentityUser[] {
  if (!Array.isArray(value)) {
    return [];
  }
  return value.filter(isRecord).map(parseIdentityUser);
}

// orgId targets another Zitadel org; omitted, the identity lands in the platform's own.
export interface EnrollIdentityUserInput {
  username: string;
  email: string;
  firstName?: string;
  lastName?: string;
  orgId?: string;
}

export interface ErunUserRef {
  userId: string;
  username: string;
}

// erunUser is absent and error set when the IdP identity was created but the erun
// mapping failed. Without mail configured the backend returns a one-time temporaryPassword.
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

// verifiedDomains is read-only; domain verification is not driven here.
export interface OrgSettings {
  forceMfa: boolean;
  allowRegister: boolean;
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
    allowRegister: asBoolean(raw.allowRegister),
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

export interface UpdateOrgSettingsInput {
  forceMfa?: boolean;
  allowRegister?: boolean;
  minPasswordLength?: number;
  passwordRequiresUppercase?: boolean;
  passwordRequiresLowercase?: boolean;
  passwordRequiresNumber?: boolean;
  passwordRequiresSymbol?: boolean;
}

// Password is write-only: Zitadel never returns it.
export interface SmtpConfig {
  host: string;
  username: string;
  senderAddress: string;
  senderName: string;
  replyToAddress?: string;
  tls: boolean;
}

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

// password is omitted to keep the stored one; required only when first creating the config.
export interface UpdateSmtpSettingsInput {
  host: string;
  username?: string;
  password?: string;
  senderAddress: string;
  senderName?: string;
  replyToAddress?: string;
  tls: boolean;
}

// token is the invite credential; the console builds the accept URL locally because
// the backend does not know its public console origin.
export interface Invite {
  inviteId: string;
  tenantId: string;
  token: string;
  email?: string;
  expiresAt: string;
  consumedAt?: string;
}

function parseInvite(raw: Record<string, unknown>): Invite {
  return {
    inviteId: asString(raw.inviteId),
    tenantId: asString(raw.tenantId),
    token: asString(raw.token),
    email: asOptionalString(raw.email),
    expiresAt: asString(raw.expiresAt),
    consumedAt: asOptionalString(raw.consumedAt),
  };
}

function parseInviteList(value: unknown): Invite[] {
  if (!Array.isArray(value)) {
    return [];
  }
  return value.filter(isRecord).map(parseInvite);
}

// tenantId is honored only for an operations-scoped caller.
export interface CreateInviteInput {
  email?: string;
  tenantId?: string;
}

// No bearer token: the invite token in the body is the credential.
export interface AcceptInviteInput {
  token: string;
  username: string;
  email?: string;
  firstName?: string;
  lastName?: string;
  password: string;
}

export interface AcceptInviteResult {
  idpUser: IdentityUser;
  erunUser?: ErunUserRef;
  error?: string;
}

function parseAcceptInviteResult(raw: unknown): AcceptInviteResult {
  if (!isRecord(raw) || !isRecord(raw.idpUser)) {
    throw new Error('accept invite response was not in the expected shape');
  }
  const erunUserRaw = raw.erunUser;
  return {
    idpUser: parseIdentityUser(raw.idpUser),
    erunUser:
      isRecord(erunUserRaw) && asString(erunUserRaw.userId) !== ''
        ? { userId: asString(erunUserRaw.userId), username: asString(erunUserRaw.username) }
        : undefined,
    error: asOptionalString(raw.error),
  };
}

export const identityApi = platformApi.injectEndpoints({
  endpoints: (builder) => ({
    // orgId picks the IdP org; tenantId picks the tenant whose erun users are cross-referenced.
    // Both are needed to administer another tenant.
    listIdentityUsers: builder.query<
      IdentityUser[],
      { token: string; orgId?: string; tenantId?: string }
    >({
      query: ({ token, orgId, tenantId }) => {
        const params = new URLSearchParams();
        if (orgId !== undefined && orgId !== '') {
          params.set('orgId', orgId);
        }
        if (tenantId !== undefined && tenantId !== '') {
          params.set('tenantId', tenantId);
        }
        const query = params.toString();
        return {
          url: query === '' ? '/v1/identity/users' : `/v1/identity/users?${query}`,
          token,
          label: 'list identity users',
        };
      },
      transformResponse: parseIdentityUserList,
      providesTags: ['IdentityUsers'],
    }),

    // Resolves even when only the IdP half landed, so the caller can render result.error.
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

    // orgId must match the scoped list read so the target tenant's account is the one changed.
    setIdentityUserActive: builder.mutation<
      NoValue,
      { token: string; externalId: string; active: boolean; orgId?: string }
    >({
      query: ({ token, externalId, active, orgId }) => ({
        url: `/v1/identity/users/${encodeURIComponent(externalId)}/${active ? 'reactivate' : 'deactivate'}${
          orgId !== undefined && orgId !== '' ? `?orgId=${encodeURIComponent(orgId)}` : ''
        }`,
        method: 'POST',
        token,
        label: active ? 'reactivate identity user' : 'deactivate identity user',
      }),
      invalidatesTags: ['IdentityUsers'],
    }),

    getOrgSettings: builder.query<OrgSettings, string>({
      query: (token) => ({ url: '/v1/identity/org-settings', token, label: 'get org settings' }),
      transformResponse: parseOrgSettings,
      providesTags: ['OrgSettings'],
    }),

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

    getSmtpSettings: builder.query<SmtpStatus, string>({
      query: (token) => ({ url: '/v1/identity/smtp-settings', token, label: 'get smtp settings' }),
      transformResponse: parseSmtpStatus,
      providesTags: ['SmtpSettings'],
    }),

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

// Split from identityApi to stay under the max-lines-per-function budget.
export const inviteApi = platformApi.injectEndpoints({
  endpoints: (builder) => ({
    listInvites: builder.query<Invite[], string>({
      query: (token) => ({ url: '/v1/invites', token, label: 'list invites' }),
      transformResponse: parseInviteList,
      providesTags: ['Invites'],
    }),

    createInvite: builder.mutation<Invite, { token: string; input: CreateInviteInput }>({
      query: ({ token, input }) => ({
        url: '/v1/invites',
        method: 'POST',
        body: input,
        token,
        label: 'create invite',
      }),
      transformResponse: parseInvite,
      invalidatesTags: ['Invites'],
    }),

    revokeInvite: builder.mutation<NoValue, { token: string; inviteId: string }>({
      query: ({ token, inviteId }) => ({
        url: `/v1/invites/${encodeURIComponent(inviteId)}`,
        method: 'DELETE',
        token,
        label: 'revoke invite',
      }),
      invalidatesTags: ['Invites'],
    }),

    // No bearer token: the invite token in the body is the credential.
    acceptInvite: builder.mutation<AcceptInviteResult, AcceptInviteInput>({
      query: (input) => ({
        url: '/v1/invites/accept',
        method: 'POST',
        body: input,
        label: 'accept invite',
      }),
      transformResponse: parseAcceptInviteResult,
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

export const {
  useListInvitesQuery,
  useCreateInviteMutation,
  useRevokeInviteMutation,
  useAcceptInviteMutation,
} = inviteApi;
