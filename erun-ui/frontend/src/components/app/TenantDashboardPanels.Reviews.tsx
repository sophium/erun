// Reviews and merge-queue panels, split out of TenantDashboardPanels.tsx
// because that file crossed eslint's 500-line max-lines cap. The two tabs
// still share PanelBody and TenantDashboardData from the main file.
import { Button, cn, EmptyState, StatusBadge, TabsContent } from 'erun-kit';
import { LoaderCircle, Plus } from 'lucide-react';
import * as React from 'react';

import { openCreateReviewDialog } from '@/app/createReviewDialogThunks';
import { useAppDispatch, useAppSelector } from '@/app/hooks';
import {
  cancelAdvanceMergeQueue,
  clearAdvanceMergeQueueError,
  confirmAdvanceMergeQueue,
  submitAdvanceMergeQueue,
} from '@/app/mergeQueueThunks';
import { resolveTenantPlatformAlias } from '@/app/platformSignIn';
import { openReviewDetail } from '@/app/reviewDetailThunks';
import {
  reviewAuthorInitials,
  reviewStatusTone,
  unresolvedThreadsLabel,
  unresolvedThreadsTone,
} from '@/app/tenantDashboardPanels';
import { setReviewFilter } from '@/app/tenantDialogThunks';
import type { UITenantDashboardReview } from '@/types';

