// Reviews and merge-queue panels, split out of TenantDashboardPanels.tsx
// because that file crossed eslint's 500-line max-lines cap. Nothing here
// changes shape or behaviour — the two tabs still share PanelBody and
// TenantDashboardData from the main file.
import { Button, EmptyState, StatusBadge, TabsContent } from 'erun-kit';
import { LoaderCircle, Plus } from 'lucide-react';
import * as React from 'react';

import { openCreateReviewDialog } from '@/app/createReviewDialogThunks';
import { useAppDispatch, useAppSelector } from '@/app/hooks';
import {
  cancelAdvanceMergeQueue,
  confirmAdvanceMergeQueue,
  submitAdvanceMergeQueue,
} from '@/app/mergeQueueThunks';
import { openReviewDetail } from '@/app/reviewDetailThunks';
import {
  formatDashboardDate,
  reviewStatusTone,
  unresolvedThreadsLabel,
  unresolvedThreadsTone,
} from '@/app/tenantDashboardPanels';
import { setReviewFilter } from '@/app/tenantDialogThunks';
import type { UITenantDashboardReview } from '@/types';

import { InlineAlert } from './InlineAlert';
import { DataCell, DataTable, PanelBody, type TenantDashboardData } from './TenantDashboardMessage';

// ReviewsPanel is the review object's own home: status, branches, and — via
// each row — its builds, comment threads, and merge-queue position. The
// merge-queue tab stays a queue-shaped view of the same reviews.
export function ReviewsPanel({ data }: { data: TenantDashboardData }): React.ReactElement {
  const dispatch = useAppDispatch();
  const reviewFilter = useAppSelector((state) => state.tenantDashboard.reviewFilter);
  const reviews = data?.reviews ?? [];
  const filterActive = reviewFilter.mine || reviewFilter.waitingOnMe;
  return (
    <TabsContent value="reviews" className="min-h-0 overflow-auto">
      <div className="mb-3 flex flex-wrap items-center justify-between gap-3">
        <div className="flex flex-wrap items-center gap-2">
          <span className="text-[13px] text-muted-foreground">
            {reviews.length} review{reviews.length === 1 ? '' : 's'}
          </span>
          <ReviewFilterToggle
            label="Mine"
            active={reviewFilter.mine}
            onClick={() => {
              void dispatch(setReviewFilter({ mine: !reviewFilter.mine }));
            }}
          />
          <ReviewFilterToggle
            label="Waiting on me"
            active={reviewFilter.waitingOnMe}
            onClick={() => {
              void dispatch(setReviewFilter({ waitingOnMe: !reviewFilter.waitingOnMe }));
            }}
          />
        </div>
        <NewReviewAction data={data} />
      </div>
      <PanelBody
        data={data}
        tab="reviews"
        empty={<ReviewsEmptyState filterActive={filterActive} />}
      >
        {reviews.length > 0 ? (
          <ReviewsTable
            reviews={reviews}
            currentUserId={data?.user?.userId}
            showThreads
            onSelect={(review) => {
              void dispatch(openReviewDetail(review.reviewId));
            }}
          />
        ) : null}
      </PanelBody>
    </TabsContent>
  );
}

// ReviewFilterToggle is a one-click discovery affordance, not a form field:
// clicking answers "which are mine" or "which are waiting on me" directly,
// matching the DiffSourceButton toggle-button pattern already used for the
// review panel's Env/ERun source switch.
function ReviewFilterToggle({
  label,
  active,
  onClick,
}: {
  label: string;
  active: boolean;
  onClick: () => void;
}): React.ReactElement {
  return (
    <Button
      type="button"
      size="sm"
      variant={active ? 'default' : 'outline'}
      aria-pressed={active}
      onClick={onClick}
    >
      {label}
    </Button>
  );
}

// ReviewsEmptyState keeps "nothing exists yet" and "nothing matches this
// filter" visually and textually distinct, per the repo's three-empty-states
// rule — a filtered zero must not read as "this tenant has no reviews".
function ReviewsEmptyState({ filterActive }: { filterActive: boolean }): React.ReactElement {
  const dispatch = useAppDispatch();
  if (filterActive) {
    return (
      <EmptyState
        heading="No reviews match this filter"
        body="Nothing is both Mine and Waiting on me right now, whichever you've turned on."
        action={
          <Button
            type="button"
            variant="outline"
            size="sm"
            onClick={() => {
              void dispatch(setReviewFilter({ mine: false, waitingOnMe: false }));
            }}
          >
            Clear filter
          </Button>
        }
      />
    );
  }
  return (
    <EmptyState
      heading="No reviews yet"
      body="A review appears here once someone opens one from the CLI's erun review create or the New review button above."
    />
  );
}

// NewReviewAction degrades by permission: the button renders only when the
// caller may create a review, and a caller who cannot is told so rather than
// left to discover it from a failed submit.
function NewReviewAction({ data }: { data: TenantDashboardData }): React.ReactElement | null {
  const dispatch = useAppDispatch();
  if (!data) {
    return null;
  }
  if (!data.canCreateReview) {
    return (
      <span className="text-[13px] text-muted-foreground">
        You do not have access to open reviews.
      </span>
    );
  }
  return (
    <Button
      type="button"
      size="sm"
      onClick={() => {
        void dispatch(openCreateReviewDialog());
      }}
    >
      <Plus aria-hidden="true" />
      New review
    </Button>
  );
}

