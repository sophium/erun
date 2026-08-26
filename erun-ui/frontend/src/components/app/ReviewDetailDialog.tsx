import {
  Button,
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
  EmptyState,
  StatusBadge,
} from 'erun-kit';
import { LoaderCircle } from 'lucide-react';
import * as React from 'react';

import { useAppDispatch, useAppSelector } from '@/app/hooks';
import {
  cancelCloseReview,
  closeReviewDetail,
  confirmCloseReview,
  submitCloseReview,
} from '@/app/reviewDetailThunks';
import type { ReviewDetailState } from '@/app/state';
import {
  formatDashboardDate,
  reviewStatusTone,
  unresolvedThreadsLabel,
  unresolvedThreadsTone,
} from '@/app/tenantDashboardPanels';
import type { UITenantDashboardBuild, UITenantDashboardReview } from '@/types';

import { InlineAlert } from './InlineAlert';
import { ReviewDetailComments } from './ReviewDetailDialog.Comments';

// ReviewDetailDialog is the review object's own detail surface, opened from a
// row in the tenant dashboard's Reviews tab. Named "review detail" — not
// "review panel" — because that name already belongs to the local diff panel
// (ReviewPanel.tsx); this dialog shows the hosted platform review.
export function ReviewDetailDialog(): React.ReactElement {
  const dispatch = useAppDispatch();
  const detail = useAppSelector((state) => state.reviewDetail);
  return (
    <Dialog
      open={detail.open}
      onOpenChange={(next) => {
        if (!next) {
          dispatch(closeReviewDetail());
        }
      }}
    >
      <DialogContent className="flex max-h-[85vh] flex-col overflow-hidden sm:max-w-lg">
        <ReviewDetailBody detail={detail} />
      </DialogContent>
    </Dialog>
  );
}

function ReviewDetailBody({ detail }: { detail: ReviewDetailState }): React.ReactElement {
  if (detail.loading) {
    return (
      <div className="flex items-center gap-2 py-8 text-sm text-muted-foreground">
        <LoaderCircle className="size-4 animate-spin" aria-hidden="true" />
        Loading review…
      </div>
    );
  }
  if (detail.error) {
    return (
      <div className="py-4">
        <InlineAlert>{detail.error}</InlineAlert>
      </div>
    );
  }
  const data = detail.data;
  if (!data) {
    return <EmptyState heading="Review not found" />;
  }
  if (data.apiError) {
    return (
      <div className="py-4">
        <InlineAlert>{data.apiError}</InlineAlert>
      </div>
    );
  }
  if (data.restricted) {
    return (
      <EmptyState
        heading="You do not have access to this review"
        body={`It needs ${data.restricted}. Ask an administrator for access.`}
      />
    );
  }
  if (data.error || !data.review) {
    return (
      <div className="py-4">
        <InlineAlert>{data.error ?? 'This review could not be loaded.'}</InlineAlert>
      </div>
    );
  }
  return <ReviewDetailLoaded review={data.review} data={data} detail={detail} />;
}

function ReviewDetailLoaded({
  review,
  data,
  detail,
}: {
  review: NonNullable<NonNullable<ReviewDetailState['data']>['review']>;
  data: NonNullable<ReviewDetailState['data']>;
  detail: ReviewDetailState;
}): React.ReactElement {
  return (
    <>
      <DialogHeader>
        <DialogTitle className="flex items-center gap-2">
          {review.name || review.reviewId}
          <StatusBadge tone={reviewStatusTone(review.status)} label={review.status} />
        </DialogTitle>
        <DialogDescription>
          {review.sourceBranch} → {review.targetBranch}
          {data.queuePosition ? ` · queue position ${String(data.queuePosition)}` : ''}
        </DialogDescription>
      </DialogHeader>
      <ReviewDetailThreadStatus data={data} />
      <CloseReviewAction review={review} data={data} detail={detail} />
      <div className="flex min-h-0 flex-1 flex-col gap-4 overflow-y-auto pr-1">
        <ReviewDetailBuilds data={data} />
        <ReviewDetailComments detail={detail} />
      </div>
    </>
  );
}

