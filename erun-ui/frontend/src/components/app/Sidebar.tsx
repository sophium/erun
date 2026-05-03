import * as React from 'react';
import { CheckCircle2, Cloud, Folder, FolderOpen, LoaderCircle, LogIn, LogOut, MoreHorizontal, Plus, Settings, UserCircle2 } from 'lucide-react';

import type { ERunUIController } from '@/app/ERunUIController';
import { readError } from '@/app/errors';
import type { AppState } from '@/app/state';
import { Button } from '@/components/ui/button';
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover';
import { cn } from '@/lib/utils';
import type { UICloudProviderStatus, UITenant } from '@/types';
import { IconTooltip } from './IconTooltip';

export function Sidebar({ controller, state }: { controller: ERunUIController; state: AppState }): React.ReactElement {
  return (
    <aside
      className={cn(
        'box-border flex min-h-0 flex-col overflow-hidden border-r border-sidebar-border bg-sidebar',
        state.sidebarHidden ? 'px-0 py-6 pb-4' : 'py-6 pr-2 pb-4 pl-3 max-[980px]:pl-2.5',
      )}
    >
      <div className="flex items-center justify-between gap-2 pr-1.5 pb-2.5 pl-3.5">
        <span className="text-xs leading-[1.2] font-semibold tracking-normal text-muted-foreground uppercase">Environments</span>
        <div className="flex items-center gap-1">
          <IconTooltip label="Manage ERun config">
            <Button
              className="size-[26px] flex-none text-muted-foreground hover:bg-sidebar-accent hover:text-sidebar-accent-foreground [&_svg]:size-4"
              type="button"
              variant="ghost"
              size="icon-xs"
              aria-label="Manage ERun config"
              onClick={() => controller.openGlobalConfigDialog()}
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
              onClick={() => controller.openInitializeDialog()}
            >
              <Plus />
            </Button>
          </IconTooltip>
        </div>
      </div>
      <div className="min-h-0 flex-1 overflow-auto pr-1">
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
      <PrimaryCloudAliasControl controller={controller} state={state} />
    </aside>
  );
}

function PrimaryCloudAliasControl({ controller, state }: { controller: ERunUIController; state: AppState }): React.ReactElement | null {
  const view = primaryCloudAliasView(state);
  if (!view) {
    return null;
  }

  return (
    <Popover>
      <PopoverTrigger asChild>
        <button
          type="button"
          className="mt-3 mr-1 flex min-h-10 min-w-0 items-center gap-2 rounded-md border border-sidebar-border bg-background/88 px-3 py-2 text-left text-sm text-foreground shadow-sm hover:bg-sidebar-accent hover:text-sidebar-accent-foreground focus-visible:ring-2 focus-visible:ring-ring focus-visible:outline-none"
          aria-label={`${view.provider.alias} cloud status`}
        >
          {view.active ? <CheckCircle2 className="size-4 shrink-0 text-green-700 dark:text-green-400" aria-hidden="true" /> : <UserCircle2 className="size-4 shrink-0 text-muted-foreground" aria-hidden="true" />}
          <span className="min-w-0 flex-1 truncate">{cloudProviderIdentity(view.provider)}</span>
        </button>
      </PopoverTrigger>
      <PopoverContent className="w-[min(360px,calc(var(--sidebar-width)-24px))] p-2" side="top" align="start">
        <div className="grid gap-1">
          <CloudAliasPopoverRow icon={<UserCircle2 />} label={cloudProviderIdentity(view.provider)} muted />
          <CloudAliasPopoverRow icon={<Cloud />} label={view.provider.alias} muted />
          <div className="my-1 border-t border-border" />
          <CloudAliasStatus provider={view.provider} />
          {view.active ? (
            <Button type="button" variant="ghost" size="sm" className="justify-start" disabled={view.busy} onClick={() => void controller.logoutPrimaryCloudProvider(view.provider.alias)}>
              {view.logoutBusy ? <LoaderCircle className="animate-spin" aria-hidden="true" /> : <LogOut aria-hidden="true" />}
              {view.logoutBusy ? 'Logging out...' : 'Log out'}
            </Button>
          ) : (
            <Button type="button" variant="ghost" size="sm" className="justify-start" disabled={view.busy} onClick={() => void controller.loginPrimaryCloudProvider(view.provider.alias)}>
              {view.loginBusy ? <LoaderCircle className="animate-spin" aria-hidden="true" /> : <LogIn aria-hidden="true" />}
              {view.loginBusy ? 'Logging in...' : 'Log in'}
            </Button>
          )}
        </div>
      </PopoverContent>
    </Popover>
  );
}

