import * as React from 'react';
import { Folder, FolderOpen, FolderPlus, MoreHorizontal } from 'lucide-react';

import type { ERunUIController } from '@/app/ERunUIController';
import { readError } from '@/app/errors';
import type { AppState } from '@/app/state';
import { Button } from '@/components/ui/button';
import { cn } from '@/lib/utils';
import type { UITenant } from '@/types';
import { IconTooltip } from './IconTooltip';

export function Sidebar({ controller, state }: { controller: ERunUIController; state: AppState }): React.ReactElement {
  return (
    <aside className="sidebar">
      <div className="sidebar-header">
        <span className="sidebar-title">Environments</span>
        <IconTooltip label="Initialize new remote environment">
          <Button
            className="sidebar-icon-button"
            type="button"
            variant="ghost"
            size="icon"
            aria-label="Initialize new remote environment"
            onClick={() => controller.openInitializeDialog()}
          >
            <FolderPlus />
          </Button>
        </IconTooltip>
      </div>
      <div className="sidebar-list">
        {state.tenants.length === 0 ? (
          <div className="sidebar-empty">No environments</div>
        ) : (
          state.tenants.map((tenant, index) => (
            <TenantGroup
              key={tenant.name}
              controller={controller}
              state={state}
              tenant={tenant}
              spaced={index > 0}
            />
          ))
        )}
      </div>
    </aside>
  );
}

function TenantGroup({
  controller,
  state,
  tenant,
  spaced,
}: {
  controller: ERunUIController;
  state: AppState;
  tenant: UITenant;
  spaced: boolean;
}): React.ReactElement {
  const collapsed = state.collapsedTenants.has(tenant.name);

  return (
    <div className={cn('tenant-group', spaced && 'tenant-group-spaced')}>
      <button className="tenant-row" type="button" onClick={() => controller.toggleTenant(tenant.name)}>
        {collapsed ? (
          <Folder className="folder-icon" aria-hidden="true" />
        ) : (
          <FolderOpen className="folder-icon" aria-hidden="true" />
        )}
        <span>{tenant.name}</span>
      </button>
      {!collapsed && (
        <div className="environment-list">
          {tenant.environments.map((environment) => {
            const selected = state.selected?.tenant === tenant.name && state.selected?.environment === environment.name;
            const selection = { tenant: tenant.name, environment: environment.name };

            return (
              <div key={environment.name} className={cn('environment-row', selected && 'is-selected')}>
                <button
                  type="button"
                  className="environment-open"
                  onClick={() => {
                    void controller.openSelection(selection).catch((error: unknown) => {
                      controller.showTerminalMessage(readError(error));
                    });
                  }}
                >
                  {environment.name}
                </button>
                <IconTooltip label="Manage environment">
                  <Button
                    type="button"
                    className="environment-manage"
                    variant="ghost"
                    size="icon"
                    aria-label={`Manage ${tenant.name} / ${environment.name}`}
                    onClick={(event) => {
                      event.stopPropagation();
                      controller.openManageDialog(selection);
                    }}
                  >
                    <MoreHorizontal />
                  </Button>
                </IconTooltip>
              </div>
            );
          })}
        </div>
      )}
    </div>
  );
}
