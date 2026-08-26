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
import type { UIReviewComment } from '@/types';

import { InlineAlert } from './InlineAlert';
import { PlatformErrorAlert } from './PlatformSignInAlert';
import { RelativeTime } from './TenantDashboardMessage';

// commentAuthorDisplay prefers the resolved tenant-user-directory username
// over the raw creator id (#1378) — the same fallback order
// displayReviewAuthor uses in the reviews list, minus the "You" special case:
// the dialog has no signed-in user id threaded down to it, and the raw id is
// still a meaningful fallback rather than nothing.
function commentAuthorDisplay(comment: UIReviewComment): string {
  const id = comment.creatorUserId?.trim() ?? '';
  if (!id) {
    return 'Unknown';
  }
  return comment.creatorUsername?.trim() ?? id;
}

// COMMENT_PREVIEW_LENGTH bounds a comment body before it needs an explicit
// "Show more" — long enough that ordinary comments never see it, short
// enough that a very long one doesn't push Reply/Resolve off without warning
// (#1378: a review tool that cannot display a long comment has failed at its
// core job).
const COMMENT_PREVIEW_LENGTH = 400;

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
  // Resolving is the action that moves the review forward, so it carries the
  // primary button weight — Reply stays outline (secondary) beside it,
  // matching how the repo already distinguishes primary from secondary
  // actions elsewhere (#1378). Reopening a thread is a correction, not the
  // forward action, so it keeps the outline treatment.
  return (
    <Button
      type="button"
      variant={resolved ? 'outline' : 'default'}
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
      <PlatformErrorAlert message={detail.resolveError} alias={detail.callerCloudProviderAlias} />
    </div>
  );
}

function CommentLine({ comment }: { comment: UIReviewComment }): React.ReactElement {
  return (
    <div className="min-w-0">
      <div className="flex min-w-0 items-center gap-2 text-[13px]">
        <span className="flex-none font-medium text-foreground">
          {commentAuthorDisplay(comment)}
        </span>
        {/* The anchor is commit + file + line — the line stays flex-none so it
            is never the part truncation eats; only the file path shrinks. */}
        <span className="flex-none font-mono text-muted-foreground">
          {comment.commitId.slice(0, 7)}
        </span>
        <span className="min-w-0 truncate text-muted-foreground" title={comment.filePath}>
          {comment.filePath}
        </span>
        <span className="flex-none font-mono text-muted-foreground">:{comment.line}</span>
        <RelativeTime value={comment.createdAt} className="flex-none text-muted-foreground" />
      </div>
      <CommentBody body={comment.body} />
    </div>
  );
}

// CommentBody gives a long comment a visible way to read the rest instead of
// relying on an invisible scroll region (#1378): a native scrollbar inside a
// max-height box gave no affordance that more text existed, so a long
// comment read as silently clipped. Collapsed text still shows a preview
// (not just "click to reveal a blank box"), and the toggle is a real control
// rather than a hover-only scrollbar a keyboard user cannot discover.
function CommentBody({ body }: { body: string }): React.ReactElement {
  const [expanded, setExpanded] = React.useState(false);
  const isLong = body.length > COMMENT_PREVIEW_LENGTH;
  const shown = !isLong || expanded ? body : `${body.slice(0, COMMENT_PREVIEW_LENGTH)}…`;
  return (
    <div className="mt-1">
      <p className="max-w-[640px] whitespace-pre-wrap text-foreground">{shown}</p>
      {isLong && (
        <Button
          type="button"
          variant="link"
          size="xs"
          className="h-auto px-0"
          onClick={() => {
            setExpanded((value) => !value);
          }}
        >
          {expanded ? 'Show less' : 'Show more'}
        </Button>
      )}
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
      {detail.submitError && (
        <PlatformErrorAlert message={detail.submitError} alias={detail.callerCloudProviderAlias} />
      )}
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
