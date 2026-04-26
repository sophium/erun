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
    <aside
      className={cn(
        'box-border flex min-h-0 flex-col overflow-hidden border-r border-sidebar-border bg-sidebar',
        state.sidebarHidden ? 'px-0 py-6 pb-4' : 'py-6 pr-2 pb-4 pl-3 max-[980px]:pl-2.5',
      )}
    >
      <div className="flex items-center justify-between gap-2 px-3.5 pb-2.5">
        <span className="text-xs leading-[1.2] font-semibold tracking-normal text-muted-foreground uppercase">Environments</span>
        <IconTooltip label="Initialize new remote environment">
          <Button
            className="size-[26px] flex-none border-0 bg-transparent text-muted-foreground hover:bg-accent hover:text-accent-foreground [&_svg]:size-[19px]"
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
      <div className="min-h-0 overflow-auto pr-1">
        {state.tenants.length === 0 ? (
          <div className="px-3.5 py-[18px] text-[13px] font-medium text-muted-foreground">No environments</div>
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
    <div className={cn('flex flex-col', spaced && 'mt-2.5')}>
      <button
        className="flex cursor-pointer items-center gap-[9px] border-0 bg-transparent px-3 py-[4px] pb-1.5 text-left text-[15px] leading-[1.25] font-medium tracking-normal text-muted-foreground hover:text-foreground"
        type="button"
        onClick={() => controller.toggleTenant(tenant.name)}
      >
        {collapsed ? (
          <Folder className="size-[18px] flex-none" aria-hidden="true" />
        ) : (
          <FolderOpen className="size-[18px] flex-none" aria-hidden="true" />
        )}
        <span>{tenant.name}</span>
      </button>
      {!collapsed && (
        <div className="flex flex-col gap-0 pt-0">
          {tenant.environments.map((environment) => {
            const selected = state.selected?.tenant === tenant.name && state.selected?.environment === environment.name;
            const selection = { tenant: tenant.name, environment: environment.name };

            return (
              <div
                key={environment.name}
                className={cn(
                  'group relative mr-0 ml-0.5 flex h-[34px] items-center rounded-[var(--radius)] pr-1.5 text-foreground hover:bg-accent',
                  selected && 'bg-primary text-primary-foreground hover:bg-primary',
                )}
              >
                <button
                  type="button"
                  className="h-[34px] min-w-0 flex-1 cursor-pointer truncate border-0 bg-transparent py-0 pr-2 pl-[42px] text-left text-sm leading-[1.2] font-normal tracking-normal text-inherit"
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
                    className={cn(
                      'pointer-events-none size-[26px] flex-none cursor-pointer border-0 bg-transparent text-current opacity-0 transition-[opacity,background-color,color] duration-150 hover:bg-[color-mix(in_oklch,currentColor_12%,transparent)] hover:text-current group-hover:pointer-events-auto group-hover:opacity-100 group-focus-within:pointer-events-auto group-focus-within:opacity-100 [&_svg]:size-4',
                      selected && 'pointer-events-auto opacity-100',
                    )}
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
