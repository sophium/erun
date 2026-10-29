// RTK Query endpoints for the invite-request queue and its rate-limit editor.
import { asOptionalString, asString, isRecord } from 'erun-kit';

import { platformApi } from './platformApi';

export type InviteRequestKind = 'JOIN_TENANT' | 'CREATE_TENANT';
export type InviteRequestStatus = 'PENDING' | 'APPROVED' | 'DECLINED';

// InviteRequest mirrors erun-backend-api's model.InviteRequest wire shape.
export interface InviteRequest {
  inviteRequestId: string;
  issuer: string;
  subject: string;
  email?: string;
  displayName?: string;
  kind: InviteRequestKind;
  tenantName: string;
  environmentName?: string;
  note?: string;
  status: InviteRequestStatus;
  decidedByUserId?: string;
  declineReason?: string;
  mintedInviteId?: string;
  mintedInviteToken?: string;
  mintedInviteExpiresAt?: string;
  createdAt: string;
  updatedAt: string;
}

function asInviteRequestKind(value: unknown): InviteRequestKind {
  return value === 'CREATE_TENANT' ? 'CREATE_TENANT' : 'JOIN_TENANT';
}

function asInviteRequestStatus(value: unknown): InviteRequestStatus {
  return value === 'APPROVED' || value === 'DECLINED' ? value : 'PENDING';
}

function parseInviteRequest(raw: Record<string, unknown>): InviteRequest {
  return {
    inviteRequestId: asString(raw.inviteRequestId),
    issuer: asString(raw.issuer),
    subject: asString(raw.subject),
    email: asOptionalString(raw.email),
    displayName: asOptionalString(raw.displayName),
    kind: asInviteRequestKind(raw.kind),
    tenantName: asString(raw.tenantName),
    environmentName: asOptionalString(raw.environmentName),
    note: asOptionalString(raw.note),
    status: asInviteRequestStatus(raw.status),
    decidedByUserId: asOptionalString(raw.decidedByUserId),
    declineReason: asOptionalString(raw.declineReason),
    mintedInviteId: asOptionalString(raw.mintedInviteId),
    mintedInviteToken: asOptionalString(raw.mintedInviteToken),
    mintedInviteExpiresAt: asOptionalString(raw.mintedInviteExpiresAt),
    createdAt: asString(raw.createdAt),
    updatedAt: asString(raw.updatedAt),
  };
}

function parseInviteRequestList(value: unknown): InviteRequest[] {
  if (!Array.isArray(value)) {
    return [];
  }
  return value.filter(isRecord).map(parseInviteRequest);
}

function parseInviteRequestResponse(raw: unknown): InviteRequest {
  if (!isRecord(raw)) {
    throw new Error('invite request response was not in the expected shape');
  }
  return parseInviteRequest(raw);
}

export interface PlatformRateLimit {
  inviteRequestWindowSeconds: number;
  createdAt: string;
  updatedAt: string;
}

function parsePlatformRateLimit(raw: unknown): PlatformRateLimit {
  if (!isRecord(raw)) {
    throw new Error('rate limit response was not in the expected shape');
  }
  return {
    inviteRequestWindowSeconds:
      typeof raw.inviteRequestWindowSeconds === 'number' ? raw.inviteRequestWindowSeconds : 0,
    createdAt: asString(raw.createdAt),
    updatedAt: asString(raw.updatedAt),
  };
}

// Shared by the nav badge and the panel so RTK Query runs one poll. Polled because an
// approval can land in another session with nothing local to react to.
export const PENDING_REQUESTS_POLL_MS = 30000;

export const requestsApi = platformApi.injectEndpoints({
  endpoints: (builder) => ({
    // Pending only: the view is a work queue, not a history.
    listInviteRequests: builder.query<InviteRequest[], string>({
      query: (token) => ({
        url: '/v1/invite-requests?status=PENDING',
        token,
        label: 'list invite requests',
      }),
      transformResponse: parseInviteRequestList,
      providesTags: ['PendingRequests'],
    }),

    approveInviteRequest: builder.mutation<InviteRequest, { token: string; id: string }>({
      query: ({ token, id }) => ({
        url: `/v1/invite-requests/${encodeURIComponent(id)}/approve`,
        method: 'POST',
        token,
        label: 'approve invite request',
      }),
      transformResponse: parseInviteRequestResponse,
      invalidatesTags: ['PendingRequests'],
    }),

    declineInviteRequest: builder.mutation<
      InviteRequest,
      { token: string; id: string; reason: string }
    >({
      query: ({ token, id, reason }) => ({
        url: `/v1/invite-requests/${encodeURIComponent(id)}/decline`,
        method: 'POST',
        body: { reason },
        token,
        label: 'decline invite request',
      }),
      transformResponse: parseInviteRequestResponse,
      invalidatesTags: ['PendingRequests'],
    }),

    // Invalidates 'Config': the window is read back through GET /v1/config.
    setInviteRequestRateLimit: builder.mutation<
      PlatformRateLimit,
      { token: string; windowSeconds: number }
    >({
      query: ({ token, windowSeconds }) => ({
        url: '/v1/config/invite-request-rate-limit',
        method: 'PATCH',
        body: { windowSeconds },
        token,
        label: 'set invite request rate limit',
      }),
      transformResponse: parsePlatformRateLimit,
      invalidatesTags: ['Config'],
    }),
  }),
});

export const {
  useListInviteRequestsQuery,
  useApproveInviteRequestMutation,
  useDeclineInviteRequestMutation,
  useSetInviteRequestRateLimitMutation,
} = requestsApi;
