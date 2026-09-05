import type {
  UICommitWorkingTreeResult,
  UIExecCommitInput,
  UIExecPushInput,
  UIPushWorkingTreeBranchResult,
  UISelection,
} from '@/types';

import { ExecCommit, ExecPush } from '../../../wailsjs/go/main/App';
import { wailsApi } from './wailsApi';
import { wailsQueryFn } from './wailsBaseQuery';

interface ExecCommitArgs {
  selection: UISelection;
  input: UIExecCommitInput;
}

interface ExecPushArgs {
  selection: UISelection;
  input: UIExecPushInput;
}

// execApi reaches the runtime repo's commit/push primitives — the
// precondition CreateReview's sourceBranch needs — invalidating the diff
// panel's own cache so a commit or push made from the review dialog is
// reflected there without a manual refresh.
export const execApi = wailsApi.injectEndpoints({
  endpoints: (builder) => ({
    execCommit: builder.mutation<UICommitWorkingTreeResult, ExecCommitArgs>({
      queryFn: wailsQueryFn<ExecCommitArgs, UICommitWorkingTreeResult>(({ selection, input }) =>
        ExecCommit(selection, input),
      ),
      invalidatesTags: ['Diff'],
    }),
    execPush: builder.mutation<UIPushWorkingTreeBranchResult, ExecPushArgs>({
      queryFn: wailsQueryFn<ExecPushArgs, UIPushWorkingTreeBranchResult>(({ selection, input }) =>
        ExecPush(selection, input),
      ),
      invalidatesTags: ['Diff'],
    }),
  }),
});

export const { useExecCommitMutation, useExecPushMutation } = execApi;
