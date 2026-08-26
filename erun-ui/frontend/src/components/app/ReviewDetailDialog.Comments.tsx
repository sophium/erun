import { Button, EmptyState, Input, StatusBadge } from 'erun-kit';
import { LoaderCircle } from 'lucide-react';
import * as React from 'react';

import { useAppDispatch } from '@/app/hooks';
import {
  cancelReviewReply,
  setReviewReplyDraft,
  setReviewReplyTarget,
  submitResolveComment,
  submitReviewReply,
  submitUnresolveComment,
} from '@/app/reviewDetailThunks';
import type { ReviewDetailState } from '@/app/state';
import { formatDashboardDate } from '@/app/tenantDashboardPanels';
import type { UIReviewComment } from '@/types';

import { InlineAlert } from './InlineAlert';

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
    return <InlineAlert>{detail.data.commentsError}</InlineAlert>;
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
  const resolved = root.status === 'CLOSED';
  return (
    <div className="rounded-[var(--radius)] border border-border px-3 py-2.5 text-sm">
      <div className="flex items-start justify-between gap-2">
        <CommentLine comment={root} />
        <StatusBadge
          tone={resolved ? 'success' : 'warning'}
          label={resolved ? 'Resolved' : 'Unresolved'}
        />
      </div>
      {replies.map((reply) => (
        <div key={reply.commentId} className="mt-2 border-t border-border pt-2 pl-3">
          <CommentLine comment={reply} />
        </div>
      ))}
      <div className="mt-2 flex flex-wrap items-center gap-2">
        <ReplyAction root={root} detail={detail} />
        <ResolveThreadAction root={root} resolved={resolved} detail={detail} />
      </div>
      <ThreadReplyComposer root={root} detail={detail} />
      <ThreadResolveError root={root} detail={detail} />
    </div>
  );
}

// ReplyAction is hidden once this thread is already being replied to (the
// composer takes its place) and whenever the caller may not comment at all.
function ReplyAction({
  root,
  detail,
}: {
  root: UIReviewComment;
  detail: ReviewDetailState;
}): React.ReactElement | null {
  const dispatch = useAppDispatch();
  if (!detail.data?.canComment || detail.replyingTo === root.commentId) {
    return null;
  }
  return (
    <Button
      type="button"
      variant="outline"
      onClick={() => {
        dispatch(setReviewReplyTarget(root.commentId));
      }}
    >
      Reply
    </Button>
  );
}

function ThreadReplyComposer({
  root,
  detail,
}: {
  root: UIReviewComment;
  detail: ReviewDetailState;
}): React.ReactElement | null {
  if (detail.replyingTo !== root.commentId || !detail.data?.canComment) {
    return null;
  }
  return <ReplyComposer detail={detail} />;
}

// ResolveThreadAction is offered only on a thread's root — never a reply —
// so the surface cannot ask for a status change the platform will refuse.
// Hidden entirely when the caller lacks the write permission (degrade by
// permission, not by a failed submit).
function ResolveThreadAction({
  root,
  resolved,
  detail,
}: {
  root: UIReviewComment;
  resolved: boolean;
  detail: ReviewDetailState;
}): React.ReactElement | null {
  const dispatch = useAppDispatch();
  if (!detail.data?.canResolveComments) {
    return null;
  }
  const busy = detail.resolvingCommentId === root.commentId;
  return (
    <Button
      type="button"
      variant="outline"
      disabled={busy}
      onClick={() => {
        void dispatch(
          resolved ? submitUnresolveComment(root.commentId) : submitResolveComment(root.commentId),
        );
      }}
    >
      {busy && <LoaderCircle className="animate-spin" aria-hidden="true" />}
      {resolved ? 'Reopen' : 'Resolve'}
    </Button>
  );
}

function ThreadResolveError({
  root,
  detail,
}: {
  root: UIReviewComment;
  detail: ReviewDetailState;
}): React.ReactElement | null {
  if (detail.resolveErrorCommentId !== root.commentId || !detail.resolveError) {
    return null;
  }
  return (
    <div className="mt-2">
      <InlineAlert>{detail.resolveError}</InlineAlert>
    </div>
  );
}

function CommentLine({ comment }: { comment: UIReviewComment }): React.ReactElement {
  return (
    <div className="min-w-0">
      <div className="flex min-w-0 items-center gap-2 text-[13px]">
        <span className="flex-none font-medium text-foreground">
          {comment.creatorUserId ?? 'Unknown'}
        </span>
        <span className="min-w-0 truncate text-muted-foreground">
          {comment.filePath}:{comment.line}
        </span>
        <span className="flex-none text-muted-foreground">
          {formatDashboardDate(comment.createdAt)}
        </span>
      </div>
      <p className="mt-1 max-h-48 overflow-y-auto whitespace-pre-wrap text-foreground">
        {comment.body}
      </p>
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
      {detail.submitError && <InlineAlert>{detail.submitError}</InlineAlert>}
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
