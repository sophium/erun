import * as React from 'react';
import { LoaderCircle, Save } from 'lucide-react';

import type { ERunUIController } from '@/app/ERunUIController';
import { readError } from '@/app/errors';
import type { AppState } from '@/app/state';
import { Button } from '@/components/ui/button';
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog';
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
          {dialog.configLoading ? (
            <div className="rounded-[var(--radius)] border border-dashed border-border px-3 py-2.5 text-[13px] leading-[1.35] text-muted-foreground">
              Loading config...
            </div>
          ) : (
            <div className="grid gap-3">
              <ReadonlyField id="tenant-config-name" label="Tenant name" value={config.name} />
              <SelectField id="tenant-config-defaultenvironment" label="Default environment" value={config.defaultEnvironment} options={environmentOptions} disabled={dialog.busy || environmentOptions.length === 0} onChange={(defaultEnvironment) => controller.updateTenantConfig({ defaultEnvironment })} />
            </div>
          )}
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
              {dialog.busy ? <LoaderCircle className="animate-spin" aria-hidden="true" /> : <Save aria-hidden="true" />}
              {dialog.busy ? 'Saving...' : 'Save'}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
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
