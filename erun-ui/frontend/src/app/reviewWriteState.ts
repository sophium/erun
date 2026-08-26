// State for the desktop's review write surface: the Merge Queue
// panel's "Advance queue" action and the "Open a review" dialog. Split out
// of state.ts to keep that file under eslint's 500-line max-lines cap, the
// same pattern diffTypes.ts/reviewTypes.ts use for types.ts.

// MergeQueueActionState backs the Merge Queue panel's "Advance queue"
// action: an inline confirm step, then busy/error like every other
// side-effecting dashboard write. blocked/blockedReviewId/unresolvedThreads
// describe a refused advance (the queue head still has open comment
// threads) as a distinct, named state rather than a bare error string — see
// mergeQueueThunks.ts. overriding/overrideReason/overrideBusy/overrideError
// back the reason-required override affordance offered from that state.
export interface MergeQueueActionState {
  confirming: boolean;
  busy: boolean;
  error: string;
  blocked: boolean;
  blockedReviewId: string;
  unresolvedThreads: number;
  overriding: boolean;
  overrideReason: string;
  overrideBusy: boolean;
  overrideError: string;
}

export const defaultMergeQueueAction = (): MergeQueueActionState => ({
  confirming: false,
  busy: false,
  error: '',
  blocked: false,
  blockedReviewId: '',
  unresolvedThreads: 0,
  overriding: false,
  overrideReason: '',
  overrideBusy: false,
  overrideError: '',
});

// CreateReviewDialogState backs the "Open a review" dialog: pushing the
// selected environment's branch (commit, then push) and, once pushed,
// creating the review itself. Each write has its own busy/error pair so the
// dialog can show which step is in flight or failed rather than one shared
// status the operator has to guess the meaning of.
export interface CreateReviewDialogState {
  open: boolean;
  tenant: string;
  environment: string;
  name: string;
  targetBranch: string;
  sourceBranch: string;
  branchLoading: boolean;
  branchError: string;
  commitMessage: string;
  committing: boolean;
  commitError: string;
  pushedBranch: string;
  pushing: boolean;
  pushError: string;
  creating: boolean;
  createError: string;
}

export const defaultCreateReviewDialog = (): CreateReviewDialogState => ({
  open: false,
  tenant: '',
  environment: '',
  name: '',
  targetBranch: 'main',
  sourceBranch: '',
  branchLoading: false,
  branchError: '',
  commitMessage: '',
  committing: false,
  commitError: '',
  pushedBranch: '',
  pushing: false,
  pushError: '',
  creating: false,
  createError: '',
});
