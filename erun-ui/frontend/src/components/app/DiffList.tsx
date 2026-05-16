import * as React from 'react';
import { AlertCircle, PlugZap, RefreshCw } from 'lucide-react';

import type { ERunUIController } from '@/app/ERunUIController';
import { compactDiffError, diffLineMark } from '@/app/diffUtils';
import { useAppSelector } from '@/app/hooks';
import { reconnectCopy } from '@/app/reconnectCopy';
import { Button } from '@/components/ui/button';
import { cn } from '@/lib/utils';
import type { DiffFile, DiffHunk } from '@/types';

export function DiffList({ controller }: { controller: ERunUIController }): React.ReactElement {
  const review = useAppSelector((state) => state.review);
  if (review.diffLoading) {
    return <ReviewStatus>Loading diff...</ReviewStatus>;
  }
  if (review.diffError) {
    return (
      <DiffErrorAlert
        message={compactDiffError(review.diffError)}
        loading={review.diffLoading}
        reconnectable={review.diffErrorReconnectable}
        onRetry={() => { void controller.loadReviewDiff(); }}
        onReconnect={() => controller.requestReconnect()}
      />
    );
  }
  const files = review.diff?.files || [];
  if (files.length === 0) {
    return <ReviewStatus>No changes</ReviewStatus>;
  }
  return (
    <>
      {files.map((file) => (
        <DiffFileView key={file.path} file={file} selected={file.path === review.selectedDiffPath} />
      ))}
      <span className="sr-only">{review.selectedDiffPath}</span>
    </>
  );
}

export function DiffErrorAlert({
  message,
  loading,
  reconnectable,
  onRetry,
  onReconnect,
}: {
  message: string;
  loading: boolean;
  reconnectable?: boolean;
  onRetry: () => void;
  onReconnect?: () => void;
}): React.ReactElement {
  const title = reconnectable ? reconnectCopy.errorTitle : 'Could not load diff';
  const body = reconnectable ? reconnectCopy.errorBody : message;
  return (
    <div
      role="alert"
      className="grid grid-cols-[auto_minmax(0,1fr)_auto] items-start gap-3 rounded-[var(--radius)] border border-destructive/40 bg-[color-mix(in_oklch,var(--destructive)_8%,transparent)] px-3 py-2.5 text-[13px] leading-[1.4]"
    >
      <AlertCircle className="mt-px size-[18px] flex-none text-destructive" aria-hidden="true" />
      <div className="min-w-0 [overflow-wrap:anywhere] text-foreground">
        <div className="font-semibold text-destructive">{title}</div>
        <div className="text-muted-foreground">{body}</div>
        {reconnectable && message && message !== body && (
          <div className="mt-1 truncate font-mono text-[12px] text-muted-foreground">{message}</div>
        )}
      </div>
      <div className="flex flex-col items-end gap-1.5">
        <Button type="button" variant="outline" size="sm" disabled={loading} onClick={onRetry}>
          <RefreshCw aria-hidden="true" />
          {reconnectCopy.retryAction}
        </Button>
        {reconnectable && onReconnect && (
          <Button type="button" variant="outline" size="sm" disabled={loading} onClick={onReconnect}>
            <PlugZap aria-hidden="true" />
            {reconnectCopy.reconnectAction}
          </Button>
        )}
      </div>
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