// ReviewDetailThreadStatus is the "is this review actually finished" signal
// at the top of the dialog, visible without scrolling into the comments
// list. Rendered only once at least one thread exists — a review with no
// comments yet has nothing to call "all resolved".
function ReviewDetailThreadStatus({
  data,
}: {
  data: NonNullable<ReviewDetailState['data']>;
}): React.ReactElement | null {
  if (data.commentsRestricted || data.commentsError) {
    return null;
  }
  const roots = (data.comments ?? []).filter((comment) => !comment.parentCommentId);
  if (roots.length === 0) {
    return null;
  }
  const unresolved = data.unresolvedThreads ?? 0;
  return (
    <div>
      <StatusBadge
        tone={unresolvedThreadsTone(unresolved)}
        label={unresolvedThreadsLabel(unresolved)}
      />
    </div>
  );
}

const reviewOpenStatuses = new Set(['OPEN', 'READY', 'FAILED', 'MERGE']);

// CloseReviewAction degrades by permission (no access, named rather than
// discovered from a failed submit) and gives Close a visible commitment
// boundary: a confirm step before the write, and the write's own
// busy/error state.
function CloseReviewAction({
  review,
  data,
  detail,
}: {
  review: UITenantDashboardReview;
  data: NonNullable<ReviewDetailState['data']>;
  detail: ReviewDetailState;
}): React.ReactElement | null {
  const dispatch = useAppDispatch();
  if (!reviewOpenStatuses.has(review.status)) {
    return null;
  }
  if (!data.canClose) {
    return (
      <p className="text-[13px] text-muted-foreground">
        You do not have access to close this review.
      </p>
    );
  }
  if (detail.closeConfirming) {
    return (
      <div className="flex flex-col gap-2">
        <div className="flex flex-wrap items-center gap-2">
          <span className="text-[13px] text-foreground">Close this review without merging it?</span>
          <Button
            type="button"
            variant="outline"
            size="sm"
            disabled={detail.closing}
            onClick={() => {
              dispatch(cancelCloseReview());
            }}
          >
            Cancel
          </Button>
          <Button
            type="button"
            size="sm"
            disabled={detail.closing}
            onClick={() => {
              void dispatch(submitCloseReview());
            }}
          >
            {detail.closing && <LoaderCircle className="animate-spin" aria-hidden="true" />}
            Confirm close
          </Button>
        </div>
        {detail.closeError && <InlineAlert>{detail.closeError}</InlineAlert>}
      </div>
    );
  }
  return (
    <Button
      type="button"
      variant="outline"
      size="sm"
      className="w-fit"
      onClick={() => {
        dispatch(confirmCloseReview());
      }}
    >
      Close review
    </Button>
  );
}

function ReviewDetailBuilds({
  data,
}: {
  data: NonNullable<ReviewDetailState['data']>;
}): React.ReactElement {
  if (data.buildsRestricted) {
    return (
      <EmptyState
        heading="You do not have access to this review's builds"
        body={`It needs ${data.buildsRestricted}. Ask an administrator for access.`}
      />
    );
  }
  if (data.buildsError) {
    return <InlineAlert>{data.buildsError}</InlineAlert>;
  }
  const builds = data.builds ?? [];
  if (builds.length === 0) {
    return (
      <EmptyState heading="No build yet" body="Nothing has recorded a build for this review yet." />
    );
  }
  return (
    <ul className="flex flex-col gap-1.5 text-[13px]">
      {builds.map((build) => (
        <ReviewDetailBuildRow key={build.buildId} build={build} />
      ))}
    </ul>
  );
}

function ReviewDetailBuildRow({ build }: { build: UITenantDashboardBuild }): React.ReactElement {
  return (
    <li className="flex items-center gap-2">
      <StatusBadge
        tone={build.successful ? 'success' : 'destructive'}
        label={build.successful ? 'Successful' : 'Failed'}
      />
      <span className="text-muted-foreground">{build.commitId}</span>
      {build.version && <span className="text-muted-foreground">v{build.version}</span>}
      <span className="ml-auto text-muted-foreground">{formatDashboardDate(build.createdAt)}</span>
    </li>
  );
}
