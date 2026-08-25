import { Button, EmptyState, Input } from 'erun-kit';
import { LoaderCircle } from 'lucide-react';
import * as React from 'react';

import { useAppDispatch } from '@/app/hooks';
import {
  cancelReviewReply,
  setReviewReplyDraft,
  setReviewReplyTarget,
  submitReviewReply,
} from '@/app/reviewDetailThunks';
import type { ReviewDetailState } from '@/app/state';
import { formatDashboardDate } from '@/app/tenantDashboardPanels';
import type { UIReviewComment } from '@/types';

// ReviewDetailComments renders the review's threads (root comments with their
// replies nested under them) and, per thread, a reply composer. Starting a
// new top-level thread needs a diff-line anchor this dialog does not have —
// that's the diff panel's job — so only replying to an existing thread is
// wired here.
export function ReviewDetailComments({
  detail,
}: {
  detail: ReviewDetailState;
}): React.ReactElement {
  const comments = detail.data?.comments ?? [];
  if (detail.data?.commentsRestricted) {
    return (
      <EmptyState
        heading="You do not have access to this review's comments"
        body={`It needs ${detail.data.commentsRestricted}. Ask an administrator for access.`}
      />
    );
  }
  if (detail.data?.commentsError) {
    return <p className="text-sm text-destructive">{detail.data.commentsError}</p>;
  }
  const roots = comments.filter((comment) => !comment.parentCommentId);
  if (roots.length === 0) {
    return (
      <EmptyState
        heading="No comments yet"
        body="Comments made from the diff panel or the CLI's erun review comment will appear here."
      />
    );
  }
  return (
    <div className="flex flex-col gap-3">
      {roots.map((root) => (
        <CommentThread
          key={root.commentId}
          root={root}
          replies={comments.filter((comment) => comment.parentCommentId === root.commentId)}
          detail={detail}
        />
      ))}
    </div>
  );
}

function CommentThread({
  root,
  replies,
  detail,
}: {
  root: UIReviewComment;
  replies: UIReviewComment[];
  detail: ReviewDetailState;
}): React.ReactElement {
  const dispatch = useAppDispatch();
  const replyingHere = detail.replyingTo === root.commentId;
  return (
    <div className="rounded-[var(--radius)] border border-border px-3 py-2.5 text-sm">
      <CommentLine comment={root} />
      {replies.map((reply) => (
        <div key={reply.commentId} className="mt-2 border-t border-border pt-2 pl-3">
          <CommentLine comment={reply} />
        </div>
      ))}
      {detail.data?.canComment &&
        (replyingHere ? (
          <ReplyComposer detail={detail} />
        ) : (
          <Button
            type="button"
            variant="outline"
            className="mt-2"
            onClick={() => {
              dispatch(setReviewReplyTarget(root.commentId));
            }}
          >
            Reply
          </Button>
        ))}
    </div>
  );
}

function CommentLine({ comment }: { comment: UIReviewComment }): React.ReactElement {
  return (
    <div>
      <div className="flex items-center gap-2 text-[13px]">
        <span className="font-medium text-foreground">{comment.creatorUserId ?? 'Unknown'}</span>
        <span className="text-muted-foreground">
          {comment.filePath}:{comment.line}
        </span>
        <span className="text-muted-foreground">{formatDashboardDate(comment.createdAt)}</span>
      </div>
      <p className="mt-1 whitespace-pre-wrap text-foreground">{comment.body}</p>
    </div>
  );
}

// ReplyComposer: body is genuine free text the operator authors; every other
// field a reply needs (commitId, filePath, line, parentCommentId) is carried
// automatically from the thread it replies to (see reviewDetailThunks.ts), so
// nothing here asks the operator to type an anchor they didn't choose.
function ReplyComposer({ detail }: { detail: ReviewDetailState }): React.ReactElement {
  const dispatch = useAppDispatch();
  return (
    <div className="mt-2 flex flex-col gap-1.5">
      <Input
        aria-label="Reply"
        placeholder="Reply…"
        value={detail.draftBody}
        disabled={detail.submitting}
        onChange={(event) => {
          dispatch(setReviewReplyDraft(event.target.value));
        }}
        onKeyDown={(event) => {
          if (event.key === 'Enter' && detail.draftBody.trim() && !detail.submitting) {
            void dispatch(submitReviewReply());
          }
        }}
      />
      {detail.submitError && <p className="text-[13px] text-destructive">{detail.submitError}</p>}
      <div className="flex justify-end gap-2">
        <Button
          type="button"
          variant="outline"
          disabled={detail.submitting}
          onClick={() => {
            dispatch(cancelReviewReply());
          }}
        >
          Cancel
        </Button>
        <Button
          type="button"
          disabled={detail.submitting || !detail.draftBody.trim()}
          onClick={() => void dispatch(submitReviewReply())}
        >
          {detail.submitting && <LoaderCircle className="animate-spin" aria-hidden="true" />}
          Send
        </Button>
      </div>
    </div>
  );
}
