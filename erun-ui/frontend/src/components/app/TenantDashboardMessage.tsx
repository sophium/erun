import { cn, EmptyState } from 'erun-kit';
import * as React from 'react';

import type { AppState } from '@/app/state';
import {
  formatDashboardDate,
  middleEllipsis,
  relativeDashboardDate,
  tenantDashboardPanel,
} from '@/app/tenantDashboardPanels';
import { AccessDeniedBody } from '@/components/app/AccessRemedyNote';
import { InlineAlert } from '@/components/app/InlineAlert';

export type TenantDashboardData = AppState['tenantDashboard']['data'];

// PanelBody renders one panel's failure, empty, or populated state. Lives here
// so panel files can share it without importing each other.
export function PanelBody({
  data,
  tab,
  empty,
  children,
}: {
  data: TenantDashboardData;
  tab: 'users' | 'reviews' | 'queue' | 'pipeline' | 'builds' | 'audit' | 'requests';
  empty: React.ReactElement;
  children: React.ReactNode;
}): React.ReactElement {
  const panel = tenantDashboardPanel(data, tab);
  if (panel?.restricted) {
    return (
      <div className="mt-4">
        <EmptyState
          heading="You do not have access to this panel"
          body={
            <AccessDeniedBody remedies={data?.accessRemedies} restricted={panel.restricted}>
              {`It needs ${panel.restricted}. Ask an administrator for access.`}
            </AccessDeniedBody>
          }
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

// DashboardMessage renders a transient loading line; failures go through InlineAlert.
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
  // Floors a wide table so a narrow viewport scrolls instead of crushing columns.
  minWidthClassName?: string;
  // Width class per header; unsized headers share the remainder.
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

// RelativeTime renders a relative phrase with the exact timestamp on hover.
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

// BranchArrow renders source → target, each side middle-ellipsed with the full value on hover.
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
