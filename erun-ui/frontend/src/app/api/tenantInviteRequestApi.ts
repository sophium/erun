// tenantInviteRequestApi wraps the invite-requests Wails methods
// (tenant_platform_invite_requests.go): submitting a request, checking the
// caller's own status, the operator/admin queue's approve/decline, and the
// sidebar's per-tenant enrollment status poll.

import type {
  UIDecideInviteRequestInput,
  UIDeclineInviteRequestInput,
  UIInviteRequest,
  UIListTenantPlatformEnrollmentStatusesInput,
  UISubmitInviteRequestInput,
  UISubmitInviteRequestOutcome,
  UITenantInput,
  UITenantPlatformEnrollmentStatus,
} from '@/types';

import {
  ApproveTenantInviteRequest,
  DeclineTenantInviteRequest,
  GetMyTenantInviteRequest,
  ListTenantPlatformEnrollmentStatuses,
  SubmitTenantInviteRequest,
} from '../../../wailsjs/go/main/App';
import { wailsApi } from './wailsApi';
import { wailsQueryFn } from './wailsBaseQuery';

export const tenantInviteRequestApi = wailsApi.injectEndpoints({
  endpoints: (builder) => ({
    submitTenantInviteRequest: builder.mutation<
      UISubmitInviteRequestOutcome,
      UISubmitInviteRequestInput
    >({
      queryFn: wailsQueryFn<UISubmitInviteRequestInput, UISubmitInviteRequestOutcome>((input) =>
        SubmitTenantInviteRequest(input),
      ),
      invalidatesTags: (result, _error, input) =>
        result?.request ? [{ type: 'TenantDashboard', id: input.tenant }] : [],
    }),
    getMyTenantInviteRequest: builder.query<UIInviteRequest | null, UITenantInput>({
      queryFn: wailsQueryFn<UITenantInput, UIInviteRequest | null>((input) =>
        GetMyTenantInviteRequest(input),
      ),
      providesTags: (_result, _error, input) => [{ type: 'TenantDashboard', id: input.tenant }],
    }),
    approveTenantInviteRequest: builder.mutation<UIInviteRequest, UIDecideInviteRequestInput>({
      queryFn: wailsQueryFn<UIDecideInviteRequestInput, UIInviteRequest>((input) =>
        ApproveTenantInviteRequest(input),
      ),
      invalidatesTags: (_result, _error, input) => [{ type: 'TenantDashboard', id: input.tenant }],
    }),
    declineTenantInviteRequest: builder.mutation<UIInviteRequest, UIDeclineInviteRequestInput>({
      queryFn: wailsQueryFn<UIDeclineInviteRequestInput, UIInviteRequest>((input) =>
        DeclineTenantInviteRequest(input),
      ),
      invalidatesTags: (_result, _error, input) => [{ type: 'TenantDashboard', id: input.tenant }],
    }),
    listTenantPlatformEnrollmentStatuses: builder.query<
      UITenantPlatformEnrollmentStatus[],
      UIListTenantPlatformEnrollmentStatusesInput
    >({
      queryFn: wailsQueryFn<
        UIListTenantPlatformEnrollmentStatusesInput,
        UITenantPlatformEnrollmentStatus[]
      >((input) => ListTenantPlatformEnrollmentStatuses(input)),
      providesTags: ['TenantEnrollment'],
    }),
  }),
});

export const {
  useSubmitTenantInviteRequestMutation,
  useGetMyTenantInviteRequestQuery,
  useApproveTenantInviteRequestMutation,
  useDeclineTenantInviteRequestMutation,
  useListTenantPlatformEnrollmentStatusesQuery,
} = tenantInviteRequestApi;
