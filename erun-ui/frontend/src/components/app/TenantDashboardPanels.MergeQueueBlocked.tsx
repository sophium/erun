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

function blockedReviewName(mergeQueue: UITenantDashboardReview[], reviewId: string): string {
  return mergeQueue.find((review) => review.reviewId === reviewId)?.name ?? reviewId;
}

// MergeQueueBlockedAlert renders the unresolved-thread refusal with a route to
// the discussion and, only when the platform grants it, an override.
export function MergeQueueBlockedAlert({
  canOverride,
  mergeQueue,
  repository,
  targetBranch,
  action,
}: {
  canOverride: boolean;
  mergeQueue: UITenantDashboardReview[];
  repository: string;
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
      {action.overriding && (
        <MergeQueueOverrideForm
          repository={repository}
          targetBranch={targetBranch}
          action={action}
        />
      )}
    </div>
  );
}

// MergeQueueOverrideForm requires a reason because the platform audits overrides.
function MergeQueueOverrideForm({
  repository,
  targetBranch,
  action,
}: {
  repository: string;
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
            void dispatch(submitMergeQueueOverride(repository, targetBranch));
          }}
        >
          {action.overrideBusy && <LoaderCircle className="animate-spin" aria-hidden="true" />}
          Confirm override
        </Button>
      </div>
    </div>
  );
}
