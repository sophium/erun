import { Button, EmptyState, Input, StatusBadge } from 'erun-kit';
import { LoaderCircle } from 'lucide-react';
import * as React from 'react';

import { useAppDispatch } from '@/app/hooks';
import {
  cancelReviewReply,
  clearResolveCommentError,
  clearReviewReplyError,
  setReviewReplyDraft,
  setReviewReplyTarget,
  submitResolveComment,
  submitReviewReply,
  submitUnresolveComment,
} from '@/app/reviewDetailThunks';
import { hasShortcutModifier, isTypingTarget } from '@/app/reviewKeyboardShortcuts';
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
//
// Threads share the terminal tab strip's roving-tabindex shape
// (TerminalTabStrip.tsx): one thread is a real Tab stop at a time, Up/Down
// roam between them, and the focused thread carries the review surface's
// `R`/`Enter` bindings for reply and resolve/unresolve (erun-ui/AGENTS.md §
// "The keyboard model the review surface still owes"). The list container
// carries one native `keydown` listener (delegated by `data-thread-index`),
// not per-thread JSX `onKeyDown` -- jsx-a11y's non-interactive-element rules
// have no ARIA role that satisfies both "needs a role for its handler" and
// "should not have a handler for its role" at once for a div whose only fit
// is `role="region"` (TerminalController's own installReviewDiffKeydown hits
// the identical conflict for the diff panel and resolves it the same way).
export function ReviewDetailComments({
  detail,
}: {
  detail: ReviewDetailState;
}): React.ReactElement {
  const dispatch = useAppDispatch();
  const comments = detail.data?.comments ?? [];
  const roots = comments.filter((comment) => !comment.parentCommentId);
  const threadRefs = React.useRef<(HTMLDivElement | null)[]>([]);
  const [focusedIndex, setFocusedIndex] = React.useState(0);
  const keydownRef = React.useRef<(event: KeyboardEvent) => void>(() => undefined);
  const keydownCleanupRef = React.useRef<(() => void) | null>(null);
  const setListRef = React.useCallback((element: HTMLDivElement | null) => {
    keydownCleanupRef.current?.();
    keydownCleanupRef.current = null;
    if (!element) {
      return;
    }
    const listener = (event: KeyboardEvent): void => {
      keydownRef.current(event);
    };
    element.addEventListener('keydown', listener);
    keydownCleanupRef.current = () => {
      element.removeEventListener('keydown', listener);
    };
  }, []);
  // Cancelling a reply unmounts the composer's Input, and the browser drops
  // focus to <body> when its focused element is removed -- stranding a
  // keyboard user there with no further binding reachable. Detect the
  // replyingTo transition away from the roving-focused thread and reclaim
  // focus for it, the same correction React apps make after any control
  // that closes itself removes the element that held focus. A successful
  // submit closes the composer the same way, but loadReviewDetail's own
  // reload (submitReviewReply's last step) flashes the whole dialog to its
  // loading state first, unmounting this component before this effect would
  // run -- true for a mouse-driven Send today too, so it is not this
  // effect's problem to solve; the remounted list's default roving focus
  // (the first thread) is still reachable with one more Tab press either way.
  const previousReplyingToRef = React.useRef(detail.replyingTo);
  React.useEffect(() => {
    const closedFor = previousReplyingToRef.current;
    previousReplyingToRef.current = detail.replyingTo;
    if (!closedFor || closedFor === detail.replyingTo) {
      return;
    }
    const index = roots.findIndex((root) => root.commentId === closedFor);
    if (index < 0 || index !== Math.min(focusedIndex, roots.length - 1)) {
      return;
    }
    threadRefs.current[index]?.focus();
  }, [detail.replyingTo, roots, focusedIndex]);

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
  if (roots.length === 0) {
    return (
      <EmptyState
        heading="No comments yet"
        body="Comments made from the diff panel or the CLI's erun review comment will appear here."
      />
    );
  }

  const focusedRootIndex = Math.min(focusedIndex, roots.length - 1);
  const focusThread = (index: number): void => {
    setFocusedIndex(index);
    threadRefs.current[index]?.focus();
  };
  // The reply Input mounts only once setReviewReplyTarget's state update
  // lands, one render after this dispatch -- the same
  // dispatch-then-setTimeout(0)-then-query shape reviewThunks.ts's own
  // selectDiffPath uses to act on an element that has not rendered yet.
  const focusReplyInput = (index: number): void => {
    window.setTimeout(() => {
      threadRefs.current[index]
        ?.querySelector<HTMLInputElement>('input[aria-label="Reply"]')
        ?.focus();
    }, 0);
  };
  keydownRef.current = commentThreadKeyDownHandler(
    dispatch,
    detail,
    roots,
    focusThread,
    focusReplyInput,
  );

  return (
    <div ref={setListRef} className="flex flex-col gap-3">
      {roots.map((root, index) => (
        <CommentThread
          key={root.commentId}
          root={root}
          replies={comments.filter((comment) => comment.parentCommentId === root.commentId)}
          detail={detail}
          index={index}
          focused={index === focusedRootIndex}
          setRef={(element) => {
            threadRefs.current[index] = element;
          }}
        />
      ))}
    </div>
  );
}