export function MergeQueuePanel({ data }: { data: TenantDashboardData }): React.ReactElement {
  const mergeQueue = data?.mergeQueue ?? [];
  return (
    <TabsContent value="queue" className="min-h-0 overflow-auto">
      <div className="mb-3 flex items-center justify-between gap-3">
        <span className="text-[13px] text-muted-foreground">{mergeQueue.length} queued</span>
        <AdvanceMergeQueueAction data={data} mergeQueue={mergeQueue} />
      </div>
      <PanelBody
        data={data}
        tab="queue"
        empty={
          <EmptyState
            heading="No reviews are waiting in the merge queue"
            body="A review joins the queue once a build has passed for it."
          />
        }
      >
        {mergeQueue.length > 0 ? <ReviewsTable reviews={mergeQueue} /> : null}
      </PanelBody>
    </TabsContent>
  );
}

// mergeQueueTargetBranch reports the target branch to advance only when the
// whole visible queue shares one — advancing is a single-queue-head write, so
// a mixed-branch queue has no single unambiguous head to name from here.
function mergeQueueTargetBranch(mergeQueue: UITenantDashboardReview[]): string {
  const branches = [...new Set(mergeQueue.map((review) => review.targetBranch.trim()))].filter(
    Boolean,
  );
  return branches.length === 1 ? (branches[0] ?? '') : '';
}

function AdvanceMergeQueueAction({
  data,
  mergeQueue,
}: {
  data: TenantDashboardData;
  mergeQueue: UITenantDashboardReview[];
}): React.ReactElement | null {
  const dispatch = useAppDispatch();
  const action = useAppSelector((state) => state.mergeQueueAction);
  if (!data) {
    return null;
  }
  if (!data.canAdvanceMergeQueue) {
    return (
      <span className="text-[13px] text-muted-foreground">
        You do not have access to advance the merge queue.
      </span>
    );
  }
  if (mergeQueue.length === 0) {
    return null;
  }
  const targetBranch = mergeQueueTargetBranch(mergeQueue);
  if (!targetBranch) {
    return (
      <span className="max-w-xs text-right text-[13px] text-muted-foreground">
        These reviews target more than one branch, so there is no single queue head to advance.
      </span>
    );
  }
  return (
    <div className="flex min-w-0 flex-col items-end gap-2">
      {action.confirming ? (
        <AdvanceMergeQueueConfirm targetBranch={targetBranch} busy={action.busy} />
      ) : (
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
      )}
      {action.error && (
        <div className="max-w-sm">
          <InlineAlert>{action.error}</InlineAlert>
        </div>
      )}
    </div>
  );
}

// The confirm step is its own row so the question reads left-to-right into the
// two answers, and a failure lands under it rather than shouldering its way
// into the middle of the sentence.
function AdvanceMergeQueueConfirm({
  targetBranch,
  busy,
}: {
  targetBranch: string;
  busy: boolean;
}): React.ReactElement {
  const dispatch = useAppDispatch();
  return (
    <div className="flex flex-wrap items-center justify-end gap-2">
      <span className="text-[13px] text-foreground">
        Merge the queue head into <span className="font-mono">{targetBranch}</span>?
      </span>
      <Button
        type="button"
        variant="outline"
        size="sm"
        disabled={busy}
        onClick={() => {
          dispatch(cancelAdvanceMergeQueue());
        }}
      >
        Cancel
      </Button>
      <Button
        type="button"
        size="sm"
        disabled={busy}
        onClick={() => {
          void dispatch(submitAdvanceMergeQueue(targetBranch));
        }}
      >
        {busy && <LoaderCircle className="animate-spin" aria-hidden="true" />}
        Confirm
      </Button>
    </div>
  );
}

// displayReviewAuthor renders "You" for the signed-in user's own reviews and
// the raw author id otherwise — the same treatment the Audit panel's Actor
// column already gives an external user id, since the desktop has no user
// directory to resolve a name from (Nielsen #4, consistency with a
// comparable flow already in this dashboard).
function displayReviewAuthor(
  authorUserId: string | undefined,
  currentUserId: string | undefined,
): string {
  const author = authorUserId?.trim() ?? '';
  if (!author) {
    return '-';
  }
  return currentUserId && author === currentUserId ? 'You' : author;
}

function ReviewsTable({
  reviews,
  currentUserId,
  showThreads = false,
  onSelect,
}: {
  reviews: UITenantDashboardReview[];
  currentUserId?: string;
  showThreads?: boolean;
  onSelect?: (review: UITenantDashboardReview) => void;
}): React.ReactElement {
  const headers = ['Review', 'Status', 'Author', 'Target', 'Source', 'Updated'];
  if (showThreads) {
    headers.push('Threads');
  }
  return (
    <DataTable headers={headers}>
      {reviews.map((review) => (
        <tr key={review.reviewId}>
          <DataCell strong>
            {onSelect ? (
              <button
                type="button"
                className="text-left text-foreground underline-offset-2 hover:underline focus-visible:underline"
                onClick={() => {
                  onSelect(review);
                }}
                aria-label={`Open review ${review.name || review.reviewId}`}
              >
                {review.name || review.reviewId}
              </button>
            ) : (
              review.name || review.reviewId
            )}
          </DataCell>
          <DataCell>
            <StatusBadge tone={reviewStatusTone(review.status)} label={review.status} />
          </DataCell>
          <DataCell>{displayReviewAuthor(review.authorUserId, currentUserId)}</DataCell>
          <DataCell>{review.targetBranch}</DataCell>
          <DataCell>{review.sourceBranch}</DataCell>
          <DataCell>{formatDashboardDate(review.updatedAt)}</DataCell>
          {showThreads && (
            <DataCell>
              {review.unresolvedThreads === undefined ? (
                '-'
              ) : (
                <StatusBadge
                  tone={unresolvedThreadsTone(review.unresolvedThreads)}
                  label={unresolvedThreadsLabel(review.unresolvedThreads)}
                />
              )}
            </DataCell>
          )}
        </tr>
      ))}
    </DataTable>
  );
}
