import * as React from 'react';
import { AlertCircle, CheckCircle2, Cloud, Copy, Folder, FolderOpen, LoaderCircle, LogIn, LogOut, MoreHorizontal, Plus, Settings, UserCircle2 } from 'lucide-react';

import { useController } from '@/app/ControllerContext';
import { readError } from '@/app/errors';
import { useAppSelector } from '@/app/hooks';
import type { AppState } from '@/app/state';
import { Button } from '@/components/ui/button';
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover';
import { cn } from '@/lib/utils';
import type { UICloudProviderStatus, UISelection, UITenant } from '@/types';
import { EmptyState } from './EmptyState';
import { IconTooltip } from './IconTooltip';
import { cloudProviderStatusTone } from './StatusBadge';

export function Sidebar(): React.ReactElement {
  const controller = useController();
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
        <span className="text-xs leading-[1.2] font-semibold tracking-normal text-muted-foreground uppercase">Environments</span>
        <div className="flex items-center gap-1">
          <IconTooltip label="Open ERun settings">
            <Button
              className="size-[26px] flex-none text-muted-foreground hover:bg-sidebar-accent hover:text-sidebar-accent-foreground [&_svg]:size-4"
              type="button"
              variant="ghost"
              size="icon-xs"
              aria-label="Open ERun settings"
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
        {tenants.length === 0 ? (
          <div className="px-2 py-2">
            <EmptyState
              icon={<Plus />}
              heading="No environments yet"
              body="Initialize a remote environment to start working. You can also import an existing one from your kubeconfig."
              action={
                <Button type="button" size="sm" onClick={() => controller.openInitializeDialog()}>
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

// pendingForTenant returns the optimistic selection that is being
// created right now, when the matching env is not yet in
// state.tenants. The sidebar renders a placeholder row for it so
// Nielsen #1 (visibility of system status) holds during the
// ~1–2 min init runs. The placeholder disappears once
// reloadStateAfterEnvironmentChange picks up the new env, or when
// `environment-init-failed` reverts state.selected.
function pendingForTenant(tenants: AppState['tenants'], selected: AppState['selected'], tenantName: string): UISelection | null {
  if (!selected || selected.tenant !== tenantName) {
    return null;
  }
  const tenant = tenants.find((entry) => entry.name === selected.tenant);
  if (!tenant) {
    return null;
  }
  if (tenant.environments.some((env) => env.name === selected.environment)) {
    return null;
  }
  return selected;
}

function PrimaryCloudAliasControl(): React.ReactElement | null {
  const controller = useController();
  const tenants = useAppSelector((s) => s.tenants.tenants);
  const cloudProviders = useAppSelector((s) => s.tenants.cloudProviders);
  const selected = useAppSelector((s) => s.selection.selected);
  const dashboardTenant = useAppSelector((s) => s.tenantDashboard.tenant);
  const sidebarBusy = useAppSelector((s) => s.sidebar.sidebarCloudAliasBusy);
  const sidebarAction = useAppSelector((s) => s.sidebar.sidebarCloudAliasAction);
  const view = primaryCloudAliasView({
    tenants,
    cloudProviders,
    selected,
    dashboardTenant,
    sidebarBusy,
    sidebarAction,
  });
  if (!view) {
    return null;
  }

  const triggerTone = cloudProviderStatusTone(view.provider.status);
  return (
    <Popover>
      <PopoverTrigger asChild>
        <button
          type="button"
          className="mt-3 mr-1 flex min-h-10 min-w-0 items-center gap-2 rounded-md border border-sidebar-border bg-background/88 px-3 py-2 text-left text-sm text-foreground shadow-sm hover:bg-sidebar-accent hover:text-sidebar-accent-foreground focus-visible:ring-2 focus-visible:ring-ring focus-visible:outline-none"
          aria-label={`${view.provider.alias} cloud status`}
        >
          <CloudAliasTriggerIcon tone={triggerTone} />
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
            <>
              <Button type="button" variant="ghost" size="sm" className="justify-start" disabled={view.busy} onClick={() => void controller.getPrimaryCloudProviderBearerToken(view.provider.alias)}>
                {view.bearerBusy ? <LoaderCircle className="animate-spin" aria-hidden="true" /> : <Copy aria-hidden="true" />}
                {view.bearerBusy ? 'Copying token...' : 'Get bearer token'}
              </Button>
              <Button type="button" variant="ghost" size="sm" className="justify-start" disabled={view.busy} onClick={() => void controller.logoutPrimaryCloudProvider(view.provider.alias)}>
                {view.logoutBusy ? <LoaderCircle className="animate-spin" aria-hidden="true" /> : <LogOut aria-hidden="true" />}
                {view.logoutBusy ? 'Logging out...' : 'Log out'}
              </Button>
            </>
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
  bearerBusy: boolean;
}

interface PrimaryCloudAliasInputs {
  tenants: AppState['tenants'];
  cloudProviders: AppState['cloudProviders'];
  selected: AppState['selected'];
  dashboardTenant: string;
  sidebarBusy: boolean;
  sidebarAction: AppState['sidebarCloudAliasAction'];
}

function primaryCloudAliasView(input: PrimaryCloudAliasInputs): PrimaryCloudAliasView | null {
  const alias = primaryCloudAliasFor(input);
  if (!alias) {
    return null;
  }
  const provider = input.cloudProviders.find((candidate) => candidate.alias === alias) || { alias, provider: '', status: 'unknown' };
  const busy = input.sidebarBusy;
  return {
    provider,
    active: provider.status.trim() === 'active',
    busy,
    loginBusy: busy && input.sidebarAction === 'login',
    logoutBusy: busy && input.sidebarAction === 'logout',
    bearerBusy: busy && input.sidebarAction === 'bearer',
  };
}

function primaryCloudAliasFor(input: PrimaryCloudAliasInputs): string | undefined {
  const tenantName = input.dashboardTenant || input.selected?.tenant || '';
  return input.tenants.find((candidate) => candidate.name === tenantName)?.primaryCloudProviderAlias?.trim();
}

function CloudAliasPopoverRow({ icon, label, muted }: { icon: React.ReactElement<{ className?: string; 'aria-hidden'?: boolean }>; label: string; muted?: boolean }): React.ReactElement {
  return (
    <div className={cn('flex min-w-0 items-center gap-2 rounded-sm px-2 py-1.5 text-sm', muted && 'text-muted-foreground')}>
      {React.cloneElement(icon, { className: 'size-4 shrink-0', 'aria-hidden': true })}
      <span className="truncate">{label}</span>
    </div>
  );
}

function CloudAliasStatus({ provider }: { provider: UICloudProviderStatus }): React.ReactElement {
  const tone = cloudProviderStatusTone(provider.status);
  return (
    <div className="flex min-w-0 items-center gap-2 rounded-sm px-2 py-1.5 text-sm">
      <CloudAliasStatusIcon tone={tone} />
      <span className="min-w-0 flex-1 truncate">{statusLabel(provider.status)}</span>
    </div>
  );
}

function CloudAliasTriggerIcon({ tone }: { tone: ReturnType<typeof cloudProviderStatusTone> }): React.ReactElement {
  if (tone === 'success') {
    return <CheckCircle2 className="size-4 shrink-0 text-green-700 dark:text-green-400" aria-hidden="true" />;
  }
  if (tone === 'destructive') {
    return <AlertCircle className="size-4 shrink-0 text-destructive" aria-hidden="true" />;
  }
  return <UserCircle2 className="size-4 shrink-0 text-muted-foreground" aria-hidden="true" />;
}

function CloudAliasStatusIcon({ tone }: { tone: ReturnType<typeof cloudProviderStatusTone> }): React.ReactElement {
  if (tone === 'success') {
    return <CheckCircle2 className="size-4 shrink-0 text-green-700 dark:text-green-400" aria-hidden="true" />;
  }
  if (tone === 'destructive') {
    return <AlertCircle className="size-4 shrink-0 text-destructive" aria-hidden="true" />;
  }
  return <Cloud className="size-4 shrink-0 text-muted-foreground" aria-hidden="true" />;
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
          active && 'bg-primary text-primary-foreground shadow-sm hover:bg-primary hover:text-primary-foreground',
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

function TenantToggleButton({ tenantName, collapsed, active }: { tenantName: string; collapsed: boolean; active: boolean }): React.ReactElement {
  const controller = useController();
  return (
    <IconTooltip label={collapsed ? 'Expand tenant' : 'Collapse tenant'}>
      <Button
        type="button"
        className={cn(
          'size-[26px] flex-none text-current hover:bg-[color-mix(in_oklch,currentColor_12%,transparent)] hover:text-current [&_svg]:size-[18px]',
          !active && 'text-muted-foreground hover:bg-sidebar-accent hover:text-sidebar-accent-foreground',
        )}
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

function TenantSelectButton({ tenantName, active, related }: { tenantName: string; active: boolean; related: boolean }): React.ReactElement {
  const controller = useController();
  return (
    <button
      className={cn(
        'flex min-w-0 flex-1 cursor-pointer items-center border-0 bg-transparent py-[4px] pr-3 pl-2 pb-1.5 text-left text-[15px] leading-[1.25] tracking-normal text-inherit disabled:cursor-default disabled:opacity-50',
        related ? 'font-medium' : 'font-normal',
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

function TenantManageButton({ tenantName, active }: { tenantName: string; active: boolean }): React.ReactElement {
  const controller = useController();
  return (
    <IconTooltip label="Edit tenant settings">
      <Button
        type="button"
        className={cn(
          'pointer-events-none size-[26px] flex-none cursor-pointer text-current opacity-0 transition-[opacity,background-color,color] duration-150 hover:bg-[color-mix(in_oklch,currentColor_12%,transparent)] hover:text-current group-hover/tenant:pointer-events-auto group-hover/tenant:opacity-100 group-focus-within/tenant:pointer-events-auto group-focus-within/tenant:opacity-100 [&_svg]:size-4',
          !active && 'text-muted-foreground hover:bg-sidebar-accent hover:text-sidebar-accent-foreground',
          active && 'pointer-events-auto opacity-100',
        )}
        variant="ghost"
        size="icon"
        aria-label={`Edit ${tenantName} settings`}
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
  tenantName,
  environmentName,
}: {
  tenantName: string;
  environmentName: string;
}): React.ReactElement {
  const controller = useController();
  const selectedSelection = useAppSelector((state) => state.selection.selected);
  const tenants = useAppSelector((state) => state.tenants.tenants);
  const terminalBusy = useAppSelector((state) => state.terminalStatus.terminalBusy);
  const selected = selectedSelection?.tenant === tenantName && selectedSelection?.environment === environmentName;
  const selection = { tenant: tenantName, environment: environmentName };
  const busy = terminalBusy === true && selectedSelection?.tenant === tenantName && selectedSelection.environment === environmentName;
  const environment = tenants
    .find((tenant) => tenant.name === tenantName)
    ?.environments.find((env) => env.name === environmentName);
  const isLocal = environment?.remote === false;

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
        title={`${tenantName} / ${environmentName}${isLocal ? ' (local)' : ''}`}
        aria-current={selected ? 'page' : undefined}
        onClick={() => {
          void controller.openSelection(selection).catch((error: unknown) => {
            controller.showTerminalMessage(readError(error));
          });
        }}
      >
        <span className="min-w-0 truncate">{environmentName}</span>
        {isLocal && (
          <span
            className={cn(
              'flex-none rounded-[calc(var(--radius)-4px)] border px-1 py-px text-[10px] font-medium uppercase leading-none tracking-wide',
              selected
                ? 'border-primary-foreground/40 text-primary-foreground/85'
                : 'border-border text-muted-foreground',
            )}
            aria-label="Local environment"
          >
            Local
          </span>
        )}
        {busy && <LoaderCircle className="size-3.5 flex-none animate-spin text-current opacity-75" aria-hidden="true" />}
      </button>
      <IconTooltip label="Edit environment settings">
        <Button
          type="button"
          className={cn(
            'pointer-events-none size-[26px] flex-none cursor-pointer border-0 bg-transparent text-current opacity-0 transition-[opacity,background-color,color] duration-150 hover:bg-[color-mix(in_oklch,currentColor_12%,transparent)] hover:text-current group-hover:pointer-events-auto group-hover:opacity-100 group-focus-within:pointer-events-auto group-focus-within:opacity-100 [&_svg]:size-4',
            selected && 'pointer-events-auto opacity-100',
          )}
          variant="ghost"
          size="icon"
          aria-label={`Edit ${tenantName} / ${environmentName} settings`}
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

// PendingEnvironmentRow renders an optimistic, non-interactive
// placeholder row for an environment that is currently being created
// by `erun init`. It exists to satisfy Nielsen #1 (visibility of
// system status) for the ~1–2 min init runs: without it,
// state.selected is set but produces no visible affordance because
// the env is not in state.tenants yet. Italic name + "Creating"
// badge + spinner + aria-busy communicate the in-flight state
// without inviting interaction.
function PendingEnvironmentRow({ tenantName, environmentName }: { tenantName: string; environmentName: string }): React.ReactElement {
  return (
    <div
      className="group relative mr-1 ml-1 flex h-8 items-center rounded-md pr-1.5 text-muted-foreground"
      aria-busy="true"
      aria-live="polite"
      aria-label={`Creating ${tenantName} / ${environmentName}`}
    >
      <div className="flex h-8 min-w-0 flex-1 items-center gap-1.5 py-0 pr-2 pl-10 text-left text-sm leading-[1.2]">
        <span className="min-w-0 truncate italic">{environmentName}</span>
        <span
          className="flex-none rounded-[calc(var(--radius)-4px)] border border-border px-1 py-px text-[10px] font-medium uppercase leading-none tracking-wide text-muted-foreground"
          aria-hidden="true"
        >
          Creating
        </span>
        <LoaderCircle className="size-3.5 flex-none animate-spin text-current opacity-75" aria-hidden="true" />
      </div>
    </div>
  );
}
