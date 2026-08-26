// MergeQueueBlockedAlert/MergeQueueOverrideForm, split out of
// TenantDashboardPanels.Reviews.tsx to keep that file under eslint's 500-line
// max-lines cap (see TenantDashboardPanels.Reviews.tsx's own header comment
// for the same pattern against TenantDashboardPanels.tsx).
import { Button, FieldLabel, Textarea } from 'erun-kit';
import { LoaderCircle } from 'lucide-react';
import * as React from 'react';

import { useAppDispatch, useAppSelector } from '@/app/hooks';
import {
  beginMergeQueueOverride,
  cancelMergeQueueOverride,
  clearMergeQueueOverrideError,
  submitMergeQueueOverride,
  updateMergeQueueOverrideReason,
} from '@/app/mergeQueueThunks';
import { resolveTenantPlatformAlias } from '@/app/platformSignIn';
import { openReviewDetail } from '@/app/reviewDetailThunks';
import type { MergeQueueActionState } from '@/app/reviewWriteState';
import type { UITenantDashboardReview } from '@/types';

import { InlineAlert } from './InlineAlert';
import { PlatformErrorAlert } from './PlatformSignInAlert';

// blockedReviewName prefers the queue row's own display name over the bare id
// (recognition over recall) — the row is normally already loaded since it is
// the head of the same queue the operator just tried to advance.
function blockedReviewName(mergeQueue: UITenantDashboardReview[], reviewId: string): string {
  return mergeQueue.find((review) => review.reviewId === reviewId)?.name ?? reviewId;
}

// MergeQueueBlockedAlert is AdvanceMergeQueue's unresolved-thread refusal
// rendered as a state with a way forward, not a dead end (AGENTS.md "Smooth,
// Seamless, No Dead Ends"): it names the review and the count, offers "View
// discussion" (openReviewDetail, the same navigation a review row's own click
// uses) as the primary route, and — only for a caller the platform actually
// grants the distinct override route to — an "Override anyway" affordance
// that demands a reason before it will act.
export function MergeQueueBlockedAlert({
  canOverride,
  mergeQueue,
  targetBranch,
  action,
}: {
  canOverride: boolean;
  mergeQueue: UITenantDashboardReview[];
  targetBranch: string;
  action: MergeQueueActionState;
}): React.ReactElement {
  const dispatch = useAppDispatch();
  const reviewName = blockedReviewName(mergeQueue, action.blockedReviewId);
  const threadWord = action.unresolvedThreads === 1 ? 'thread' : 'threads';
  return (
    <div className="grid max-w-sm gap-2">
      <InlineAlert>
        <span className="font-medium">{reviewName}</span> has {action.unresolvedThreads} unresolved
        comment {threadWord}. Resolve {action.unresolvedThreads === 1 ? 'it' : 'them'} before
        advancing the merge queue.
      </InlineAlert>
      <div className="flex flex-wrap justify-end gap-2">
        <Button
          type="button"
          variant="outline"
          size="sm"
          onClick={() => {
            void dispatch(openReviewDetail(action.blockedReviewId));
          }}
        >
          View discussion
        </Button>
        {canOverride && !action.overriding && (
          <Button
            type="button"
            variant="outline"
            size="sm"
            onClick={() => {
              dispatch(beginMergeQueueOverride());
            }}
          >
            Override anyway
          </Button>
        )}
      </div>
      {action.overriding && <MergeQueueOverrideForm targetBranch={targetBranch} action={action} />}
    </div>
  );
}

// MergeQueueOverrideForm is the reason-required, audited escape from the
// gate: the platform records the reason and the caller's identity, so the
// control asks for that reason explicitly rather than treating the override
// as a plain confirm click. See erun-backend-api AGENTS.md "Merge Queue".
function MergeQueueOverrideForm({
  targetBranch,
  action,
}: {
  targetBranch: string;
  action: MergeQueueActionState;
}): React.ReactElement {
  const dispatch = useAppDispatch();
  const cloudProviderAlias = useAppSelector((state) =>
    resolveTenantPlatformAlias(state.tenantDashboard.data),
  );
  return (
    <div className="grid gap-2 rounded-[var(--radius)] border border-border bg-muted/40 p-3">
      <FieldLabel htmlFor="merge-queue-override-reason" required>
        Reason for overriding
      </FieldLabel>
      <Textarea
        id="merge-queue-override-reason"
        rows={2}
        value={action.overrideReason}
        disabled={action.overrideBusy}
        onChange={(event) => {
          dispatch(updateMergeQueueOverrideReason(event.target.value));
        }}
        placeholder="Why is it right to merge with unresolved discussion?"
      />
      {action.overrideError && (
        <PlatformErrorAlert
          message={action.overrideError}
          alias={cloudProviderAlias}
          onRecovered={() => {
            dispatch(clearMergeQueueOverrideError());
          }}
        />
      )}
      <div className="flex justify-end gap-2">
        <Button
          type="button"
          variant="outline"
          size="sm"
          disabled={action.overrideBusy}
          onClick={() => {
            dispatch(cancelMergeQueueOverride());
          }}
        >
          Cancel
        </Button>
        <Button
          type="button"
          size="sm"
          disabled={action.overrideBusy || !action.overrideReason.trim()}
          onClick={() => {
            void dispatch(submitMergeQueueOverride(targetBranch));
          }}
        >
          {action.overrideBusy && <LoaderCircle className="animate-spin" aria-hidden="true" />}
          Confirm override
        </Button>
      </div>
    </div>
  );
}
