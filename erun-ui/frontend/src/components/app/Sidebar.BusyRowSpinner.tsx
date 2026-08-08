import { LoaderCircle } from 'lucide-react';
import * as React from 'react';

// BusyRowSpinner is the shared "this row is working" indicator for sidebar
// rows. Shared by AI-orchestrator and environment rows so both report activity
// identically — an orchestrator driving its envs is working in exactly the
// sense an env's AI tab is, and a row that looks idle while it works is the
// worse failure. Independent of the status dot, which carries the row's
// condition rather than its activity. The caller owns the accessible label.
export function BusyRowSpinner({ label }: { label: string }): React.ReactElement {
  return (
    <LoaderCircle
      className="size-3.5 flex-none animate-spin text-current opacity-75"
      aria-label={label || undefined}
      aria-hidden={label ? undefined : true}
      role={label ? 'status' : undefined}
    />
  );
}
