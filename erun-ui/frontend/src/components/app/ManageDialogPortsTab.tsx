import * as React from 'react';

import type { AppState } from '@/app/state';
import { ReadonlyField } from '@/components/app/ManageDialog.fields';
import { portRangeValue } from '@/components/app/ManageDialog.helpers';
import { cn } from '@/lib/utils';
import type { UIPortStatus } from '@/types';

type ManageDialog = AppState['manageDialog'];

export function PortsTab({ dialog }: { dialog: ManageDialog }): React.ReactElement {
  const config = dialog.config;
  return (
    <>
      <ReadonlyField
        id="environment-config-localportrange"
        label="Assigned local port range"
        value={portRangeValue(config.localPorts.rangeStart, config.localPorts.rangeEnd)}
      />
      <PortStatusTable
        rows={[
          {
            key: 'mcp',
            service: 'AI agent connection',
            port: config.localPorts.mcp,
            status: config.localPorts.mcpStatus,
          },
          {
            key: 'api',
            service: 'Environment API',
            port: config.localPorts.api,
            status: config.localPorts.apiStatus,
          },
          {
            key: 'ssh',
            service: 'SSH access',
            port: config.localPorts.ssh,
            status: config.localPorts.sshStatus,
          },
          {
            key: 'contribute-app',
            service: 'Contribute app preview',
            port: config.localPorts.contributeApp,
            status: config.localPorts.contributeAppStatus,
          },
        ]}
      />
    </>
  );
}

function PortStatusTable({
  rows,
}: {
  rows: { key: string; service: string; port: number; status: UIPortStatus }[];
}): React.ReactElement {
  return (
    <div className="grid gap-2">
      <div className="text-sm font-medium leading-none">Local ports</div>
      <div className="overflow-hidden rounded-[var(--radius)] border border-border bg-muted/35 text-xs leading-[1.3]">
        <div className="grid grid-cols-[auto_minmax(0,1fr)_auto] gap-3 border-b border-border px-3 py-2 text-[11px] font-semibold uppercase leading-[1.2] text-muted-foreground">
          <div>Port</div>
          <div>Service</div>
          <div>Status</div>
        </div>
        {rows.map((row) => (
          <div
            key={row.key}
            className="grid min-h-8 grid-cols-[auto_minmax(0,1fr)_auto] items-center gap-3 border-b border-border px-3 py-1 last:border-b-0"
          >
            <div className="font-mono text-xs text-foreground">
              {row.port > 0 ? row.port : 'Not configured'}
            </div>
            <div className="text-foreground">{row.service}</div>
            <PortAvailability status={row.status} />
          </div>
        ))}
      </div>
    </div>
  );
}

function PortAvailability({ status }: { status: UIPortStatus }): React.ReactElement {
  const available = status.available;
  const label = available ? 'Available' : 'Unavailable';
  return (
    <span
      className={cn(
        'inline-flex items-center gap-1.5 text-xs font-medium',
        available ? 'text-green-700 dark:text-green-400' : 'text-destructive',
      )}
    >
      <span
        className={cn('size-2 rounded-full', available ? 'bg-green-600' : 'bg-destructive')}
        aria-hidden="true"
      />
      {label}
    </span>
  );
}
