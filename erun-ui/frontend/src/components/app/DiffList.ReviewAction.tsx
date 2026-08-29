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

// DiffReviewStatusChip is the diff panel's honest read of where this
// environment section's branch pair sits on the review ladder -- it starts
// "Checking status…" while DiffReviewStatus resolves, then "No review" only
// once that read confirms one doesn't exist, never defaulting an unresolved
// answer to a value that looks authoritative. Clickable straight into the
// review's own detail whenever one exists, the same navigation a Reviews-tab
// row's own click uses (openReviewDetail), so the chip is also a way in, not
// only a status label.
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

// DiffReviewAction is the diff panel's single action per environment section,
// StartReviewFromDiffAction's own successor: the label and the operation it
// runs now follow the chip's status instead of a fixed "Start a review"
// button that discovered a live review only from the 409 submitCreateReview's
// catch reports, after the operator had already committed and pushed. Every
// branch reuses an existing write path (openCreateReviewDialog,
// submitAdvanceMergeQueue, openReviewDetail) rather than a new one.
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
      // 'checking' / 'unavailable' / 'none': the honest answer isn't in yet,
      // or is confirmed empty -- either way, the dialog itself resolves
      // whether this caller may create a review at all (Restricted state),
      // so this stays the one affordance that always renders.
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

// DiffAdvanceQueueBlocked is AdvanceMergeQueue's own unresolved-thread
// refusal (a race between this chip's own read and the click), rendered the
// same way MergeQueueBlockedAlert renders it for the Reviews tab: a state
// with a way forward, not a fault -- role="status", not role="alert".
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

// DiffAdvanceQueueAction reuses the Reviews tab's own confirm-then-submit
// merge-queue write (mergeQueueThunks.ts, state.mergeQueueAction) rather than
// a parallel one -- degrading by permission the same way that tab's
// AdvanceMergeQueueAction does, instead of rendering a control that fails
// with a 403 after the click.
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
            // Refreshes the chip once the write settles regardless of
            // outcome (advanced, blocked, or failed) -- the chip must never
            // keep showing "Ready" once the platform's own answer changed.
            void dispatch(submitAdvanceMergeQueue(tenant, targetBranch)).then(() => {
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

// StartReviewFromDiffAction is the diff panel's own entry point into the
// "Open a review" dialog: the panel already knows the environment and the
// branch it is diffing against, so opening the dialog from here carries both
// instead of sending the operator to the Reviews tab to re-specify what they
// were already looking at. It composes the existing dialog and its thunks
// (openCreateReviewDialog) rather than duplicating them -- the dialog itself
// resolves whether this caller may create a review at all (Restricted state).
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
        // Queried by the review surface's `S` keyboard shortcut
        // (TerminalController.startReviewForFocusedDiffEnv) to activate this
        // exact section's action without duplicating its dialog-opening logic.
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
