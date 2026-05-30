import { Folder, FolderOpen, MoreHorizontal } from 'lucide-react';
import * as React from 'react';

import { useAppDispatch, useAppSelector } from '@/app/hooks';
import { toggleTenantCollapsed } from '@/app/slices/sidebarSlice';
import { openTenantDashboard, openTenantDialog } from '@/app/tenantDialogThunks';
import { IconTooltip } from '@/components/app/IconTooltip';
import { EnvironmentRow, PendingEnvironmentRow } from '@/components/app/Sidebar.EnvironmentRow';
import { Button } from '@/components/ui/button';
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip';
import { cn } from '@/lib/utils';
import type { UISelection, UITenant } from '@/types';

export function TenantGroup({
  tenant,
  spaced,
  pending,
}: {
  tenant: UITenant;
  spaced: boolean;
  pending: UISelection | null;
}): React.ReactElement {
  const collapsedTenants = useAppSelector((state) => state.sidebar.collapsedTenants);
  const dashboardTenant = useAppSelector((state) => state.tenantDashboard.tenant);
  const selected = useAppSelector((state) => state.selection.selected);
  const collapsed = collapsedTenants.includes(tenant.name);
  const active = dashboardTenant === tenant.name;
  const related = active || selected?.tenant === tenant.name;

  return (
    <div className={cn('flex flex-col', spaced && 'mt-2.5')}>
      <div
        className={cn(
          'group/tenant mr-1 ml-1 flex h-8 items-center rounded-md pr-1 text-muted-foreground hover:bg-sidebar-accent hover:text-sidebar-accent-foreground',
          active &&
            'bg-primary text-primary-foreground shadow-sm hover:bg-primary hover:text-primary-foreground',
        )}
      >
        <TenantToggleButton tenantName={tenant.name} collapsed={collapsed} active={active} />
        <TenantSelectButton tenantName={tenant.name} active={active} related={related} />
        <TenantManageButton tenantName={tenant.name} active={active} />
      </div>
      {!collapsed && (
        <div className="flex flex-col gap-0 pt-0">
          {tenant.environments.map((environment) => (
            <EnvironmentRow
              key={environment.name}
              tenantName={tenant.name}
              environmentName={environment.name}
            />
          ))}
          {pending && (
            <PendingEnvironmentRow
              tenantName={pending.tenant}
              environmentName={pending.environment}
            />
          )}
        </div>
      )}
    </div>
  );
}

function TenantToggleButton({
  tenantName,
  collapsed,
  active,
}: {
  tenantName: string;
  collapsed: boolean;
  active: boolean;
}): React.ReactElement {
  const dispatch = useAppDispatch();
  return (
    <IconTooltip label={collapsed ? 'Expand tenant' : 'Collapse tenant'}>
      <Button
        type="button"
        className={cn(
          'size-[26px] flex-none text-current hover:bg-[color-mix(in_oklch,currentColor_12%,transparent)] hover:text-current [&_svg]:size-[18px]',
          !active &&
            'text-muted-foreground hover:bg-sidebar-accent hover:text-sidebar-accent-foreground',
        )}
        variant="ghost"
        size="icon"
        aria-label={collapsed ? `Expand ${tenantName}` : `Collapse ${tenantName}`}
        aria-expanded={!collapsed}
        onClick={() => dispatch(toggleTenantCollapsed(tenantName))}
      >
        {collapsed ? <Folder aria-hidden="true" /> : <FolderOpen aria-hidden="true" />}
      </Button>
    </IconTooltip>
  );
}

function TenantSelectButton({
  tenantName,
  active,
  related,
}: {
  tenantName: string;
  active: boolean;
  related: boolean;
}): React.ReactElement {
  const dispatch = useAppDispatch();
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <button
          className={cn(
            'flex min-w-0 flex-1 cursor-pointer items-center border-0 bg-transparent py-[4px] pr-3 pl-2 pb-1.5 text-left text-[15px] leading-[1.25] tracking-normal text-inherit disabled:cursor-default disabled:opacity-50',
            related ? 'font-medium' : 'font-normal',
          )}
          type="button"
          aria-label={`Open ${tenantName} dashboard`}
          aria-current={active ? 'page' : undefined}
          onClick={() => {
            dispatch(openTenantDashboard(tenantName));
          }}
        >
          <span className="truncate">{tenantName}</span>
        </button>
      </TooltipTrigger>
      <TooltipContent side="right">{tenantName}</TooltipContent>
    </Tooltip>
  );
}

function TenantManageButton({
  tenantName,
  active,
}: {
  tenantName: string;
  active: boolean;
}): React.ReactElement {
  const dispatch = useAppDispatch();
  return (
    <IconTooltip label="Edit tenant settings">
      <Button
        type="button"
        className={cn(
          'pointer-events-none size-[26px] flex-none cursor-pointer text-current opacity-0 transition-[opacity,background-color,color] duration-150 hover:bg-[color-mix(in_oklch,currentColor_12%,transparent)] hover:text-current group-hover/tenant:pointer-events-auto group-hover/tenant:opacity-100 group-focus-within/tenant:pointer-events-auto group-focus-within/tenant:opacity-100 [&_svg]:size-4',
          !active &&
            'text-muted-foreground hover:bg-sidebar-accent hover:text-sidebar-accent-foreground',
          active && 'pointer-events-auto opacity-100',
        )}
        variant="ghost"
        size="icon"
        aria-label={`Edit ${tenantName} settings`}
        onClick={(event) => {
          event.stopPropagation();
          dispatch(openTenantDialog(tenantName));
        }}
      >
        <MoreHorizontal />
      </Button>
    </IconTooltip>
  );
}
