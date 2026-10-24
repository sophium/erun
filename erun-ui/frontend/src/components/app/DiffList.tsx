import { Button, cn } from 'erun-kit';
import { AlertCircle, CheckCircle2, Copy, Info, Play, PlugZap, RefreshCw } from 'lucide-react';
import * as React from 'react';

import { compactDiffError, diffReviewCommitCount, visibleDiffFilePaths } from '@/app/diffUtils';
import { useAppDispatch, useAppSelector } from '@/app/hooks';
import { reachabilityCopy, type ReachabilityKind, reconnectCopy } from '@/app/reconnectCopy';
import { loadReviewDiff, requestReconnect, selectReviewRange } from '@/app/reviewThunks';
import { type ReviewTarget, selectReviewTargets } from '@/app/selectors';
import { diffPathKey, type EnvDiffState, type ReviewScope } from '@/app/slices/reviewSlice';
import { useEnvDiffSlot } from '@/app/useEnvDiffSlot';
import { copyToClipboard } from '@/components/app/ActivityQueueDrawer.helpers';
import type { DiffFile, DiffHunk, DiffResult } from '@/types';

import { DiffEnvSectionHeader } from './DiffList.EnvSectionHeader';
import { DiffLineRow } from './DiffList.LineRow';

export function DiffList(): React.ReactElement {
  const targets = useAppSelector(selectReviewTargets);
  if (targets.length === 0) {
    return <ReviewStatus>No environment selected</ReviewStatus>;
  }
  // A single environment renders without section chrome.
  const multi = targets.length > 1;
  return (
    <>
      {targets.map((target) => (
        <DiffEnvSection key={target.envKey} target={target} showHeader={multi} />
      ))}
    </>
  );
}

// DiffEnvSection owns loading, error and empty states per target, so one
// stopped environment does not blank the others.
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
  // Held here by line identity, since re-reads can remount the rows below.
  const [revealedLine, setRevealedLine] = React.useState('');
  useRevealTracking(setRevealedLine);
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
  // Collapsed dirs are env-keyed, so one env cannot hide another's same-named dir.
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
          revealedLine={revealedLine}
        />
      ))}
    </>
  );
}

// useRevealTracking listens for pointermove, not boundary events: a re-read
// moving the row under a stationary pointer fires pointerout/over without moving.
function useRevealTracking(setRevealedLine: (key: string) => void): void {
  React.useEffect(() => {
    const track = (event: PointerEvent): void => {
      const under =
        event.target instanceof Element ? event.target.closest('[data-reveal-key]') : null;
      setRevealedLine(under?.getAttribute('data-reveal-key') ?? '');
    };
    window.addEventListener('pointermove', track);
    return () => {
      window.removeEventListener('pointermove', track);
    };
  }, [setRevealedLine]);
}

// targetTenant is "" for a directory, which has no platform affordances.
function targetTenant(target: ReviewTarget): string {
  return target.kind === 'env' ? target.tenant : '';
}

// A directory has no environment to reconnect, so it gets no action.
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

// resolveDiffCommitHash is empty when the range covers only uncommitted changes.
function resolveDiffCommitHash(diff: DiffResult | null | undefined): string {
  const selected = diff?.selectedCommit?.trim();
  if (selected) {
    return selected;
  }
  const commits = diff?.reviewCommits ?? [];
  return commits[commits.length - 1]?.hash ?? '';
}

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
  // A stopped environment is the normal resting state: informational, with "Open".
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
  revealedLine,
}: {
  file: DiffFile;
  envKey: string;
  selected: boolean;
  commitHash: string;
  tenant: string;
  revealedLine: string;
}): React.ReactElement {
  return (
    <section
      className="diff-file scroll-mt-4"
      data-path={file.path}
      // Lets reviewDiffKeyboardNav map a focused hunk to its section.
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
            envKey={envKey}
            revealedLine={revealedLine}
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
  envKey,
  revealedLine,
}: {
  hunk: DiffHunk;
  filePath: string;
  commitHash: string;
  tenant: string;
  envKey: string;
  revealedLine: string;
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
          <DiffLineRow
            key={`${String(line.oldLine ?? '')}:${String(line.newLine ?? '')}:${String(index)}`}
            line={line}
            filePath={filePath}
            commitHash={commitHash}
            tenant={tenant}
            envKey={envKey}
            revealedLine={revealedLine}
          />
        ))}
      </div>
    </div>
  );
}

export function ReviewStatus({ children }: { children: React.ReactNode }): React.ReactElement {
  return <div className="px-3 py-3.5 text-sm leading-[1.4] text-muted-foreground">{children}</div>;
}

// DiffEmptyState points the "current" scope at commits awaiting review
// (DiffResult.reviewCommits) instead of a flat "No changes".
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
