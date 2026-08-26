import { Button, cn, Popover, PopoverAnchor, PopoverContent, Textarea } from 'erun-kit';
import { LoaderCircle, MessageSquarePlus } from 'lucide-react';
import * as React from 'react';

import { useAppDispatch, useAppSelector } from '@/app/hooks';
import {
  cancelReviewComment,
  clearReviewCommentError,
  setReviewCommentDraft,
  startReviewComment,
  submitReviewComment,
} from '@/app/reviewDetailThunks';
import { openTenantDashboard, setTenantDashboardTab } from '@/app/tenantDialogThunks';
import type { DiffLine } from '@/types';

import { PlatformErrorAlert } from './PlatformSignInAlert';

// DiffLineCommentAction starts a new top-level review thread anchored to this
// diff line — the gap ReviewDetailDialog.Comments.tsx used to call out as
// deferred, since only the diff panel knows which line was clicked.
//
// The affordance is revealed on hover or keyboard focus, the way the sidebar's
// per-row actions already are: the diff is the densest reading surface in the
// app, so one persistent icon on every line would compete with the code it is
// there to discuss. Its column leads the row and is always laid out, so
// revealing it shifts nothing and it cannot scroll out of view on a diff wider
// than the panel. Clicking it when a precondition is unmet explains which one
// rather than doing nothing.
export function DiffLineCommentAction({
  filePath,
  line,
  commitHash,
  tenant,
}: {
  filePath: string;
  line: DiffLine;
  commitHash: string;
  tenant: string;
}): React.ReactElement | null {
  const dispatch = useAppDispatch();
  // The active commenting context is the last-opened review, which survives
  // closing its dialog (see closeReviewDetail) precisely so it can still be
  // referenced from here — the dialog is modal and would otherwise cover the
  // diff panel entirely while "open".
  const hasActiveReview = useAppSelector((state) => state.reviewDetail.reviewId !== '');
  const canComment = useAppSelector((state) => state.reviewDetail.data?.canComment ?? false);
  const anchor = useAppSelector((state) => state.reviewDetail.newCommentAnchor);
  const [hintOpen, setHintOpen] = React.useState(false);

  const lineNumber = line.newLine;
  if (line.kind === 'meta' || lineNumber === undefined) {
    return null;
  }
  const isThisLine = anchor !== null && anchor.filePath === filePath && anchor.line === lineNumber;
  const blockedReason = diffLineCommentBlockedReason(hasActiveReview, canComment, commitHash);
  const open = isThisLine || hintOpen;

  return (
    <Popover
      open={open}
      onOpenChange={(next) => {
        if (next) {
          return;
        }
        setHintOpen(false);
        if (isThisLine) {
          dispatch(cancelReviewComment());
        }
      }}
    >
      <PopoverAnchor asChild>
        <button
          type="button"
          aria-label={`Comment on line ${String(lineNumber)} of ${filePath}`}
          className={cn(
            'flex size-full items-center justify-center border-r border-[oklch(0_0_0/0.05)] bg-inherit text-muted-foreground opacity-0 transition-opacity duration-150 group-hover:opacity-100 hover:text-foreground focus-visible:opacity-100 focus-visible:text-foreground',
            open && 'opacity-100',
          )}
          onClick={() => {
            if (blockedReason) {
              setHintOpen(true);
              return;
            }
            dispatch(startReviewComment({ commitId: commitHash, filePath, line: lineNumber }));
          }}
        >
          <MessageSquarePlus className="size-3" aria-hidden="true" />
        </button>
      </PopoverAnchor>
      <PopoverContent side="right" align="start" className="w-72">
        {blockedReason ? (
          <DiffLineCommentBlocked
            reason={blockedReason}
            tenant={tenant}
            showOpenReviewsTab={!hasActiveReview}
          />
        ) : (
          <DiffLineCommentComposer />
        )}
      </PopoverContent>
    </Popover>
  );
}

// DiffLineCommentBlocked renders the explanatory sentence every blocked
// reason gets, plus the one action that actually resolves a reason: the
// no-active-review case names the Reviews tab, which lives in the tenant
// dashboard — a different surface from this diff panel — so the popover
// navigates there in one click rather than leaving the reader to find it on
// their own (#1388). The other two reasons are actionable exactly where the
// operator already stands (commit the change, request access), so they stay
// message-only.
function DiffLineCommentBlocked({
  reason,
  tenant,
  showOpenReviewsTab,
}: {
  reason: string;
  tenant: string;
  showOpenReviewsTab: boolean;
}): React.ReactElement {
  const dispatch = useAppDispatch();
  return (
    <div className="grid gap-2">
      <p className="text-[13px] text-muted-foreground">{reason}</p>
      {showOpenReviewsTab && (
        <Button
          type="button"
          size="sm"
          onClick={() => {
            dispatch(openTenantDashboard(tenant));
            dispatch(setTenantDashboardTab('reviews'));
          }}
        >
          Open Reviews tab
        </Button>
      )}
    </div>
  );
}

function DiffLineCommentComposer(): React.ReactElement {
  const dispatch = useAppDispatch();
  const draft = useAppSelector((state) => state.reviewDetail.newCommentDraft);
  const submitting = useAppSelector((state) => state.reviewDetail.newCommentSubmitting);
  const submitError = useAppSelector((state) => state.reviewDetail.newCommentSubmitError);
  const cloudProviderAlias = useAppSelector((state) => state.reviewDetail.callerCloudProviderAlias);
  return (
    <div className="grid gap-2">
      <Textarea
        aria-label="New comment"
        placeholder="Start a discussion about this line…"
        value={draft}
        disabled={submitting}
        onChange={(event) => {
          dispatch(setReviewCommentDraft(event.target.value));
        }}
      />
      {submitError && (
        <PlatformErrorAlert
          message={submitError}
          alias={cloudProviderAlias}
          onRecovered={() => {
            dispatch(clearReviewCommentError());
          }}
        />
      )}
      <div className="flex justify-end gap-2">
        <Button
          type="button"
          variant="outline"
          size="sm"
          disabled={submitting}
          onClick={() => {
            dispatch(cancelReviewComment());
          }}
        >
          Cancel
        </Button>
        <Button
          type="button"
          size="sm"
          disabled={submitting || !draft.trim()}
          onClick={() => {
            void dispatch(submitReviewComment());
          }}
        >
          {submitting && <LoaderCircle className="animate-spin" aria-hidden="true" />}
          Comment
        </Button>
      </div>
    </div>
  );
}

function diffLineCommentBlockedReason(
  hasActiveReview: boolean,
  canComment: boolean,
  commitHash: string,
): string {
  if (!hasActiveReview) {
    return 'Open a review from the Reviews tab to comment on this line.';
  }
  if (!commitHash) {
    return 'Commit this change before commenting on it.';
  }
  if (!canComment) {
    return 'You do not have access to comment on this review.';
  }
  return '';
}
