import { Plus, Settings } from 'lucide-react';
import * as React from 'react';

import { openInitializeDialog } from '@/app/environmentDialogThunks';
import { openGlobalConfigDialog } from '@/app/globalConfigThunks';
import { useAppDispatch, useAppSelector } from '@/app/hooks';
import { EmptyState } from '@/components/app/EmptyState';
import { IconTooltip } from '@/components/app/IconTooltip';
import { pendingForTenant } from '@/components/app/Sidebar.helpers';
import { PrimaryCloudAliasControl } from '@/components/app/Sidebar.PrimaryCloudAliasControl';
import { TenantGroup } from '@/components/app/Sidebar.TenantGroup';
import { Button } from '@/components/ui/button';
import { cn } from '@/lib/utils';

export function Sidebar(): React.ReactElement {
  const dispatch = useAppDispatch();
  const sidebarHidden = useAppSelector((state) => state.layout.sidebarHidden);
  const tenants = useAppSelector((state) => state.tenants.tenants);
  const selected = useAppSelector((state) => state.selection.selected);
  return (
    <aside
      className={cn(
        'box-border flex min-h-0 flex-col overflow-hidden border-r border-sidebar-border bg-sidebar',
        sidebarHidden ? 'px-0 py-6 pb-4' : 'py-6 pr-2 pb-4 pl-3 max-[980px]:pl-2.5',
      )}
    >
      <div className="flex items-center justify-between gap-2 pr-1.5 pb-2.5 pl-3.5">
        <span className="text-xs leading-[1.2] font-semibold tracking-normal text-muted-foreground uppercase">
          Environments
        </span>
        <div className="flex items-center gap-1">
          <IconTooltip label="Open ERun settings">
            <Button
              className="size-[26px] flex-none text-muted-foreground hover:bg-sidebar-accent hover:text-sidebar-accent-foreground [&_svg]:size-4"
              type="button"
              variant="ghost"
              size="icon-xs"
              aria-label="Open ERun settings"
              onClick={() => {
                dispatch(openGlobalConfigDialog());
              }}
            >
              <Settings />
            </Button>
          </IconTooltip>
          <IconTooltip label="Initialize new remote environment">
            <Button
              className="size-[26px] flex-none text-muted-foreground hover:bg-sidebar-accent hover:text-sidebar-accent-foreground [&_svg]:size-4"
              type="button"
              variant="ghost"
              size="icon-xs"
              aria-label="Initialize new remote environment"
              onClick={() => {
                dispatch(openInitializeDialog());
              }}
            >
              <Plus />
            </Button>
          </IconTooltip>
        </div>
      </div>
      <div className="min-h-0 flex-1 overflow-auto pr-1">
        {tenants.length === 0 ? (
          <div className="px-2 py-2">
            <EmptyState
              icon={<Plus />}
              heading="No environments yet"
              body="Initialize a remote environment to start working. You can also import an existing one from your kubeconfig."
              action={
                <Button
                  type="button"
                  size="sm"
                  onClick={() => {
                    dispatch(openInitializeDialog());
                  }}
                >
                  <Plus aria-hidden="true" />
                  Initialize environment
                </Button>
              }
            />
          </div>
        ) : (
          tenants.map((tenant, index) => (
            <TenantGroup
              key={tenant.name}
              tenant={tenant}
              spaced={index > 0}
              pending={pendingForTenant(tenants, selected, tenant.name)}
            />
          ))
        )}
      </div>
      <PrimaryCloudAliasControl />
    </aside>
  );
}
