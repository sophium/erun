import * as React from 'react';

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
}: {
  headers: string[];
  children: React.ReactNode;
}): React.ReactElement {
  return (
    <table className="mt-4 w-full table-fixed border-collapse text-sm">
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
