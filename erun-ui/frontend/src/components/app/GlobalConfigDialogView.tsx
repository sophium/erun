import * as React from 'react';
import { CheckCircle2, Cloud, LoaderCircle, LogIn, Play, Plus, Power, RefreshCw, Save, Server } from 'lucide-react';

import type { ERunUIController } from '@/app/ERunUIController';
import { readError } from '@/app/errors';
import type { AppState } from '@/app/state';
import { Button } from '@/components/ui/button';
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';

const dialogErrorClassName =
  'rounded-[var(--radius)] border border-[color-mix(in_oklch,var(--destructive)_36%,transparent)] bg-[color-mix(in_oklch,var(--destructive)_8%,transparent)] px-[11px] py-[9px] text-[13px] leading-[1.35] text-destructive [overflow-wrap:anywhere]';

export function GlobalConfigDialogView({ controller, state }: { controller: ERunUIController; state: AppState }): React.ReactElement {
  const dialog = state.globalConfigDialog;
  const config = dialog.config;
  const tenantOptions = optionValues(state.tenants.map((tenant) => tenant.name), config.defaultTenant);
  const selectedCloudProvider = (config.cloudProviders || []).find((provider) => provider.alias === dialog.cloudContextDraft.cloudProviderAlias);
  const generatedCloudContextName = generatedContextName(selectedCloudProvider, dialog.cloudContextDraft.region, config.cloudContexts || []);

  return (
    <Dialog open={dialog.open} onOpenChange={(open) => !open && controller.closeGlobalConfigDialog()}>
      <DialogContent
        className="sm:max-w-xl"
        onCloseAutoFocus={(event) => {
          event.preventDefault();
          controller.focusTerminalSoon();
        }}
      >
        <form
          className="grid gap-4"
          onSubmit={(event) => {
            event.preventDefault();
            void controller.submitGlobalConfig().catch((error: unknown) => {
              controller.showTerminalMessage(readError(error));
            });
          }}
        >
          <DialogHeader>
            <DialogTitle>ERun settings</DialogTitle>
            <DialogDescription className="sr-only">Manage default tenant and cloud aliases.</DialogDescription>
          </DialogHeader>
          {dialog.configLoading ? (
            <div className="rounded-[var(--radius)] border border-dashed border-border px-3 py-2.5 text-[13px] leading-[1.35] text-muted-foreground">
              Loading config...
            </div>
          ) : (
            <div className="grid gap-3">
              <SelectField id="global-config-defaulttenant" label="Default tenant" value={config.defaultTenant} options={tenantOptions} disabled={dialog.busy || tenantOptions.length === 0} onChange={(defaultTenant) => controller.updateGlobalConfig({ defaultTenant })} />
              <div className="grid gap-2">
                <div className="flex items-center justify-between gap-2">
                  <Label>Cloud aliases</Label>
                  <div className="flex gap-1.5">
                    <Button type="button" variant="outline" size="sm" disabled={dialog.busy} onClick={() => void controller.startAWSCloudInit()}>
                      <Plus aria-hidden="true" />
                      AWS
                    </Button>
                    <Button type="button" variant="ghost" size="icon" disabled={dialog.busy} aria-label="Refresh cloud aliases" onClick={() => void controller.refreshCloudProviders()}>
                      <RefreshCw aria-hidden="true" />
                    </Button>
                  </div>
                </div>
                {(config.cloudProviders || []).length === 0 ? (
                  <div className="px-0.5 py-2 text-[13px] leading-[1.35] text-muted-foreground">No cloud aliases configured</div>
                ) : (
                  <div className="overflow-hidden rounded-[var(--radius)] border border-border">
                    {(config.cloudProviders || []).map((provider, index) => (
                      <div
                        key={provider.alias}
                        className="grid grid-cols-[minmax(0,1fr)_auto] items-center gap-3 border-border px-3 py-2.5 data-[border=true]:border-t"
                        data-border={index > 0}
                        data-cloud-alias={provider.alias}
                        data-cloud-status={provider.status}
                      >
                        <div className="grid min-w-0 gap-1">
                          <div className="flex min-w-0 items-center gap-2 text-sm font-medium">
                            <Cloud className="size-4 shrink-0 text-muted-foreground" aria-hidden="true" />
                            <span className="truncate">{provider.alias}</span>
                            <StatusBadge status={provider.status} />
                          </div>
                          <div className="truncate text-xs text-muted-foreground">
                            {cloudProviderSummary(provider)}
                            {provider.message ? ` - ${provider.message}` : ''}
                          </div>
                        </div>
                        <CloudAliasAction status={provider.status} busy={dialog.busy} onLogin={() => void controller.loginCloudProvider(provider.alias)} />
                      </div>
                    ))}
                  </div>
                )}
              </div>
              <div className="grid gap-2">
                <div className="flex items-center justify-between gap-2">
                  <Label>Cloud contexts</Label>
                  <Button type="button" variant="ghost" size="icon" disabled={dialog.busy} aria-label="Refresh cloud contexts" onClick={() => void controller.refreshCloudContexts()}>
                    <RefreshCw aria-hidden="true" />
                  </Button>
                </div>
                <div className="grid gap-2 rounded-[var(--radius)] border border-border p-3">
                  <div className="grid gap-2 sm:grid-cols-2">
                    <SelectInput
                      id="global-config-cloudcontext-provider"
                      label="Cloud provider"
                      value={dialog.cloudContextDraft.cloudProviderAlias}
                      options={(config.cloudProviders || []).map((provider) => provider.alias)}
                      emptyLabel="No cloud aliases"
                      disabled={dialog.busy || (config.cloudProviders || []).length === 0}
                      onChange={(cloudProviderAlias) => controller.updateCloudContextDraft({ cloudProviderAlias })}
                    />
                    <RegionSelectInput id="global-config-cloudcontext-region" value={dialog.cloudContextDraft.region} disabled={dialog.busy} onChange={(region) => controller.updateCloudContextDraft({ region })} />
                    <SelectInput
                      id="global-config-cloudcontext-instancetype"
                      label="Instance type"
                      value={dialog.cloudContextDraft.instanceType}
                      options={['c8gd.2xlarge', 't4g.xlarge']}
                      disabled={dialog.busy}
                      onChange={(instanceType) => controller.updateCloudContextDraft({ instanceType })}
                    />
                    <SelectInput
                      id="global-config-cloudcontext-disksize"
                      label="Disk size"
                      value={String(dialog.cloudContextDraft.diskSizeGb)}
                      options={['100', '200']}
                      disabled={dialog.busy}
                      onChange={(diskSizeGb) => controller.updateCloudContextDraft({ diskSizeGb: Number(diskSizeGb) })}
                    />
                  </div>
                  <div className="grid gap-2">
                    <Label htmlFor="global-config-cloudcontext-name">Context name</Label>
                    <div className="grid gap-2 sm:grid-cols-[minmax(0,1fr)_auto] sm:items-center">
                      <Input
                        id="global-config-cloudcontext-name"
                        value={dialog.cloudContextDraft.name}
                        disabled={dialog.busy}
                        placeholder="Generated when empty"
                        onChange={(event) => controller.updateCloudContextDraft({ name: event.target.value })}
                      />
                      <Button type="button" size="sm" disabled={dialog.busy || dialog.configLoading || !dialog.cloudContextDraft.cloudProviderAlias || !dialog.cloudContextDraft.region} onClick={() => void controller.initCloudContext()}>
                        {dialog.busy ? <LoaderCircle className="animate-spin" aria-hidden="true" /> : <Plus aria-hidden="true" />}
                        Init
                      </Button>
                    </div>
                    {generatedCloudContextName && !dialog.cloudContextDraft.name && (
                      <div className="px-0.5 text-xs leading-[1.35] text-muted-foreground [overflow-wrap:anywhere]">Generated: {generatedCloudContextName}</div>
                    )}
                  </div>
                </div>
                {(config.cloudContexts || []).length === 0 ? (
                  <div className="px-0.5 py-2 text-[13px] leading-[1.35] text-muted-foreground">No cloud contexts configured</div>
                ) : (
                  <div className="overflow-hidden rounded-[var(--radius)] border border-border">
                    {(config.cloudContexts || []).map((context, index) => (
                      <div
                        key={context.name}
                        className="grid grid-cols-[minmax(0,1fr)_auto] items-center gap-3 border-border px-3 py-2.5 data-[border=true]:border-t"
                        data-border={index > 0}
                        data-cloud-context={context.name}
                        data-cloud-context-status={context.status}
                      >
                        <div className="grid min-w-0 gap-1">
                          <div className="flex min-w-0 items-center gap-2 text-sm font-medium">
                            <Server className="size-4 shrink-0 text-muted-foreground" aria-hidden="true" />
                            <span className="truncate">{context.kubernetesContext || context.name}</span>
                            <StatusBadge status={context.status} />
                          </div>
                          <div className="truncate text-xs text-muted-foreground">
                            {cloudContextSummary(context)}
                            {context.message ? ` - ${context.message}` : ''}
                          </div>
                        </div>
                        <CloudContextAction status={context.status} busy={dialog.busy} onStart={() => void controller.startCloudContext(context.name)} onStop={() => void controller.stopCloudContext(context.name)} />
                      </div>
                    ))}
                  </div>
                )}
              </div>
            </div>
          )}
          {dialog.error && (
            <div className={dialogErrorClassName} role="alert">
              {dialog.error}
            </div>
          )}
          <DialogFooter>
            <Button type="button" variant="outline" size="sm" disabled={dialog.busy} onClick={() => controller.closeGlobalConfigDialog()}>
              Cancel
            </Button>
            <Button type="submit" size="sm" disabled={dialog.busy || dialog.configLoading}>
              {dialog.busy ? <LoaderCircle className="animate-spin" aria-hidden="true" /> : <Save aria-hidden="true" />}
              {dialog.busy ? 'Saving...' : 'Save settings'}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

function StatusBadge({ status }: { status: string }): React.ReactElement {
  const normalized = status.trim() || 'unknown';
  const className =
    normalized === 'active' || normalized === 'running'
      ? 'border-green-600/35 bg-green-600/10 text-green-700 dark:text-green-400'
      : normalized === 'expired' || normalized === 'not_configured'
        ? 'border-[color-mix(in_oklch,var(--destructive)_35%,var(--border))] bg-[color-mix(in_oklch,var(--destructive)_8%,transparent)] text-destructive'
        : 'border-border bg-muted/40 text-muted-foreground';
  return (
    <span className={`shrink-0 rounded-[calc(var(--radius)-2px)] border px-1.5 py-0.5 text-[11px] leading-none font-medium ${className}`}>
      {statusLabel(normalized)}
    </span>
  );
}

function CloudAliasAction({ status, busy, onLogin }: { status: string; busy: boolean; onLogin: () => void }): React.ReactElement {
  if (status.trim() === 'active') {
    return (
      <div className="inline-flex items-center gap-1.5 px-1 text-xs font-medium text-green-700 dark:text-green-400" aria-label="Connected">
        <CheckCircle2 className="size-4" aria-hidden="true" />
        Connected
      </div>
    );
  }
  return (
    <Button type="button" variant="outline" size="sm" disabled={busy} onClick={onLogin}>
      <LogIn aria-hidden="true" />
      Login
    </Button>
  );
}

function CloudContextAction({ status, busy, onStart, onStop }: { status: string; busy: boolean; onStart: () => void; onStop: () => void }): React.ReactElement {
  if (status.trim() === 'running') {
    return (
      <Button type="button" variant="outline" size="sm" disabled={busy} onClick={onStop}>
        <Power aria-hidden="true" />
        Stop
      </Button>
    );
  }
  return (
    <Button type="button" variant="outline" size="sm" disabled={busy} onClick={onStart}>
      <Play aria-hidden="true" />
      Start
    </Button>
  );
}

function cloudProviderSummary(provider: { provider: string; username?: string; accountId?: string }): string {
  const providerName = provider.provider.toUpperCase();
  if (provider.accountId && provider.username) {
    return `${providerName} account ${provider.accountId} - ${provider.username}`;
  }
  if (provider.accountId) {
    return `${providerName} account ${provider.accountId}`;
  }
  return providerName;
}

function cloudContextSummary(context: { cloudProviderAlias: string; region: string; instanceType: string; diskSizeGb: number; diskType: string; instanceId?: string }): string {
  const parts = [
    context.cloudProviderAlias,
    cloudRegionLabel(context.region),
    context.instanceType,
    `${context.diskSizeGb} GB ${context.diskType}`,
  ].filter(Boolean);
  if (context.instanceId) {
    parts.push(context.instanceId);
  }
  return parts.join(' - ');
}

function cloudRegionLabel(region: string): string {
  switch (region) {
    case 'eu-west-2':
      return 'London';
    case 'eu-west-1':
      return 'Ireland';
    default:
      return region;
  }
}

function generatedContextName(provider: { alias: string; username?: string; accountId?: string } | undefined, region: string, contexts: Array<{ name: string; kubernetesContext: string }>): string {
  if (!provider) {
    return '';
  }
  const identity = provider.accountId || provider.username || provider.alias;
  const tail = sanitizeContextName([identity, region || 'eu-west-2'].filter(Boolean).join('-'));
  return nextGeneratedContextName(tail, contexts);
}

function nextGeneratedContextName(tail: string, contexts: Array<{ name: string; kubernetesContext: string }>): string {
  const normalizedTail = sanitizeContextName(tail) || 'context';
  const suffix = `-${normalizedTail}`;
  let next = 1;
  for (const context of contexts) {
    for (const name of [context.name, context.kubernetesContext]) {
      if (!name.startsWith('erun-') || !name.endsWith(suffix)) {
        continue;
      }
      const counter = name.slice('erun-'.length, name.length - suffix.length);
      if (!/^\d{3}$/.test(counter)) {
        continue;
      }
      const value = Number(counter);
      if (value >= next) {
        next = value + 1;
      }
    }
  }
  return `erun-${String(next).padStart(3, '0')}-${normalizedTail}`;
}

function sanitizeContextName(value: string): string {
  let result = '';
  let lastDash = false;
  for (const char of value.trim().toLowerCase()) {
    if ((char >= 'a' && char <= 'z') || (char >= '0' && char <= '9')) {
      result += char;
      lastDash = false;
      continue;
    }
    if (!lastDash) {
      result += '-';
      lastDash = true;
    }
  }
  return result.replace(/^-+|-+$/g, '');
}

function statusLabel(status: string): string {
  switch (status) {
    case 'active':
      return 'Active';
    case 'running':
      return 'Running';
    case 'stopped':
      return 'Stopped';
    case 'pending':
      return 'Pending';
    case 'expired':
      return 'Expired';
    case 'not_configured':
      return 'Not configured';
    default:
      return 'Unknown';
  }
}

function RegionSelectInput({ id, value, disabled, onChange }: { id: string; value: string; disabled?: boolean; onChange: (value: string) => void }): React.ReactElement {
  const regions = [
    { value: 'eu-west-2', label: 'London' },
    { value: 'eu-west-1', label: 'Ireland' },
  ];
  return (
    <div className="grid gap-2">
      <Label htmlFor={id}>Region</Label>
      <select
        id={id}
        className="border-input bg-background ring-offset-background placeholder:text-muted-foreground focus-visible:ring-ring flex h-10 w-full rounded-[var(--radius)] border px-3 py-2 text-sm file:border-0 file:bg-transparent file:text-sm file:font-medium focus-visible:ring-2 focus-visible:ring-offset-2 focus-visible:outline-none disabled:cursor-not-allowed disabled:opacity-50"
        value={value || 'eu-west-2'}
        disabled={disabled}
        onChange={(event) => onChange(event.target.value)}
      >
        {regions.map((region) => (
          <option key={region.value} value={region.value}>
            {region.label}
          </option>
        ))}
      </select>
    </div>
  );
}

function SelectInput({ id, label, value, options, disabled, emptyLabel, onChange }: { id: string; label: string; value: string; options: string[]; disabled?: boolean; emptyLabel?: string; onChange: (value: string) => void }): React.ReactElement {
  return (
    <div className="grid gap-2">
      <Label htmlFor={id}>{label}</Label>
      <select
        id={id}
        className="border-input bg-background ring-offset-background placeholder:text-muted-foreground focus-visible:ring-ring flex h-10 w-full rounded-[var(--radius)] border px-3 py-2 text-sm file:border-0 file:bg-transparent file:text-sm file:font-medium focus-visible:ring-2 focus-visible:ring-offset-2 focus-visible:outline-none disabled:cursor-not-allowed disabled:opacity-50"
        value={value}
        disabled={disabled}
        onChange={(event) => onChange(event.target.value)}
      >
        {options.length === 0 ? (
          <option value="">{emptyLabel || 'No options'}</option>
        ) : (
          options.map((option) => (
            <option key={option} value={option}>
              {option}
            </option>
          ))
        )}
      </select>
    </div>
  );
}

function SelectField({ id, label, value, options, disabled, onChange }: { id: string; label: string; value: string; options: string[]; disabled?: boolean; onChange: (value: string) => void }): React.ReactElement {
  return (
    <div className="grid gap-2">
      <Label htmlFor={id}>{label}</Label>
      <select
        id={id}
        className="border-input bg-background ring-offset-background placeholder:text-muted-foreground focus-visible:ring-ring flex h-10 w-full rounded-[var(--radius)] border px-3 py-2 text-sm file:border-0 file:bg-transparent file:text-sm file:font-medium focus-visible:ring-2 focus-visible:ring-offset-2 focus-visible:outline-none disabled:cursor-not-allowed disabled:opacity-50"
        value={value}
        disabled={disabled}
        onChange={(event) => onChange(event.target.value)}
      >
        {options.length === 0 ? (
          <option value="">No tenants</option>
        ) : (
          <>
            <option value="">Not configured</option>
            {options.map((option) => (
              <option key={option} value={option}>
                {option}
              </option>
            ))}
          </>
        )}
      </select>
    </div>
  );
}

function optionValues(values: string[], current: string): string[] {
  const seen = new Set<string>();
  return [current, ...values]
    .map((value) => value.trim())
    .filter((value) => {
      if (!value || seen.has(value)) {
        return false;
      }
      seen.add(value);
      return true;
    });
}
