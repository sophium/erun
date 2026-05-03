import * as React from 'react';
import { Cloud, Link, LoaderCircle, RefreshCw, Save } from 'lucide-react';

import type { ERunUIController } from '@/app/ERunUIController';
import { readError } from '@/app/errors';
import type { AppState } from '@/app/state';
import { Button } from '@/components/ui/button';
import { Checkbox } from '@/components/ui/checkbox';
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';

const dialogErrorClassName =
  'rounded-[var(--radius)] border border-[color-mix(in_oklch,var(--destructive)_36%,transparent)] bg-[color-mix(in_oklch,var(--destructive)_8%,transparent)] px-[11px] py-[9px] text-[13px] leading-[1.35] text-destructive [overflow-wrap:anywhere]';

export function TenantDialogView({ controller, state }: { controller: ERunUIController; state: AppState }): React.ReactElement {
  const dialog = state.tenantDialog;
  const config = dialog.config;
  const tenant = state.tenants.find((candidate) => candidate.name === dialog.tenant);
  const environmentOptions = optionValues((tenant?.environments || []).map((environment) => environment.name), config.defaultEnvironment);

  return (
    <Dialog open={dialog.open} onOpenChange={(open) => !open && controller.closeTenantDialog()}>
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
            void controller.submitTenantConfig().catch((error: unknown) => {
              controller.showTerminalMessage(readError(error));
            });
          }}
        >
          <DialogHeader>
            <DialogTitle>Manage tenant</DialogTitle>
            <DialogDescription>{dialog.tenant}</DialogDescription>
          </DialogHeader>
          <TenantDialogFields controller={controller} state={state} environmentOptions={environmentOptions} apiPlaceholder={tenantDefaultAPIURL(tenant)} />
          {dialog.error && (
            <div className={dialogErrorClassName} role="alert">
              {dialog.error}
            </div>
          )}
          <DialogFooter>
            <Button type="button" variant="outline" size="sm" disabled={dialog.busy} onClick={() => controller.closeTenantDialog()}>
              Cancel
            </Button>
            <Button type="submit" size="sm" disabled={dialog.busy || dialog.configLoading}>
              {dialog.busy && dialog.busyAction === 'save' ? <LoaderCircle className="animate-spin" aria-hidden="true" /> : <Save aria-hidden="true" />}
              {dialog.busy && dialog.busyAction === 'save' ? 'Saving...' : 'Save'}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

function TenantDialogFields({ controller, state, environmentOptions, apiPlaceholder }: { controller: ERunUIController; state: AppState; environmentOptions: string[]; apiPlaceholder: string }): React.ReactElement {
  const dialog = state.tenantDialog;
  const config = dialog.config;
  if (dialog.configLoading) {
    return <div className="rounded-[var(--radius)] border border-dashed border-border px-3 py-2.5 text-[13px] leading-[1.35] text-muted-foreground">Loading config...</div>;
  }
  return (
    <div className="grid gap-3">
      <ReadonlyField id="tenant-config-name" label="Tenant name" value={config.name} />
      <SelectField id="tenant-config-defaultenvironment" label="Default environment" value={config.defaultEnvironment} options={environmentOptions} disabled={dialog.busy || environmentOptions.length === 0} onChange={(defaultEnvironment) => controller.updateTenantConfig({ defaultEnvironment })} />
      <TextField id="tenant-config-apiurl" label="API URL" value={config.apiUrl} disabled={dialog.busy} placeholder={apiPlaceholder} onChange={(apiUrl) => controller.updateTenantConfig({ apiUrl })} />
      <CloudAliasesField controller={controller} state={state} />
    </div>
  );
}

function CloudAliasesField({ controller, state }: { controller: ERunUIController; state: AppState }): React.ReactElement {
  const dialog = state.tenantDialog;
  const config = dialog.config;
  const providers = config.cloudProviders || [];
  const linked = new Set((config.cloudProviderAliases || []).map((alias) => alias.trim()).filter(Boolean));
  const primary = (config.primaryCloudProviderAlias || '').trim();

  if (providers.length === 0) {
    return (
      <div className="grid gap-2">
        <Label>Cloud aliases</Label>
        <div className="px-0.5 py-2 text-[13px] leading-[1.35] text-muted-foreground">No cloud aliases configured</div>
      </div>
    );
  }

  return (
    <div className="grid gap-2">
      <Label>Cloud aliases</Label>
      <div className="overflow-hidden rounded-[var(--radius)] border border-border">
        {providers.map((provider, index) => (
          <CloudAliasRow key={provider.alias} controller={controller} dialog={dialog} config={config} provider={provider} checked={linked.has(provider.alias.trim())} primary={primary} withBorder={index > 0} />
        ))}
      </div>
    </div>
  );
}