import { PermissionNotice } from './InlineAlert';
import { PlatformErrorAlert } from './PlatformSignInAlert';
import {
  BranchArrow,
  DataCell,
  DataTable,
  PanelBody,
  RelativeTime,
  type TenantDashboardData,
} from './TenantDashboardMessage';
import { MergeQueueBlockedAlert } from './TenantDashboardPanels.MergeQueueBlocked';

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
        <div className="flex flex-wrap items-center gap-3">
          <span className="text-[13px] text-muted-foreground">
            {reviews.length} review{reviews.length === 1 ? '' : 's'}
          </span>
          <ReviewFilterSegmentedControl
            mine={reviewFilter.mine}
            waitingOnMe={reviewFilter.waitingOnMe}
            mineCount={data?.mineReviewCount}
            waitingOnMeCount={data?.waitingOnMeReviewCount}
            onToggleMine={() => {
              void dispatch(setReviewFilter({ mine: !reviewFilter.mine }));
            }}
            onToggleWaitingOnMe={() => {
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

// ReviewFilterSegmentedControl is one grouped control, not two independent
// buttons (#1378): Mine and Waiting-on-me visually merge into a single pill,
// matching the DiffSourceButton segmented-toggle pattern the review panel's
// Env/ERun source switch already uses (Nielsen #4, consistency). Each side
// still toggles independently — a review can be both — so this is a grouped
// filter chip pair, not a mutually-exclusive tab strip. The count on each
// side is the discovery signal itself: which pile has work in it is visible
// before either is clicked, rather than only after.
function ReviewFilterSegmentedControl({
  mine,
  waitingOnMe,
  mineCount,
  waitingOnMeCount,
  onToggleMine,
  onToggleWaitingOnMe,
}: {
  mine: boolean;
  waitingOnMe: boolean;
  mineCount: number | undefined;
  waitingOnMeCount: number | undefined;
  onToggleMine: () => void;
  onToggleWaitingOnMe: () => void;
}): React.ReactElement {
  return (
    <div className="flex items-center gap-1 rounded-[var(--radius)] border border-input bg-background p-1 text-[13px]">
      <ReviewFilterToggle label="Mine" count={mineCount} active={mine} onClick={onToggleMine} />
      <ReviewFilterToggle
        label="Waiting on me"
        count={waitingOnMeCount}
        active={waitingOnMe}
        onClick={onToggleWaitingOnMe}
      />
    </div>
  );
}

// ReviewFilterToggle is a one-click discovery affordance, not a form field:
// clicking answers "which are mine" or "which are waiting on me" directly.
// The count renders inside the button's own accessible name (e.g. "Mine 2")
// so a screen reader announces the same distribution a sighted operator sees.
function ReviewFilterToggle({
  label,
  count,
  active,
  onClick,
}: {
  label: string;
  count: number | undefined;
  active: boolean;
  onClick: () => void;
}): React.ReactElement {
  return (
    <button
      type="button"
      aria-pressed={active}
      onClick={onClick}
      className={cn(
        'flex cursor-pointer items-center gap-1.5 rounded-[calc(var(--radius)-2px)] px-2.5 py-1 transition-colors focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-1 focus-visible:outline-none',
        active
          ? 'bg-primary text-primary-foreground'
          : 'text-muted-foreground hover:bg-accent hover:text-accent-foreground',
      )}
    >
      {label}
      {count !== undefined && (
        <span
          className={cn(
            'inline-flex min-w-[1.25rem] items-center justify-center rounded-full px-1 text-[11px] leading-4 font-semibold',
            active ? 'bg-primary-foreground/20' : 'bg-muted text-foreground',
          )}
        >
          {count}
        </span>
      )}
    </button>
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
      <div className="max-w-sm">
        <PermissionNotice>You do not have access to create reviews.</PermissionNotice>
      </div>
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
  const cloudProviderAlias = useAppSelector((state) =>
    resolveTenantPlatformAlias(state.tenantDashboard.data),
  );
  if (!data) {
    return null;
  }
  if (!data.canAdvanceMergeQueue) {
    return (
      <div className="max-w-sm">
        <PermissionNotice>You do not have access to advance the merge queue.</PermissionNotice>
      </div>
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
  if (action.blocked) {
    return (
      <MergeQueueBlockedAlert
        canOverride={data.canOverrideMergeQueue}
        mergeQueue={mergeQueue}
        targetBranch={targetBranch}
        action={action}
      />
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
          <PlatformErrorAlert
            message={action.error}
            alias={cloudProviderAlias}
            onRecovered={() => {
              dispatch(clearAdvanceMergeQueueError());
            }}
          />
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

// displayReviewAuthor renders "You" for the signed-in user's own reviews,
// then the resolved tenant-user-directory username, then the raw author id
// as a last resort (#1378) — never a bare implementation-facing id when a
// human name is available.
function displayReviewAuthor(
  review: UITenantDashboardReview,
  currentUserId: string | undefined,
): string {
  const author = review.authorUserId?.trim() ?? '';
  if (!author) {
    return '-';
  }
  if (currentUserId && author === currentUserId) {
    return 'You';
  }
  return review.authorUsername?.trim() ?? author;
}

// ReviewAuthorAvatar is the row's fastest scan key: an initials avatar for
// the author display name already computed for the row, styled filled for
// the signed-in user's own reviews so "mine" is recognizable at a glance
// even before reading the text next to it.
function ReviewAuthorAvatar({ name }: { name: string }): React.ReactElement {
  const isSelf = name === 'You';
  return (
    <span
      aria-hidden="true"
      className={cn(
        'flex size-7 flex-none items-center justify-center rounded-full text-[11px] font-semibold',
        isSelf ? 'bg-primary text-primary-foreground' : 'bg-primary/10 text-primary',
      )}
    >
      {reviewAuthorInitials(name)}
    </span>
  );
}

// ReviewsTable renders one row per review. The Review column is deliberately
// the only one DataTable's shared truncate/DataCell treatment doesn't own: it
// carries the avatar plus a two-line title/metadata stack so the title can
// dominate the row (larger, heavier, full-strength foreground) while author,
// branches, updated time, and thread count read as a subordinate second line
// — a dense grid of eight equal columns gave nothing for the eye to land on
// (#1378). Status and Threads stay their own narrow columns so their badges
// keep a fixed, predictable width.
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
  const headers = ['Review', 'Status'];
  const columnWidths = ['', 'w-[110px]'];
  if (showThreads) {
    headers.push('Threads');
    columnWidths.push('w-[130px]');
  }
  return (
    <DataTable headers={headers} columnWidths={columnWidths} minWidthClassName="min-w-[760px]">
      {reviews.map((review) => {
        const title = review.name || review.reviewId;
        const author = displayReviewAuthor(review, currentUserId);
        return (
          <tr key={review.reviewId}>
            <td className="px-2 py-2.5">
              <div className="flex min-w-0 items-center gap-2.5">
                <ReviewAuthorAvatar name={author} />
                <div className="flex min-w-0 flex-col gap-0.5">
                  {onSelect ? (
                    <button
                      type="button"
                      title={title}
                      className="min-w-0 truncate text-left text-[14px] font-semibold text-foreground underline-offset-2 hover:underline focus-visible:underline"
                      onClick={() => {
                        onSelect(review);
                      }}
                      aria-label={`Open review ${title}`}
                    >
                      {title}
                    </button>
                  ) : (
                    <span
                      title={title}
                      className="min-w-0 truncate text-[14px] font-semibold text-foreground"
                    >
                      {title}
                    </span>
                  )}
                  <div className="flex min-w-0 items-center gap-1.5 text-xs text-muted-foreground">
                    <span className="flex-none">{author}</span>
                    <span aria-hidden="true" className="flex-none">
                      ·
                    </span>
                    <BranchArrow
                      source={review.sourceBranch}
                      target={review.targetBranch}
                      className="min-w-0 flex-1"
                    />
                    <span aria-hidden="true" className="flex-none">
                      ·
                    </span>
                    <RelativeTime value={review.updatedAt} className="flex-none" />
                  </div>
                </div>
              </div>
            </td>
            <DataCell>
              <StatusBadge tone={reviewStatusTone(review.status)} label={review.status} />
            </DataCell>
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
        );
      })}
    </DataTable>
  );
}
