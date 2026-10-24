import { Button, IconTooltip, StatusBadge } from 'erun-kit';
import { GitPullRequestCreate, LoaderCircle } from 'lucide-react';
import * as React from 'react';

import { openCreateReviewDialog } from '@/app/createReviewDialogThunks';
import {
  diffReviewChipLabel,
  diffReviewChipTone,
  diffReviewThreadCount,
} from '@/app/diffReviewStatusPresentation';
import { loadDiffReviewStatus } from '@/app/diffReviewStatusThunks';
import { useAppDispatch, useAppSelector } from '@/app/hooks';
import {
  cancelAdvanceMergeQueue,
  clearAdvanceMergeQueueError,
  confirmAdvanceMergeQueue,
  submitAdvanceMergeQueue,
} from '@/app/mergeQueueThunks';
import { openReviewDetail } from '@/app/reviewDetailThunks';
import type { MergeQueueActionState } from '@/app/reviewWriteState';
import { useDiffReviewStatusSlot } from '@/app/useDiffReviewStatusSlot';
import type { UIDiffReviewStatus } from '@/types';

import { PermissionNotice } from './InlineAlert';
import { PlatformErrorAlert } from './PlatformSignInAlert';

// DiffReviewStatusChip shows "Checking status…" until the read resolves, never
// a default that looks authoritative.
export function DiffReviewStatusChip({
  envKey,
  tenant,
}: {
  envKey: string;
  tenant: string;
}): React.ReactElement {
  const dispatch = useAppDispatch();
  const { status } = useDiffReviewStatusSlot(envKey);
  const badge = (
    <StatusBadge tone={diffReviewChipTone(status.state)} label={diffReviewChipLabel(status)} />
  );
  if (!status.reviewId) {
    return badge;
  }
  const reviewId = status.reviewId;
  return (
    <button
      type="button"
      aria-label={`Open review ${status.name ?? reviewId}`}
      className="cursor-pointer rounded-[calc(var(--radius)-2px)] focus-visible:ring-2 focus-visible:ring-ring focus-visible:outline-none"
      onClick={() => {
        void dispatch(openReviewDetail(reviewId, tenant));
      }}
    >
      {badge}
    </button>
  );
}

// DiffReviewAction follows the chip's status, so a live review is found before
// the operator pushes rather than from a 409.
export function DiffReviewAction({
  tenant,
  environment,
  targetBranch,
  envKey,
}: {
  tenant: string;
  environment: string;
  targetBranch: string;
  envKey: string;
}): React.ReactElement {
  const { status } = useDiffReviewStatusSlot(envKey);
  switch (status.state) {
    case 'ready':
      return (
        <DiffAdvanceQueueAction
          tenant={tenant}
          environment={environment}
          envKey={envKey}
          targetBranch={targetBranch}
          status={status}
        />
      );
    case 'blocked':
      return <DiffResolveThreadsAction tenant={tenant} status={status} />;
    case 'open':
    case 'merging':
    case 'merged':
    case 'closed':
    case 'failed':
      return <DiffViewReviewAction tenant={tenant} status={status} />;
    default:
      // The dialog itself resolves whether this caller may create a review.
      return (
        <StartReviewFromDiffAction
          tenant={tenant}
          environment={environment}
          targetBranch={targetBranch}
        />
      );
  }
}

function DiffViewReviewAction({
  tenant,
  status,
}: {
  tenant: string;
  status: UIDiffReviewStatus;
}): React.ReactElement {
  const dispatch = useAppDispatch();
  const reviewId = status.reviewId ?? '';
  return (
    <Button
      type="button"
      variant="outline"
      size="sm"
      onClick={() => {
        void dispatch(openReviewDetail(reviewId, tenant));
      }}
    >
      View review
    </Button>
  );
}

function DiffResolveThreadsAction({
  tenant,
  status,
}: {
  tenant: string;
  status: UIDiffReviewStatus;
}): React.ReactElement {
  const dispatch = useAppDispatch();
  const reviewId = status.reviewId ?? '';
  return (
    <IconTooltip label="Merge is blocked until every comment thread is resolved">
      <Button
        type="button"
        variant="outline"
        size="sm"
        onClick={() => {
          void dispatch(openReviewDetail(reviewId, tenant));
        }}
      >
        Resolve {diffReviewThreadCount(status)}
      </Button>
    </IconTooltip>
  );
}