function CloudAliasRow({ controller, dialog, config, provider, checked, primary, withBorder }: { controller: ERunUIController; dialog: AppState['tenantDialog']; config: AppState['tenantDialog']['config']; provider: AppState['tenantDialog']['config']['cloudProviders'][number]; checked: boolean; primary: string; withBorder: boolean }): React.ReactElement {
  const alias = provider.alias.trim();
  const hasIssuer = Boolean(provider.oidcIssuerUrl?.trim());
  const oidcBusy = dialog.busy && dialog.busyAction === 'cloud-oidc' && dialog.busyTarget === alias;
  return (
    <div className="grid grid-cols-[auto_minmax(0,1fr)_auto_auto] items-center gap-3 border-border px-3 py-2.5 data-[border=true]:border-t" data-border={withBorder}>
      <Checkbox aria-label={`Trust ${alias}`} checked={checked} disabled={dialog.busy || (!checked && !hasIssuer)} onCheckedChange={(value) => updateLinkedCloudAlias(controller, config, alias, Boolean(value))} />
      <CloudAliasIdentity alias={alias} issuer={provider.oidcIssuerUrl || ''} />
      <CloudAliasOIDCButton controller={controller} alias={alias} hasIssuer={hasIssuer} busy={dialog.busy} oidcBusy={oidcBusy} />
      <CloudAliasPrimaryControl controller={controller} alias={alias} checked={checked} primary={primary} busy={dialog.busy} />
    </div>
  );
}

function CloudAliasIdentity({ alias, issuer }: { alias: string; issuer: string }): React.ReactElement {
  const hasIssuer = Boolean(issuer.trim());
  return (
    <div className="grid min-w-0 gap-1">
      <div className="flex min-w-0 items-center gap-2 text-sm font-medium">
        <Cloud className="size-4 shrink-0 text-muted-foreground" aria-hidden="true" />
        <span className="truncate">{alias}</span>
      </div>
      <div className="truncate text-xs text-muted-foreground">{hasIssuer ? issuer : 'OIDC issuer required before linking'}</div>
    </div>
  );
}

function CloudAliasOIDCButton({ controller, alias, hasIssuer, busy, oidcBusy }: { controller: ERunUIController; alias: string; hasIssuer: boolean; busy: boolean; oidcBusy: boolean }): React.ReactElement {
  return (
    <Button
      type="button"
      variant="ghost"
      size="icon"
      className="size-8"
      title={hasIssuer ? `Refresh OIDC issuer for ${alias}` : `Set up OIDC issuer for ${alias}`}
      disabled={busy || !alias}
      onClick={() => {
        void controller.setupTenantCloudProviderOIDC(alias).catch((error: unknown) => {
          controller.showTerminalMessage(readError(error));
        });
      }}
    >
      {oidcBusy ? <LoaderCircle className="animate-spin" aria-hidden="true" /> : hasIssuer ? <RefreshCw aria-hidden="true" /> : <Link aria-hidden="true" />}
    </Button>
  );
}

function CloudAliasPrimaryControl({ controller, alias, checked, primary, busy }: { controller: ERunUIController; alias: string; checked: boolean; primary: string; busy: boolean }): React.ReactElement {
  return (
    <label className="flex items-center gap-1.5 text-xs font-medium text-muted-foreground">
      <input type="radio" className="size-3.5" name="tenant-primary-cloud-alias" checked={checked && primary === alias} disabled={busy || !checked} onChange={() => controller.updateTenantConfig({ primaryCloudProviderAlias: alias })} />
      Primary
    </label>
  );
}

function updateLinkedCloudAlias(controller: ERunUIController, config: AppState['tenantDialog']['config'], alias: string, checked: boolean): void {
  const aliases = (config.cloudProviderAliases || []).map((value) => value.trim()).filter(Boolean);
  const next = checked ? uniqueSorted([...aliases, alias]) : aliases.filter((value) => value !== alias);
  let primary = (config.primaryCloudProviderAlias || '').trim();
  if (!next.length) {
    primary = '';
  } else if (!primary || !next.includes(primary)) {
    primary = next[0];
  }
  controller.updateTenantConfig({ cloudProviderAliases: next, primaryCloudProviderAlias: primary });
}

function uniqueSorted(values: string[]): string[] {
  return Array.from(new Set(values.map((value) => value.trim()).filter(Boolean))).sort((left, right) => left.localeCompare(right));
}

function TextField({ id, label, value, disabled, placeholder, onChange }: { id: string; label: string; value: string; disabled?: boolean; placeholder?: string; onChange: (value: string) => void }): React.ReactElement {
  return (
    <div className="grid gap-2">
      <Label htmlFor={id}>{label}</Label>
      <Input id={id} value={value} disabled={disabled} placeholder={placeholder} onChange={(event) => onChange(event.target.value)} />
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
          <option value="">No environments</option>
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

function tenantDefaultAPIURL(tenant: AppState['tenants'][number] | undefined): string {
  const environment = tenant?.environments.find((candidate) => candidate.apiUrl);
  return environment?.apiUrl || 'Environment API URL';
}

function ReadonlyField({ id, label, value }: { id: string; label: string; value: string }): React.ReactElement {
  return (
    <div className="grid gap-2">
      <div id={id} className="text-sm font-medium leading-none">
        {label}
      </div>
      <div
        className="min-h-9 rounded-[var(--radius)] border border-border bg-muted/35 px-3 py-2 text-sm leading-[1.35] text-muted-foreground [overflow-wrap:anywhere]"
        aria-labelledby={id}
      >
        {value || 'Not configured'}
      </div>
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
