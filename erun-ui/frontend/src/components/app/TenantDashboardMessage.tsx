import { EmptyState } from 'erun-kit';
import * as React from 'react';

import type { AppState } from '@/app/state';
import { tenantDashboardPanel } from '@/app/tenantDashboardPanels';

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
  tab: 'users' | 'reviews' | 'queue' | 'builds' | 'audit';
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
    return <DashboardMessage message={panel.error} destructive />;
  }
  if (!children) {
    return <div className="mt-4">{empty}</div>;
  }
  return <>{children}</>;
}

// DashboardMessage carries a status line or a failure for one dashboard surface.
// It is deliberately not input-shaped: an empty panel uses EmptyState instead,
// so nothing reads as a disabled field.
export function DashboardMessage({
  message,
  icon,
  destructive,
}: {
  message: string;
  icon?: React.ReactElement;
  destructive?: boolean;
}): React.ReactElement {
  return (
    <div
      className={`mt-4 flex items-center gap-2 rounded-[var(--radius)] border px-3 py-2.5 text-sm ${destructive ? 'border-destructive/35 text-destructive' : 'border-border text-muted-foreground'}`}
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
}): React.ReactElement {
  return (
    <table className={`mt-4 w-full table-fixed border-collapse text-sm ${minWidthClassName ?? ''}`}>
      <thead>
        <tr className="border-b border-border text-left text-xs font-medium uppercase text-muted-foreground">
          {headers.map((header) => (
            <th key={header} className="px-2 py-2">
              {header}
            </th>
          ))}
        </tr>
      </thead>
      <tbody className="divide-y divide-border">{children}</tbody>
    </table>
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