// replyToFocusedThread re-dispatches setReviewReplyTarget only when this
// thread isn't already the reply target -- doing so unconditionally would
// wipe an in-progress draftBody every time `R` is pressed again.
function replyToFocusedThread(
  dispatch: ReturnType<typeof useAppDispatch>,
  detail: ReviewDetailState,
  root: UIReviewComment,
  index: number,
  focusReplyInput: (index: number) => void,
): void {
  if (!detail.data?.canComment) {
    return;
  }
  if (detail.replyingTo !== root.commentId) {
    dispatch(setReviewReplyTarget(root.commentId));
  }
  focusReplyInput(index);
}

function resolveOrReopenFocusedThread(
  dispatch: ReturnType<typeof useAppDispatch>,
  detail: ReviewDetailState,
  root: UIReviewComment,
): void {
  if (!detail.data?.canResolveComments) {
    return;
  }
  void dispatch(
    root.status === 'CLOSED'
      ? submitUnresolveComment(root.commentId)
      : submitResolveComment(root.commentId),
  );
}

// resolveFocusedThread reads which thread the delegated keydown listener's
// event bubbled up from, via the `data-thread-index` CommentThread stamps on
// itself -- null when the target isn't (or is no longer) a known thread.
function resolveFocusedThread(
  event: KeyboardEvent,
  roots: UIReviewComment[],
): { index: number; root: UIReviewComment } | null {
  const threadElement = (event.target as HTMLElement | null)?.closest<HTMLElement>(
    '[data-thread-index]',
  );
  const index = threadElement ? Number(threadElement.dataset.threadIndex) : NaN;
  const root = roots[index];
  return root ? { index, root } : null;
}

// commentThreadKeyDownHandler mirrors tabStripKeyDownHandler's shape
// (TerminalTabStrip.tsx): one factory closing over the list-level state,
// returning one delegated handler that resolves its thread from
// `data-thread-index` on the event target. Guarded the same way as the diff
// panel's own handler (reviewKeyboardShortcuts.ts): no binding fires while a
// text field has focus (the reply composer's Input) or a Cmd/Ctrl/Alt chord
// is held.
function commentThreadKeyDownHandler(
  dispatch: ReturnType<typeof useAppDispatch>,
  detail: ReviewDetailState,
  roots: UIReviewComment[],
  focusThread: (index: number) => void,
  focusReplyInput: (index: number) => void,
): (event: KeyboardEvent) => void {
  return (event: KeyboardEvent): void => {
    if (isTypingTarget(event.target) || hasShortcutModifier(event)) {
      return;
    }
    const focused = resolveFocusedThread(event, roots);
    if (!focused) {
      return;
    }
    const { index, root } = focused;
    switch (event.key) {
      case 'ArrowDown':
        event.preventDefault();
        focusThread(Math.min(index + 1, roots.length - 1));
        return;
      case 'ArrowUp':
        event.preventDefault();
        focusThread(Math.max(index - 1, 0));
        return;
      case 'r':
      case 'R':
        event.preventDefault();
        replyToFocusedThread(dispatch, detail, root, index, focusReplyInput);
        return;
      case 'Enter':
        event.preventDefault();
        resolveOrReopenFocusedThread(dispatch, detail, root);
        return;
      default:
        return;
    }
  };
}

function CommentThread({
  root,
  replies,
  detail,
  index,
  focused,
  setRef,
}: {
  root: UIReviewComment;
  replies: UIReviewComment[];
  detail: ReviewDetailState;
  index: number;
  focused: boolean;
  setRef: (element: HTMLDivElement | null) => void;
}): React.ReactElement {
  const resolved = root.status === 'CLOSED';
  return (
    <div
      ref={setRef}
      tabIndex={focused ? 0 : -1}
      role="region"
      aria-label={`Comment thread by ${commentAuthorDisplay(root)}`}
      data-thread-index={index}
      className="rounded-[var(--radius)] border border-border px-3 py-2.5 text-sm outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring"
    >
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
  const dispatch = useAppDispatch();
  if (detail.resolveErrorCommentId !== root.commentId || !detail.resolveError) {
    return null;
  }
  return (
    <div className="mt-2">
      <PlatformErrorAlert
        message={detail.resolveError}
        alias={detail.callerPlatformAlias}
        onRecovered={() => {
          dispatch(clearResolveCommentError());
        }}
      />
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
        <PlatformErrorAlert
          message={detail.submitError}
          alias={detail.callerPlatformAlias}
          onRecovered={() => {
            dispatch(clearReviewReplyError());
          }}
        />
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
