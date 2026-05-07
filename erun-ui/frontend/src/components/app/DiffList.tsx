import * as React from 'react';
import { AlertCircle, RefreshCw } from 'lucide-react';

import type { ERunUIController } from '@/app/ERunUIController';
import { compactDiffError, diffLineMark } from '@/app/diffUtils';
import type { AppState } from '@/app/state';
import { Button } from '@/components/ui/button';
import { cn } from '@/lib/utils';
import type { DiffFile, DiffHunk } from '@/types';

export function DiffList({ controller, state }: { controller: ERunUIController; state: AppState }): React.ReactElement {
  if (state.diffLoading) {
    return <ReviewStatus>Loading diff...</ReviewStatus>;
  }
  if (state.diffError) {
    return <DiffErrorAlert message={compactDiffError(state.diffError)} loading={state.diffLoading} onRetry={() => { void controller.loadReviewDiff(); }} />;
  }
  const files = state.diff?.files || [];
  if (files.length === 0) {
    return <ReviewStatus>No changes</ReviewStatus>;
  }
  return (
    <>
      {files.map((file) => (
        <DiffFileView key={file.path} file={file} selected={file.path === state.selectedDiffPath} />
      ))}
      <span className="sr-only">{controller.state.selectedDiffPath}</span>
    </>
  );
}

export function DiffErrorAlert({ message, loading, onRetry }: { message: string; loading: boolean; onRetry: () => void }): React.ReactElement {
  return (
    <div
      role="alert"
      className="grid grid-cols-[auto_minmax(0,1fr)_auto] items-start gap-3 rounded-[var(--radius)] border border-destructive/40 bg-[color-mix(in_oklch,var(--destructive)_8%,transparent)] px-3 py-2.5 text-[13px] leading-[1.4]"
    >
      <AlertCircle className="mt-px size-[18px] flex-none text-destructive" aria-hidden="true" />
      <div className="min-w-0 [overflow-wrap:anywhere] text-foreground">
        <div className="font-semibold text-destructive">Could not load diff</div>
        <div className="text-muted-foreground">{message}</div>
      </div>
      <Button type="button" variant="outline" size="sm" disabled={loading} onClick={onRetry}>
        <RefreshCw aria-hidden="true" />
        Retry
      </Button>
    </div>
  );
}

function DiffFileView({ file, selected }: { file: DiffFile; selected: boolean }): React.ReactElement {
  return (
    <section className="diff-file scroll-mt-4" data-path={file.path} data-selected={selected || undefined}>
      <header className="flex items-center justify-between gap-4 px-1.5 pb-2.5 text-[13px] font-semibold text-foreground">
        <span className="min-w-0 truncate">{file.path}</span>
        <span className="flex-none font-semibold text-diff-add-foreground">
          <span>+{file.additions}</span> <span className="text-diff-delete-foreground">-{file.deletions}</span>
        </span>
      </header>
      {file.binary ? (
        <ReviewStatus>Binary file changed</ReviewStatus>
      ) : (
        (file.hunks || []).map((hunk) => <DiffHunkView key={hunk.header} hunk={hunk} />)
      )}
    </section>
  );
}

function DiffHunkView({ hunk }: { hunk: DiffHunk }): React.ReactElement {
  const contentWidth = Math.max(1, ...(hunk.lines || []).map((line) => line.content?.length || 0));
  const style = { '--diff-content-width': `${contentWidth + 2}ch` } as React.CSSProperties;

  return (
    <div className="overflow-hidden rounded-[var(--radius)] border bg-background not-first:mt-2.5">
      <div className="overflow-hidden bg-muted px-2.5 py-1.5 font-mono text-[11px] leading-[1.35] text-ellipsis whitespace-pre text-muted-foreground">
        {hunk.header}
      </div>
      <div className="relative max-w-full overflow-x-auto overflow-y-hidden" style={style}>
        {(hunk.lines || []).map((line, index) => (
          <div
            key={`${line.oldLine || ''}:${line.newLine || ''}:${index}`}
            className={cn(
              'grid min-h-5 w-max min-w-full grid-cols-[48px_48px_22px_minmax(var(--diff-content-width),1fr)] bg-background font-mono text-[11px] leading-5',
              line.kind === 'add' && 'bg-diff-add',
              line.kind === 'delete' && 'bg-diff-delete',
              line.kind === 'meta' && 'bg-muted text-muted-foreground',
            )}
          >
            <span className="select-none border-r border-[oklch(0_0_0/0.05)] bg-inherit px-2 text-right text-muted-foreground">
              {line.oldLine || ''}
            </span>
            <span className="select-none border-r border-[oklch(0_0_0/0.05)] bg-inherit px-2 text-right text-muted-foreground">
              {line.newLine || ''}
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
