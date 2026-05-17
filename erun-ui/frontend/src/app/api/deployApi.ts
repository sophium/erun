import type { UISelection } from '@/types';

import {
  CancelWaitingAction,
  DismissDeploy,
  FindActiveDeployForSelection,
  ForceDismissActivity,
  GetDeploy,
  KillSession,
  ListDeploys,
  RecoverPendingHelmRelease,
} from '../../../wailsjs/go/main/App';
import type { ActivityQueueEntry, ActivityRecoveryResult } from '../activityQueueState';
import { wailsApi } from './wailsApi';
import { wailsQueryFn } from './wailsBaseQuery';

export const deployApi = wailsApi.injectEndpoints({
  endpoints: (builder) => ({
    listDeploys: builder.query<ActivityQueueEntry[], void>({
      queryFn: wailsQueryFn<void, ActivityQueueEntry[]>(
        () => ListDeploys() as Promise<ActivityQueueEntry[]>,
      ),
      providesTags: ['Deploys'],
    }),
    getDeploy: builder.query<ActivityQueueEntry, string>({
      queryFn: wailsQueryFn<string, ActivityQueueEntry>(
        (id) => GetDeploy(id) as Promise<ActivityQueueEntry>,
      ),
      providesTags: (_result, _error, id) => [{ type: 'Deploys', id }],
    }),
    findActiveDeployForSelection: builder.query<ActivityQueueEntry, UISelection>({
      queryFn: wailsQueryFn<UISelection, ActivityQueueEntry>(
        (selection) => FindActiveDeployForSelection(selection) as Promise<ActivityQueueEntry>,
      ),
      providesTags: ['Deploys'],
    }),
    dismissDeploy: builder.mutation<boolean, string>({
      queryFn: wailsQueryFn<string, boolean>((id) => DismissDeploy(id)),
      invalidatesTags: ['Deploys'],
    }),
    forceDismissActivity: builder.mutation<boolean, string>({
      queryFn: wailsQueryFn<string, boolean>((id) => ForceDismissActivity(id)),
      invalidatesTags: ['Deploys'],
    }),
    recoverPendingHelmRelease: builder.mutation<ActivityRecoveryResult, string>({
      queryFn: wailsQueryFn<string, ActivityRecoveryResult>((id) => RecoverPendingHelmRelease(id)),
      invalidatesTags: ['Deploys'],
    }),
    killSessionMutation: builder.mutation<boolean, number>({
      queryFn: wailsQueryFn<number, boolean>((sessionId) => KillSession(sessionId)),
      invalidatesTags: ['Deploys'],
    }),
    cancelWaitingAction: builder.mutation<boolean, string>({
      queryFn: wailsQueryFn<string, boolean>((id) => CancelWaitingAction(id)),
      invalidatesTags: ['Deploys'],
    }),
  }),
});

export const {
  useListDeploysQuery,
  useGetDeployQuery,
  useFindActiveDeployForSelectionQuery,
  useDismissDeployMutation,
  useForceDismissActivityMutation,
  useRecoverPendingHelmReleaseMutation,
  useKillSessionMutationMutation,
  useCancelWaitingActionMutation,
} = deployApi;
