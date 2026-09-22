import { Button, cn } from 'erun-kit';
import { AlertCircle, CheckCircle2, Copy, Info, Play, PlugZap, RefreshCw } from 'lucide-react';
import * as React from 'react';

import {
  compactDiffError,
  diffLineMark,
  diffReviewCommitCount,
  visibleDiffFilePaths,
} from '@/app/diffUtils';
import { useAppDispatch, useAppSelector } from '@/app/hooks';
import { reachabilityCopy, type ReachabilityKind, reconnectCopy } from '@/app/reconnectCopy';
import { loadReviewDiff, requestReconnect, selectReviewRange } from '@/app/reviewThunks';
import { type ReviewTarget, selectReviewTargets } from '@/app/selectors';
import { diffPathKey, type EnvDiffState, type ReviewScope } from '@/app/slices/reviewSlice';
import { useEnvDiffSlot } from '@/app/useEnvDiffSlot';
import { copyToClipboard } from '@/components/app/ActivityQueueDrawer.helpers';
import type { DiffFile, DiffHunk, DiffResult } from '@/types';

import { DiffLineCommentAction } from './DiffList.CommentAction';
import { DiffEnvSectionHeader } from './DiffList.EnvSectionHeader';

export function DiffList(): React.ReactElement {
  const targets = useAppSelector(selectReviewTargets);
  if (targets.length === 0) {
    return <ReviewStatus>No environment selected</ReviewStatus>;
  }
  // One section per environment, in the orchestrator's configured order. A
  // single environment renders exactly as before -- no header, no chrome -- so
  // the env-tab case is visually unchanged (#1178).
  const multi = targets.length > 1;
  return (
    <>
      {targets.map((target) => (
        <DiffEnvSection key={target.envKey} target={target} showHeader={multi} />
      ))}
    </>
  );
}

// DiffEnvSection renders one target's section -- its header, and its own
// loading, error and empty states -- owning those states per target. That
// containment is the load-bearing part: the single-slot panel cleared one
// shared diff on any failure, so one stopped environment blanked every other
// linked env's diff -- and an orchestrator's environments are rarely all
// running at once, so that was the everyday state.
//
// The header lives in DiffList.EnvSectionHeader.tsx, split off for the shared
// function-size and complexity budget (eslint.config.mjs) as much as for the
// file's own line budget.
function DiffEnvSection({
  target,
  showHeader,
}: {
  target: ReviewTarget;
  showHeader: boolean;
}): React.ReactElement {
  const slot = useEnvDiffSlot(target.envKey);
  const diffFilter = useAppSelector((state) => state.review.diffFilter);
  const collapsedDiffDirs = useAppSelector((state) => state.review.collapsedDiffDirs);
  const selectedDiffPath = useAppSelector((state) => state.review.selectedDiffPath);

  const targetBranchHint = slot.diff?.reviewBase?.branch?.trim() ?? '';
  const header = (
    <DiffEnvSectionHeader
      target={target}
      targetBranchHint={targetBranchHint}
      showHeader={showHeader}
    />
  );

  return (
    <>
      {header}
      <DiffEnvSectionBody
        target={target}
        slot={slot}
        diffFilter={diffFilter}
        collapsedDiffDirs={collapsedDiffDirs}
        selectedDiffPath={selectedDiffPath}
      />
    </>
  );
}

// DiffEnvSectionBody is DiffEnvSection's own body, split out so the
// loading/error/empty/populated branching doesn't grow DiffEnvSection itself
// past the shared function-size and complexity budget (eslint.config.mjs).
function DiffEnvSectionBody({
  target,
  slot,
  diffFilter,
  collapsedDiffDirs,
  selectedDiffPath,
}: {
  target: ReviewTarget;
  slot: EnvDiffState;
  diffFilter: string;
  collapsedDiffDirs: string[];
  selectedDiffPath: string;
}): React.ReactElement {
  const dispatch = useAppDispatch();
  if (slot.loading) {
    return <ReviewStatus>Loading diff...</ReviewStatus>;
  }
  if (slot.error) {
    return (
      <DiffErrorAlert
        message={compactDiffError(slot.error)}
        loading={slot.loading}
        reconnectable={slot.errorReconnectable}
        kind={slot.errorKind}
        onRetry={() => {
          void dispatch(loadReviewDiff());
        }}
        onReconnect={reconnectActionFor(target, slot.errorKind, dispatch)}
      />
    );
  }
  const allFiles = slot.diff?.files ?? [];
  if (allFiles.length === 0) {
    return (
      <DiffEmptyState
        envKey={target.envKey}
        scope={slot.scope}
        commitCount={diffReviewCommitCount(slot.diff)}
      />
    );
  }
  // Keep the diff panel's files and their order matching the changed-files
  // tree's visible subset; diff.files is already ordered to match the tree.
  // Collapsed dirs are env-keyed, so one env's collapsed directory cannot
  // hide a same-named directory in another.
  const collapsedForEnv = new Set(
    collapsedDiffDirs
      .filter((entry) => entry.startsWith(`${target.envKey}:`))
      .map((entry) => entry.slice(target.envKey.length + 1)),
  );
  const visiblePaths = visibleDiffFilePaths(slot.diff?.tree ?? [], diffFilter, collapsedForEnv);
  const files = allFiles.filter((file) => visiblePaths.has(file.path));
  if (files.length === 0) {
    return <ReviewStatus>No matching files</ReviewStatus>;
  }
  const commitHash = resolveDiffCommitHash(slot.diff);
  return (
    <>
      {files.map((file) => (
        <DiffFileView
          key={file.path}
          file={file}
          envKey={target.envKey}
          selected={diffPathKey(target.envKey, file.path) === selectedDiffPath}
          commitHash={commitHash}
          tenant={targetTenant(target)}
        />
      ))}
    </>
  );
}

