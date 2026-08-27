import { Button, cn } from 'erun-kit';
import { AlertCircle, CheckCircle2, Copy, Info, Play, PlugZap, RefreshCw } from 'lucide-react';
import * as React from 'react';

import { compactDiffError, diffLineMark, visibleDiffFilePaths } from '@/app/diffUtils';
import { useAppDispatch, useAppSelector } from '@/app/hooks';
import { reachabilityCopy, type ReachabilityKind, reconnectCopy } from '@/app/reconnectCopy';
import { loadReviewDiff, requestReconnect } from '@/app/reviewThunks';
import { type ReviewEnvTarget, selectReviewEnvTargets } from '@/app/selectors';
import { diffPathKey } from '@/app/slices/reviewSlice';
import { useEnvDiffSlot } from '@/app/useEnvDiffSlot';
import { copyToClipboard } from '@/components/app/ActivityQueueDrawer.helpers';
import type { DiffFile, DiffHunk, DiffResult } from '@/types';

import { DiffLineCommentAction } from './DiffList.CommentAction';
import { StartReviewFromDiffAction } from './DiffList.StartReviewAction';
import { ReviewKeyboardShortcutsHint } from './ReviewKeyboardShortcuts';
import { ReviewEnvLabel } from './ReviewPanel.EnvLabel';

export function DiffList(): React.ReactElement {
  const targets = useAppSelector(selectReviewEnvTargets);
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

// DiffEnvSection renders one environment's diff, owning its own loading, error
// and empty states. That containment is the load-bearing part: the single-slot
// panel cleared one shared diff on any failure, so one stopped environment
// blanked every other linked env's diff -- and an orchestrator's environments
// are rarely all running at once, so that was the everyday state (#1178).
function DiffEnvSection({
  target,
  showHeader,
}: {
  target: ReviewEnvTarget;
  showHeader: boolean;
}): React.ReactElement {
  const dispatch = useAppDispatch();
  const slot = useEnvDiffSlot(target.envKey);
  const diffFilter = useAppSelector((state) => state.review.diffFilter);
  const collapsedDiffDirs = useAppSelector((state) => state.review.collapsedDiffDirs);
  const selectedDiffPath = useAppSelector((state) => state.review.selectedDiffPath);

  // The same ReviewEnvLabel treatment the review-layers block and the
  // changed-files tree use (#1314), so all three per-environment surfaces
  // read as one group instead of three independently-labelled ones. The
  // sticky wrapper stays: it is a real functional need (this header keeps the
  // active environment identity visible while a long diff scrolls), unlike
  // the label styling it wraps.
  //
  // Unlike the label, the "Start a review" action renders unconditionally —
  // one persistent affordance per environment section rather than one that
  // only appears once files have loaded, so it never flickers in and out as
  // the diff itself loads, errors, or comes back empty.
  const targetBranchHint = slot.diff?.reviewBase?.branch?.trim() ?? '';
  const header = (
    <div
      // data-env-key lets keyboard navigation (TerminalController's
      // startReviewForFocusedDiffEnv) find this section's own "Start a
      // review" button without duplicating the dialog-opening logic here.
      data-env-key={target.envKey}
      className={cn(
        'sticky top-0 z-10 flex items-center gap-3 border-b border-border bg-background px-3 py-1',
        showHeader ? 'justify-between' : 'justify-end',
      )}
    >
      {showHeader && <ReviewEnvLabel tenant={target.tenant} environment={target.environment} />}
      <ReviewKeyboardShortcutsHint />
      <StartReviewFromDiffAction
        tenant={target.tenant}
        environment={target.environment}
        targetBranch={targetBranchHint}
      />
    </div>
  );

  const body = ((): React.ReactElement => {
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
          onReconnect={() => {
            dispatch(requestReconnect(target.tenant, target.environment, slot.errorKind));
          }}
        />
      );
    }
    const allFiles = slot.diff?.files ?? [];
    if (allFiles.length === 0) {
      return <ReviewStatus>No changes</ReviewStatus>;
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
            tenant={target.tenant}
          />
        ))}
      </>
    );
  })();

  return (
    <>
      {header}
      {body}
    </>
  );
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
      // Lets keyboard navigation resolve which environment section a
      // focused hunk belongs to (TerminalController.startReviewForFocusedDiffEnv)
      // without threading envKey through every hunk element individually.
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
            <DiffLineCommentAction
              filePath={filePath}
              line={line}
              commitHash={commitHash}
              tenant={tenant}
            />
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
