import { cn, EmptyState } from 'erun-kit';
import * as React from 'react';

import type { AppState } from '@/app/state';
import {
  formatDashboardDate,
  middleEllipsis,
  relativeDashboardDate,
  tenantDashboardPanel,
} from '@/app/tenantDashboardPanels';
import { InlineAlert } from '@/components/app/InlineAlert';

export type TenantDashboardData = AppState['tenantDashboard']['data'];

// PanelBody renders one panel's three distinguishable outcomes: it failed,
// there is nothing in it, or here it is. "You may not read this" never
// reaches here — a restricted panel has no tab to open. Shared by every
// TenantDashboardPanels* file rather than duplicated per panel, and kept in
// this message-focused module (not TenantDashboardPanels.tsx) so the panel
// files can both depend on it without importing each other.
export function PanelBody({
  data,
  tab,
  empty,
  children,
}: {
  data: TenantDashboardData;
  tab: 'users' | 'reviews' | 'queue' | 'builds' | 'audit' | 'requests';
  empty: React.ReactElement;
  children: React.ReactNode;
}): React.ReactElement {
  const panel = tenantDashboardPanel(data, tab);
  if (panel?.restricted) {
    return (
      <div className="mt-4">
        <EmptyState
          heading="You do not have access to this panel"
          body={`It needs ${panel.restricted}. Ask an administrator for access.`}
        />
      </div>
    );
  }
  if (panel?.error) {
    return (
      <div className="mt-4">
        <InlineAlert>{panel.error}</InlineAlert>
      </div>
    );
  }
  if (!children) {
    return <div className="mt-4">{empty}</div>;
  }
  return <>{children}</>;
}

// DashboardMessage is the one case an inline dashboard message isn't a
// failure or a permission state: a transient loading line. Failures route
// through InlineAlert instead (see PanelBody and GenericLoadFailure).
export function DashboardMessage({
  message,
  icon,
}: {
  message: string;
  icon?: React.ReactElement;
}): React.ReactElement {
  return (
    <div
      role="status"
      className="mt-4 flex items-center gap-2 rounded-[var(--radius)] border border-border px-3 py-2.5 text-sm text-muted-foreground"
    >
      {icon}
      <span>{message}</span>
    </div>
  );
}

export function DataTable({
  headers,
  children,
  minWidthClassName,
  columnWidths,
}: {
  headers: string[];
  children: React.ReactNode;
  // minWidthClassName floors a wide table (many columns, e.g. the reviews
  // table's Review/Status/Author/Target/Source/Updated/Threads) so a narrow
  // viewport scrolls the table horizontally (the panel around it is already
  // overflow-auto) instead of table-fixed shrinking every column past
  // readability — a status badge missing its last letter is unreadable, not
  // truncated. Omitted for narrower tables (Users, Builds, Audit), which fit
  // without it.
  minWidthClassName?: string;
  // columnWidths sets each header's width class, same length/order as
  // headers; a header left without one shares the width table-fixed leaves
  // over once the sized columns are subtracted. Lets a table give most of its
  // width to one dominant column (the reviews table's Review cell) instead of
  // table-fixed's default equal split.
  columnWidths?: string[];
}): React.ReactElement {
  return (
    <table className={`mt-4 w-full table-fixed border-collapse text-sm ${minWidthClassName ?? ''}`}>
      <thead>
        <tr className="border-b border-border text-left text-xs font-medium uppercase text-muted-foreground">
          {headers.map((header, index) => (
            <th key={header} className={cn('px-2 py-2', columnWidths?.[index])}>
              {header}
            </th>
          ))}
        </tr>
      </thead>
      <tbody className="divide-y divide-border">{children}</tbody>
    </table>
  );
}

// RelativeTime renders a scannable relative phrase ("2 days ago") with the
// exact timestamp one hover away — nobody scans a column of absolute
// timestamps, but the exact moment still matters and must not be lost (#1378).
export function RelativeTime({
  value,
  className,
}: {
  value: string | undefined;
  className?: string;
}): React.ReactElement {
  return (
    <span className={className} title={formatDashboardDate(value)}>
      {relativeDashboardDate(value)}
    </span>
  );
}

// BranchArrow is the review object's "where it's headed" line — source →
// target — shared by the reviews list row and the review detail dialog's
// header (#1378) so the two read as one treatment. Each side middle-ellipses
// on its own (keeping both the identifying prefix and suffix, unlike a
// trailing "…") with the full value one hover away, so a long branch name
// never pushes the arrow off-row or wraps the header into several lines.
export function BranchArrow({
  source,
  target,
  className,
}: {
  source: string;
  target: string;
  className?: string;
}): React.ReactElement {
  return (
    <span className={cn('inline-flex min-w-0 items-center gap-1.5', className)}>
      <span className="min-w-0 truncate" title={source}>
        {middleEllipsis(source)}
      </span>
      <span aria-hidden="true" className="flex-none text-muted-foreground/70">
        →
      </span>
      <span className="min-w-0 truncate" title={target}>
        {middleEllipsis(target)}
      </span>
    </span>
  );
}

export function DataCell({
  children,
  strong,
}: {
  children: React.ReactNode;
  strong?: boolean;
}): React.ReactElement {
  const isEmpty =
    children === null || children === undefined || children === false || children === '';
  return (
    <td className={`truncate px-2 py-2.5 ${strong ? 'font-medium' : 'text-muted-foreground'}`}>
      {isEmpty ? '-' : children}
    </td>
  );
}