// targetTenant is the tenant a target's platform affordances anchor to, and "" for
// a directory -- which has none -- so the diff renderer can leave a tenant-scoped
// affordance out rather than render one that could only fail.
function targetTenant(target: ReviewTarget): string {
  return target.kind === 'env' ? target.tenant : '';
}

// reconnectActionFor is the error alert's reconnect action, and it exists only
// for a target that has an environment to reconnect: reconnecting re-establishes
// that environment's MCP forward, and a directory has no environment and no edge,
// so the action would have nothing to re-establish. The alert takes it as
// optional, so a directory's alert renders without one.
function reconnectActionFor(
  target: ReviewTarget,
  kind: ReachabilityKind,
  dispatch: ReturnType<typeof useAppDispatch>,
): (() => void) | undefined {
  if (target.kind !== 'env') {
    return undefined;
  }
  return () => {
    dispatch(requestReconnect(target.tenant, target.environment, kind));
  };
}

// resolveDiffCommitHash is the commit a new diff-line thread anchors to: the
// specific commit when one is selected, otherwise the newest commit the
// diff's own range covers. Empty when the range covers only uncommitted
// worktree changes, which have no commit id to anchor a comment to yet.
function resolveDiffCommitHash(diff: DiffResult | null | undefined): string {
  const selected = diff?.selectedCommit?.trim();
  if (selected) {
    return selected;
  }
  const commits = diff?.reviewCommits ?? [];
  return commits[commits.length - 1]?.hash ?? '';
}

// diffErrorCopy resolves the title/body/technical-detail text and whether this
// is the informational not-running case, as one pure step so DiffErrorAlert
// itself only has layout branching left (#1230 pushed the complexity here).
function diffErrorCopy(
  message: string,
  reconnectable: boolean | undefined,
  kind: ReachabilityKind | undefined,
): { notRunning: boolean; title: string; body: string; technicalMessage: string; action: string } {
  const notRunning = Boolean(reconnectable) && kind === 'not-open';
  const copy = reachabilityCopy[kind ?? 'stale-forward'];
  const title = reconnectable ? copy.errorTitle : 'Could not load diff';
  const body = reconnectable ? copy.errorBody : message;
  const technicalMessage = reconnectable && message && message !== body ? message : '';
  return { notRunning, title, body, technicalMessage, action: copy.action };
}

export function DiffErrorAlert({
  message,
  loading,
  reconnectable,
  kind,
  onRetry,
  onReconnect,
}: {
  message: string;
  loading: boolean;
  reconnectable?: boolean;
  kind?: ReachabilityKind;
  onRetry: () => void;
  onReconnect?: () => void;
}): React.ReactElement {
  // A stopped/never-opened environment is the ordinary resting state, not a
  // fault -- it renders as an informational status, not a red alert, with
  // "Open" as the primary action instead of "Reconnect…" (#1230).
  const { notRunning, title, body, technicalMessage, action } = diffErrorCopy(
    message,
    reconnectable,
    kind,
  );
  return (
    <div
      role={notRunning ? 'status' : 'alert'}
      className={cn(
        'grid grid-cols-[auto_minmax(0,1fr)_auto] items-start gap-3 rounded-[var(--radius)] border px-3 py-2.5 text-[13px] leading-[1.4]',
        notRunning
          ? 'border-border bg-muted/40'
          : 'border-destructive/40 bg-[color-mix(in_oklch,var(--destructive)_8%,transparent)]',
      )}
    >
      <DiffErrorIcon notRunning={notRunning} />
      <DiffErrorBody
        notRunning={notRunning}
        title={title}
        body={body}
        technical={technicalMessage}
      />
      <DiffErrorActions
        notRunning={notRunning}
        reconnectable={reconnectable}
        loading={loading}
        actionLabel={action}
        onRetry={onRetry}
        onReconnect={onReconnect}
        clipboardText={[title, body, technicalMessage].filter(Boolean).join('\n')}
      />
    </div>
  );
}