interface PrimaryCloudAliasView {
  provider: UICloudProviderStatus;
  active: boolean;
  busy: boolean;
  loginBusy: boolean;
  logoutBusy: boolean;
}

function primaryCloudAliasView(state: AppState): PrimaryCloudAliasView | null {
  const tenantName = state.tenantDashboard.tenant || state.selected?.tenant || '';
  const tenant = state.tenants.find((candidate) => candidate.name === tenantName);
  const alias = tenant?.primaryCloudProviderAlias?.trim();
  if (!alias) {
    return null;
  }
  const provider = state.cloudProviders.find((candidate) => candidate.alias === alias) || { alias, provider: '', status: 'unknown' };
  const busy = state.sidebarCloudAliasBusy;
  return {
    provider,
    active: provider.status.trim() === 'active',
    busy,
    loginBusy: busy && state.sidebarCloudAliasAction === 'login',
    logoutBusy: busy && state.sidebarCloudAliasAction === 'logout',
  };
}

function CloudAliasPopoverRow({ icon, label, muted }: { icon: React.ReactElement; label: string; muted?: boolean }): React.ReactElement {
  return (
    <div className={cn('flex min-w-0 items-center gap-2 rounded-sm px-2 py-1.5 text-sm', muted && 'text-muted-foreground')}>
      {React.cloneElement(icon, { className: 'size-4 shrink-0', 'aria-hidden': true })}
      <span className="truncate">{label}</span>
    </div>
  );
}

function CloudAliasStatus({ provider }: { provider: UICloudProviderStatus }): React.ReactElement {
  const active = provider.status.trim() === 'active';
  return (
    <div className="flex min-w-0 items-center gap-2 rounded-sm px-2 py-1.5 text-sm">
      {active ? <CheckCircle2 className="size-4 shrink-0 text-green-700 dark:text-green-400" aria-hidden="true" /> : <Cloud className="size-4 shrink-0 text-muted-foreground" aria-hidden="true" />}
      <span className="min-w-0 flex-1 truncate">{active ? 'Connected' : statusLabel(provider.status)}</span>
    </div>
  );
}

function cloudProviderIdentity(provider: UICloudProviderStatus): string {
  return provider.username?.trim() || provider.alias;
}

function statusLabel(status: string): string {
  switch (status.trim()) {
    case 'expired':
      return 'Login expired';
    case 'not_configured':
      return 'Not configured';
    case 'active':
      return 'Connected';
    default:
      return 'Status unknown';
  }
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
  const active = state.tenantDashboard.tenant === tenant.name || state.selected?.tenant === tenant.name;

  return (
    <div className={cn('flex flex-col', spaced && 'mt-2.5')}>
      <div className="group/tenant flex items-center pr-1">
        <TenantToggleButton controller={controller} tenantName={tenant.name} collapsed={collapsed} />
        <TenantSelectButton controller={controller} tenantName={tenant.name} active={active} />
        <TenantManageButton controller={controller} tenantName={tenant.name} />
      </div>
      {!collapsed && (
        <div className="flex flex-col gap-0 pt-0">
          {tenant.environments.map((environment) => (
            <EnvironmentRow
              key={environment.name}
              controller={controller}
              state={state}
              tenantName={tenant.name}
              environmentName={environment.name}
            />
          ))}
        </div>
      )}
    </div>
  );
}

