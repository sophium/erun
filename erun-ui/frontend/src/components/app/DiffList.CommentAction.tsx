import { Button, cn, Popover, PopoverAnchor, PopoverContent, Textarea } from 'erun-kit';
import { LoaderCircle, MessageSquarePlus } from 'lucide-react';
import * as React from 'react';

import { useAppDispatch, useAppSelector } from '@/app/hooks';
import {
  cancelReviewComment,
  setReviewCommentDraft,
  startReviewComment,
  submitReviewComment,
} from '@/app/reviewDetailThunks';
import type { DiffLine } from '@/types';

import { InlineAlert } from './InlineAlert';

// DiffLineCommentAction starts a new top-level review thread anchored to this
// diff line — the gap ReviewDetailDialog.Comments.tsx used to call out as
// deferred, since only the diff panel knows which line was clicked.
//
// The affordance is revealed on hover or keyboard focus, the way the sidebar's
// per-row actions already are: the diff is the densest reading surface in the
// app, so one persistent icon on every line would compete with the code it is
// there to discuss. Its column is always laid out, so revealing it shifts
// nothing. Clicking it when a precondition is unmet explains which one rather
// than doing nothing.
export function DiffLineCommentAction({
  filePath,
  line,
  commitHash,
}: {
  filePath: string;
  line: DiffLine;
  commitHash: string;
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
            'flex size-full items-center justify-center border-l border-[oklch(0_0_0/0.05)] bg-inherit text-muted-foreground opacity-0 transition-opacity duration-150 group-hover:opacity-100 hover:text-foreground focus-visible:opacity-100 focus-visible:text-foreground',
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
          <p className="text-[13px] text-muted-foreground">{blockedReason}</p>
        ) : (
          <DiffLineCommentComposer />
        )}
      </PopoverContent>
    </Popover>
  );
}

function DiffLineCommentComposer(): React.ReactElement {
  const dispatch = useAppDispatch();
  const draft = useAppSelector((state) => state.reviewDetail.newCommentDraft);
  const submitting = useAppSelector((state) => state.reviewDetail.newCommentSubmitting);
  const submitError = useAppSelector((state) => state.reviewDetail.newCommentSubmitError);
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
      {submitError && <InlineAlert>{submitError}</InlineAlert>}
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
