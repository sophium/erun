import { cn } from 'erun-kit';
import * as React from 'react';

import { diffLineMark } from '@/app/diffUtils';
import type { DiffLine } from '@/types';

import { DiffLineCommentAction } from './DiffList.CommentAction';

// lineRevealKey identifies a line across re-reads; empty for rows with no affordance.
function lineRevealKey(envKey: string, tenant: string, filePath: string, line: DiffLine): string {
  if (tenant === '' || line.kind === 'meta' || line.newLine === undefined) {
    return '';
  }
  return `${envKey}|${filePath}:${String(line.newLine)}`;
}

// `data-reveal-key` is rendered on every row, empty included, so moving onto a
// row with no affordance ends the reveal.
export function DiffLineRow({
  line,
  envKey,
  filePath,
  commitHash,
  tenant,
  revealedLine,
}: {
  line: DiffLine;
  envKey: string;
  filePath: string;
  commitHash: string;
  tenant: string;
  revealedLine: string;
}): React.ReactElement {
  const revealKey = lineRevealKey(envKey, tenant, filePath, line);
  const revealed = revealKey !== '' && revealKey === revealedLine;
  return (
    <div
      data-reveal-key={revealKey}
      className={cn(
        'group grid min-h-5 w-max min-w-full grid-cols-[22px_48px_48px_22px_minmax(var(--diff-content-width),1fr)] bg-background font-mono text-[11px] leading-5',
        line.kind === 'add' && 'bg-diff-add',
        line.kind === 'delete' && 'bg-diff-delete',
        line.kind === 'meta' && 'bg-muted text-muted-foreground',
      )}
    >
      {/* Leads the row so it stays reachable on diffs wider than the panel. */}
      {/* A directory has no tenant to anchor a review comment to. */}
      {tenant === '' ? null : (
        <DiffLineCommentAction
          filePath={filePath}
          line={line}
          commitHash={commitHash}
          tenant={tenant}
          revealed={revealed}
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
  );
}