function DiffErrorIcon({ notRunning }: { notRunning: boolean }): React.ReactElement {
  if (notRunning) {
    return (
      <Info className="mt-px size-[18px] flex-none text-muted-foreground" aria-hidden="true" />
    );
  }
  return (
    <AlertCircle className="mt-px size-[18px] flex-none text-destructive" aria-hidden="true" />
  );
}

function DiffErrorBody({
  notRunning,
  title,
  body,
  technical,
}: {
  notRunning: boolean;
  title: string;
  body: string;
  technical: string;
}): React.ReactElement {
  return (
    <div className="min-w-0 [overflow-wrap:anywhere] text-foreground">
      <div className={cn('font-semibold', notRunning ? 'text-foreground' : 'text-destructive')}>
        {title}
      </div>
      <div className="text-muted-foreground">{body}</div>
      {technical && (
        <div className="mt-1 font-mono text-[12px] break-words whitespace-pre-wrap text-muted-foreground select-text">
          {technical}
        </div>
      )}
    </div>
  );
}

function DiffErrorActions({
  notRunning,
  reconnectable,
  loading,
  actionLabel,
  onRetry,
  onReconnect,
  clipboardText,
}: {
  notRunning: boolean;
  reconnectable?: boolean;
  loading: boolean;
  actionLabel: string;
  onRetry: () => void;
  onReconnect?: () => void;
  clipboardText: string;
}): React.ReactElement {
  return (
    <div className="flex flex-col items-end gap-1.5">
      {!notRunning && (
        <Button type="button" variant="outline" size="sm" disabled={loading} onClick={onRetry}>
          <RefreshCw aria-hidden="true" />
          {reconnectCopy.retryAction}
        </Button>
      )}
      {reconnectable && onReconnect && (
        <Button type="button" variant="outline" size="sm" disabled={loading} onClick={onReconnect}>
          {notRunning ? <Play aria-hidden="true" /> : <PlugZap aria-hidden="true" />}
          {actionLabel}
        </Button>
      )}
      {!notRunning && <CopyErrorButton text={clipboardText} />}
    </div>
  );
}

function CopyErrorButton({ text }: { text: string }): React.ReactElement {
  const [copied, setCopied] = React.useState(false);
  React.useEffect(() => {
    if (!copied) {
      return;
    }
    const timer = window.setTimeout(() => {
      setCopied(false);
    }, 1500);
    return () => {
      window.clearTimeout(timer);
    };
  }, [copied]);
  return (
    <Button
      type="button"
      variant="ghost"
      size="sm"
      aria-label="Copy error message"
      onClick={() => {
        void copyToClipboard(text).then(() => {
          setCopied(true);
        });
      }}
    >
      {copied ? <CheckCircle2 aria-hidden="true" /> : <Copy aria-hidden="true" />}
      {copied ? 'Copied' : 'Copy'}
    </Button>
  );
}

function DiffFileView({
  file,
  envKey,
  selected,
  commitHash,
  tenant,
}: {
  file: DiffFile;
  envKey: string;
  selected: boolean;
  commitHash: string;
  tenant: string;
}): React.ReactElement {
  return (
    <section
      className="diff-file scroll-mt-4"
      data-path={file.path}
      // Lets keyboard navigation resolve which section a focused hunk belongs
      // to (reviewDiffKeyboardNav's startReviewForFocusedEnv) without threading
      // envKey through every hunk element individually.
      data-env-key={envKey}
      data-selected={selected || undefined}
    >
      <header className="flex items-center justify-between gap-4 px-1.5 pb-2.5 text-[13px] font-semibold text-foreground">
        <span className="min-w-0 truncate">{file.path}</span>
        <span className="flex-none font-semibold text-diff-add-foreground">
          <span>+{file.additions}</span>{' '}
          <span className="text-diff-delete-foreground">-{file.deletions}</span>
        </span>
      </header>
      {file.binary ? (
        <ReviewStatus>Binary file changed</ReviewStatus>
      ) : (
        (file.hunks ?? []).map((hunk) => (
          <DiffHunkView
            key={hunk.header}
            hunk={hunk}
            filePath={file.path}
            commitHash={commitHash}
            tenant={tenant}
          />
        ))
      )}
    </section>
  );
}

