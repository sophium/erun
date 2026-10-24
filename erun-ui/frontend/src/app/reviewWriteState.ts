import type { UIAccessRemedy } from '@/types';

// Review write surface state, split out of state.ts for the max-lines cap.

// MergeQueueActionState backs the Merge Queue "Advance queue" action, including
// a refused advance (blocked) and its reason-required override.
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

// CreateReviewDialogState backs the "Open a review" dialog's push and create steps.
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
  // A failed capability probe (capabilityError) never blocks Create; only a known restriction does.
  capabilityLoading: boolean;
  capabilityRestricted: string;
  capabilityRemedy?: UIAccessRemedy;
  capabilityError: string;
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
  capabilityLoading: false,
  capabilityRestricted: '',
  capabilityError: '',
});