function TenantToggleButton({ controller, tenantName, collapsed }: { controller: ERunUIController; tenantName: string; collapsed: boolean }): React.ReactElement {
  return (
    <IconTooltip label={collapsed ? 'Expand tenant' : 'Collapse tenant'}>
      <Button
        type="button"
        className="ml-1 size-[26px] flex-none text-muted-foreground hover:bg-sidebar-accent hover:text-sidebar-accent-foreground [&_svg]:size-[18px]"
        variant="ghost"
        size="icon"
        aria-label={collapsed ? `Expand ${tenantName}` : `Collapse ${tenantName}`}
        aria-expanded={!collapsed}
        onClick={() => controller.toggleTenant(tenantName)}
      >
        {collapsed ? <Folder aria-hidden="true" /> : <FolderOpen aria-hidden="true" />}
      </Button>
    </IconTooltip>
  );
}

function TenantSelectButton({ controller, tenantName, active }: { controller: ERunUIController; tenantName: string; active: boolean }): React.ReactElement {
  return (
    <button
      className={cn(
        'flex min-w-0 flex-1 cursor-pointer items-center border-0 bg-transparent py-[4px] pr-3 pl-2 pb-1.5 text-left text-[15px] leading-[1.25] font-medium tracking-normal text-muted-foreground hover:text-foreground disabled:cursor-default disabled:opacity-50',
        active && 'text-foreground',
      )}
      type="button"
      title={tenantName}
      aria-current={active ? 'page' : undefined}
      onClick={() => controller.openTenantDashboard(tenantName)}
    >
      <span className="truncate">{tenantName}</span>
    </button>
  );
}

function TenantManageButton({ controller, tenantName }: { controller: ERunUIController; tenantName: string }): React.ReactElement {
  return (
    <IconTooltip label="Manage tenant">
      <Button
        type="button"
        className="pointer-events-none size-[26px] flex-none cursor-pointer text-muted-foreground opacity-0 transition-[opacity,background-color,color] duration-150 hover:bg-sidebar-accent hover:text-sidebar-accent-foreground group-hover/tenant:pointer-events-auto group-hover/tenant:opacity-100 group-focus-within/tenant:pointer-events-auto group-focus-within/tenant:opacity-100 [&_svg]:size-4"
        variant="ghost"
        size="icon"
        aria-label={`Manage ${tenantName}`}
        onClick={(event) => {
          event.stopPropagation();
          controller.openTenantDialog(tenantName);
        }}
      >
        <MoreHorizontal />
      </Button>
    </IconTooltip>
  );
}

function EnvironmentRow({
  controller,
  state,
  tenantName,
  environmentName,
}: {
  controller: ERunUIController;
  state: AppState;
  tenantName: string;
  environmentName: string;
}): React.ReactElement {
  const selected = !state.tenantDashboard.tenant && state.selected?.tenant === tenantName && state.selected?.environment === environmentName;
  const selection = { tenant: tenantName, environment: environmentName };
  const busy = environmentIsBusy(state, tenantName, environmentName);

  return (
    <div
      className={cn(
        'group relative mr-1 ml-1 flex h-8 items-center rounded-md pr-1.5 text-foreground hover:bg-sidebar-accent hover:text-sidebar-accent-foreground',
        selected && 'bg-primary text-primary-foreground shadow-sm hover:bg-primary hover:text-primary-foreground',
      )}
    >
      <button
        type="button"
        className={cn(
          'flex h-8 min-w-0 flex-1 cursor-pointer items-center gap-1.5 border-0 bg-transparent py-0 pr-2 pl-10 text-left text-sm leading-[1.2] tracking-normal text-inherit',
          selected ? 'font-medium' : 'font-normal',
        )}
        title={`${tenantName} / ${environmentName}`}
        aria-current={selected ? 'page' : undefined}
        onClick={() => {
          void controller.openSelection(selection).catch((error: unknown) => {
            controller.showTerminalMessage(readError(error));
          });
        }}
      >
        <span className="min-w-0 truncate">{environmentName}</span>
        {busy && <LoaderCircle className="size-3.5 flex-none animate-spin text-current opacity-75" aria-hidden="true" />}
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
          aria-label={`Manage ${tenantName} / ${environmentName}`}
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
}

function environmentIsBusy(state: AppState, tenant: string, environment: string): boolean {
  return state.terminalBusy === true && state.selected?.tenant === tenant && state.selected.environment === environment;
}