// A refusal with a way forward, so role="status", not "alert".
function DiffAdvanceQueueBlocked({
  tenant,
  status,
  action,
}: {
  tenant: string;
  status: UIDiffReviewStatus;
  action: MergeQueueActionState;
}): React.ReactElement {
  const dispatch = useAppDispatch();
  const threadWord = action.unresolvedThreads === 1 ? 'thread' : 'threads';
  return (
    <div
      role="status"
      className="flex max-w-sm flex-col items-end gap-2 rounded-[var(--radius)] border border-border bg-muted/40 px-[11px] py-[9px] text-[13px] leading-[1.35] text-muted-foreground"
    >
      <span>
        <span className="font-medium text-foreground">{status.name ?? action.blockedReviewId}</span>{' '}
        has {action.unresolvedThreads} unresolved comment {threadWord}. Resolve{' '}
        {action.unresolvedThreads === 1 ? 'it' : 'them'} before advancing the merge queue.
      </span>
      <Button
        type="button"
        variant="outline"
        size="sm"
        onClick={() => {
          void dispatch(openReviewDetail(action.blockedReviewId, tenant));
        }}
      >
        View discussion
      </Button>
    </div>
  );
}

// DiffAdvanceQueueAction degrades by permission rather than failing with a 403.
function DiffAdvanceQueueAction({
  tenant,
  environment,
  envKey,
  targetBranch,
  status,
}: {
  tenant: string;
  environment: string;
  envKey: string;
  targetBranch: string;
  status: UIDiffReviewStatus;
}): React.ReactElement {
  const dispatch = useAppDispatch();
  const action = useAppSelector((state) => state.mergeQueueAction);
  if (!status.canAdvanceMergeQueue) {
    return (
      <div className="max-w-xs">
        <PermissionNotice>You do not have access to advance the merge queue.</PermissionNotice>
      </div>
    );
  }
  if (action.blocked) {
    return <DiffAdvanceQueueBlocked tenant={tenant} status={status} action={action} />;
  }
  if (action.confirming) {
    return (
      <div className="flex flex-wrap items-center justify-end gap-2">
        <Button
          type="button"
          variant="outline"
          size="sm"
          disabled={action.busy}
          onClick={() => {
            dispatch(cancelAdvanceMergeQueue());
          }}
        >
          Cancel
        </Button>
        <Button
          type="button"
          size="sm"
          disabled={action.busy}
          onClick={() => {
            // Refresh the chip whatever the outcome.
            void dispatch(
              submitAdvanceMergeQueue(tenant, status.repository ?? '', targetBranch),
            ).then(() => {
              void dispatch(loadDiffReviewStatus(envKey, tenant, environment, targetBranch));
            });
          }}
        >
          {action.busy && <LoaderCircle className="animate-spin" aria-hidden="true" />}
          Confirm
        </Button>
      </div>
    );
  }
  return (
    <div className="flex flex-col items-end gap-1.5">
      <Button
        type="button"
        variant="outline"
        size="sm"
        onClick={() => {
          dispatch(confirmAdvanceMergeQueue());
        }}
      >
        Advance queue
      </Button>
      {action.error && (
        <div className="max-w-sm">
          <PlatformErrorAlert
            message={action.error}
            alias=""
            onRecovered={() => {
              dispatch(clearAdvanceMergeQueueError());
            }}
          />
        </div>
      )}
    </div>
  );
}

export function StartReviewFromDiffAction({
  tenant,
  environment,
  targetBranch,
}: {
  tenant: string;
  environment: string;
  targetBranch: string;
}): React.ReactElement {
  const dispatch = useAppDispatch();
  return (
    <IconTooltip label={`Start a review from ${tenant} / ${environment}`}>
      <Button
        type="button"
        variant="outline"
        size="sm"
        // Queried by the `S` shortcut (reviewDiffKeyboardNav).
        data-review-action="start-review"
        onClick={() => {
          void dispatch(openCreateReviewDialog({ tenant, environment, targetBranch }));
        }}
      >
        <GitPullRequestCreate aria-hidden="true" />
        Start a review
      </Button>
    </IconTooltip>
  );
}
