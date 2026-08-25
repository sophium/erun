// State for the desktop's review write surface (#1348): the Merge Queue
// panel's "Advance queue" action and the "Open a review" dialog. Split out
// of state.ts to keep that file under eslint's 500-line max-lines cap, the
// same pattern diffTypes.ts/reviewTypes.ts use for types.ts.

// MergeQueueActionState backs the Merge Queue panel's "Advance queue"
// action: an inline confirm step, then busy/error like every other
// side-effecting dashboard write.
export interface MergeQueueActionState {
  confirming: boolean;
  busy: boolean;
  error: string;
}

export const defaultMergeQueueAction = (): MergeQueueActionState => ({
  confirming: false,
  busy: false,
  error: '',
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
  apiUrl: string;
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
  apiUrl: '',
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