function DiffHunkView({
  hunk,
  filePath,
  commitHash,
  tenant,
}: {
  hunk: DiffHunk;
  filePath: string;
  commitHash: string;
  tenant: string;
}): React.ReactElement {
  const contentWidth = Math.max(1, ...(hunk.lines ?? []).map((line) => line.content.length));
  const style = { '--diff-content-width': `${String(contentWidth + 2)}ch` } as React.CSSProperties;

  return (
    <div className="overflow-hidden rounded-[var(--radius)] border bg-background not-first:mt-2.5">
      <div className="overflow-hidden bg-muted px-2.5 py-1.5 font-mono text-[11px] leading-[1.35] text-ellipsis whitespace-pre text-muted-foreground">
        {hunk.header}
      </div>
      <div
        tabIndex={0}
        role="region"
        aria-label={`Diff for ${filePath} at ${hunk.header}`}
        className="relative max-w-full overflow-x-auto overflow-y-hidden outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring"
        style={style}
      >
        {(hunk.lines ?? []).map((line, index) => (
          <div
            key={`${String(line.oldLine ?? '')}:${String(line.newLine ?? '')}:${String(index)}`}
            className={cn(
              'group grid min-h-5 w-max min-w-full grid-cols-[22px_48px_48px_22px_minmax(var(--diff-content-width),1fr)] bg-background font-mono text-[11px] leading-5',
              line.kind === 'add' && 'bg-diff-add',
              line.kind === 'delete' && 'bg-diff-delete',
              line.kind === 'meta' && 'bg-muted text-muted-foreground',
            )}
          >
            {/* Leads the row: a trailing column sits past the content width, so
                on any diff wider than the panel the affordance was only
                reachable by scrolling right. */}
            {/* A line comment anchors to a hosted review record on the tenant's
                platform, so it is offered only where there is a tenant to anchor
                to. A directory has none (that is what makes it a directory
                rather than an environment), and an affordance that opened a
                tenant-scoped dialog with no tenant would be worse than its
                absence. */}
            {tenant === '' ? null : (
              <DiffLineCommentAction
                filePath={filePath}
                line={line}
                commitHash={commitHash}
                tenant={tenant}
              />
            )}
            <span className="select-none border-r border-[oklch(0_0_0/0.05)] bg-inherit px-2 text-right text-muted-foreground">
              {line.oldLine ?? ''}
            </span>
            <span className="select-none border-r border-[oklch(0_0_0/0.05)] bg-inherit px-2 text-right text-muted-foreground">
              {line.newLine ?? ''}
            </span>
            <span className="select-none border-r border-[oklch(0_0_0/0.05)] bg-inherit text-center text-foreground">
              {diffLineMark(line.kind)}
            </span>
            <span className="min-w-0 whitespace-pre pr-4">{line.content || ' '}</span>
          </div>
        ))}
      </div>
    </div>
  );
}

export function ReviewStatus({ children }: { children: React.ReactNode }): React.ReactElement {
  return <div className="px-3 py-3.5 text-sm leading-[1.4] text-muted-foreground">{children}</div>;
}

// DiffEmptyState is the diff panel's (and the changed-files tree's) shared
// empty-state render. The "current" scope only ever diffs uncommitted local
// edits, so a clean worktree with commits ahead of the review base reads as
// a flat "No changes" even though there is history waiting to be reviewed
// under a different scope -- the result object already carries those commits
// (DiffResult.reviewCommits), so asserting "no changes" without looking at
// them would be exactly the confident-but-wrong empty state this component
// exists to avoid. Other scopes ("all", "commit") have nothing further back
// to point at, so they keep the plain message.
export function DiffEmptyState({
  envKey,
  scope,
  commitCount,
}: {
  envKey: string;
  scope: ReviewScope;
  commitCount: number;
}): React.ReactElement {
  const dispatch = useAppDispatch();
  if (scope !== 'current' || commitCount === 0) {
    return <ReviewStatus>No changes</ReviewStatus>;
  }
  return (
    <div
      role="status"
      className="mx-1.5 my-2 flex flex-col items-start gap-2 rounded-[var(--radius)] border border-border bg-muted/40 px-3 py-2.5 text-[13px] leading-[1.4] text-muted-foreground"
    >
      <span>
        No local changes —{' '}
        {commitCount === 1 ? '1 commit is' : `${String(commitCount)} commits are`} not shown in this
        view.
      </span>
      <Button
        type="button"
        variant="outline"
        size="sm"
        onClick={() => {
          dispatch(selectReviewRange(envKey, 'all'));
        }}
      >
        View all branch changes
      </Button>
    </div>
  );
}
