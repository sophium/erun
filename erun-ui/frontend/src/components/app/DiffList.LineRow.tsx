import { cn } from 'erun-kit';
import * as React from 'react';

import { diffLineMark } from '@/app/diffUtils';
import type { DiffLine } from '@/types';

import { DiffLineCommentAction } from './DiffList.CommentAction';

// lineRevealKey is the identity a revealed comment affordance follows across a
// re-read: the line's own environment, file and number -- what the affordance is
// anchored to and what the reader pointed at, rather than the element the
// pointer happened to be over when the panel last answered. Empty for a row
// that offers no affordance, matching DiffLineCommentAction's own reason for
// rendering nothing there: a hunk header line, or a directory with no tenant to
// anchor a review comment to.
function lineRevealKey(envKey: string, tenant: string, filePath: string, line: DiffLine): string {
  if (tenant === '' || line.kind === 'meta' || line.newLine === undefined) {
    return '';
  }
  return `${envKey}|${filePath}:${String(line.newLine)}`;
}

// DiffLineRow renders one line of a hunk. It is a component rather than an
// inline map body because it publishes the reveal key below and reads
// `revealedLine` back, and both the shared function-size and complexity budgets
// (eslint.config.mjs) put that past what the hunk body may hold.
//
// `data-reveal-key` is how the panel finds the line under the pointer when the
// reader moves it, without re-deriving anything from the DOM: it is the same
// value the panel stores, so a moved pointer and a stored reveal can never
// disagree. It is rendered on every row, empty included, so that a row with no
// affordance is still a hit -- moving onto one has to end the reader's reveal,
// not fall through to the row above it.
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
