import * as React from 'react';
import { AlertTriangle, Check, ChevronsUpDown, LoaderCircle, Rocket, Save, Trash2 } from 'lucide-react';

import type { ERunUIController } from '@/app/ERunUIController';
import { readError } from '@/app/errors';
import type { AppState } from '@/app/state';
import { deleteConfirmationValue, normalizeDialogValue, versionChoiceImage, versionChoiceKind, versionChoiceLabel } from '@/app/versionSuggestions';
import { Button } from '@/components/ui/button';
import { Checkbox } from '@/components/ui/checkbox';
import { Command, CommandEmpty, CommandGroup, CommandInput, CommandItem, CommandList } from '@/components/ui/command';
import { Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover';
import type { UIVersionSuggestion } from '@/types';
import { cn } from '@/lib/utils';

const dialogErrorClassName =
  'rounded-[var(--radius)] border border-[color-mix(in_oklch,var(--destructive)_36%,transparent)] bg-[color-mix(in_oklch,var(--destructive)_8%,transparent)] px-[11px] py-[9px] text-[13px] leading-[1.35] text-destructive [overflow-wrap:anywhere]';

export function ManageDialogView({ controller, state }: { controller: ERunUIController; state: AppState }): React.ReactElement {
  const dialog = state.manageDialog;
  const confirmationRef = React.useRef<HTMLInputElement>(null);
  const selection = dialog.selection;
  const expected = selection ? deleteConfirmationValue(selection) : '';
  const confirmingDelete = dialog.tab === 'delete';
  const deleteEnabled = !dialog.busy && normalizeDialogValue(dialog.confirmation) === expected;
  const config = dialog.config;

  React.useEffect(() => {
    if (!dialog.open || !confirmingDelete) {
      return;
    }
    window.setTimeout(() => {
      confirmationRef.current?.focus();
    }, 0);
  }, [dialog.open, confirmingDelete]);

  return (
    <Dialog open={dialog.open} onOpenChange={(open) => !open && controller.closeManageDialog()}>
      <DialogContent
        className="max-h-[min(88vh,900px)] sm:max-w-2xl"
        onCloseAutoFocus={(event) => {
          event.preventDefault();
          controller.focusTerminalSoon();
        }}
      >
        <form
          className="flex max-h-[calc(min(88vh,900px)-3rem)] min-h-0 flex-col gap-4"
          onSubmit={(event) => {
            event.preventDefault();
            if (confirmingDelete && deleteEnabled) {
              void controller.submitManageDelete();
            }
          }}
        >
          <DialogHeader>
            <DialogTitle>{selection ? `${selection.tenant}-${selection.environment}` : 'Environment'}</DialogTitle>
          </DialogHeader>
          <div className="-mx-1 min-h-0 overflow-auto px-1 pb-1">
            {dialog.configLoading ? (
              <div className="rounded-[var(--radius)] border border-dashed border-border px-3 py-2.5 text-[13px] leading-[1.35] text-muted-foreground">
                Loading config...
              </div>
            ) : (
              <div className="grid gap-3">
                <ReadonlyField id="environment-config-repopath" label="repopath" value={config.repoPath} />
                <ReadonlyField id="environment-config-kubernetescontext" label="kubernetescontext" value={config.kubernetesContext} />
                <ReadonlyField id="environment-config-containerregistry" label="containerregistry" value={config.containerRegistry} />
                <RuntimeDeployField
                  configuredVersion={config.runtimeVersion}
                  overrideVersion={dialog.version}
                  suggestions={state.versionSuggestions}
                  choicesOpen={dialog.choicesOpen}
                  disabled={dialog.busy || dialog.configLoading}
                  onValueChange={(version) => controller.updateManageDialog({ version })}
                  onChoicesOpenChange={(open) => controller.setManageVersionChoicesOpen(open)}
                  onSelect={(suggestion) => controller.selectManageVersionSuggestion(suggestion)}
                  onDeploy={() => void controller.submitManageDeploy().catch((error: unknown) => controller.showTerminalMessage(readError(error)))}
                />
                <CheckboxField id="environment-config-remote" label="remote" checked={config.remote} disabled onChange={() => {}} />
                <CheckboxField id="environment-config-snapshot" label="snapshot" checked={config.snapshot} disabled={dialog.busy} onChange={(snapshot) => controller.updateManageConfig({ snapshot })} />
                <div className="grid gap-3 rounded-[var(--radius)] border border-border p-3">
                  <div className="text-xs leading-[1.2] font-semibold tracking-normal text-muted-foreground uppercase">sshd</div>
                  <CheckboxField id="environment-config-sshd-enabled" label="enabled" checked={config.sshd.enabled} disabled onChange={() => {}} />
                  <TextField
                    id="environment-config-sshd-localport"
                    label="localport"
                    value={config.sshd.localPort > 0 ? String(config.sshd.localPort) : ''}
                    disabled
                    inputMode="numeric"
                    onChange={() => {}}
                  />
                  <TextField id="environment-config-sshd-publickeypath" label="publickeypath" value={config.sshd.publicKeyPath} disabled onChange={() => {}} />
                </div>
                {confirmingDelete && (
                  <div className="grid gap-3">
                    {selection && (
                      <div className="grid grid-cols-[18px_minmax(0,1fr)] items-start gap-[9px] rounded-[var(--radius)] border border-[color-mix(in_oklch,var(--destructive)_30%,var(--border))] bg-[color-mix(in_oklch,var(--destructive)_7%,transparent)] px-[11px] py-2.5 text-[13px] leading-[1.35] text-foreground">
                        <AlertTriangle className="mt-px size-[17px] text-destructive" aria-hidden="true" />
                        <span>
                          Type{' '}
                          <code className="rounded-[calc(var(--radius)-4px)] bg-[color-mix(in_oklch,var(--destructive)_12%,transparent)] px-1 py-px font-mono text-xs text-destructive">
                            {expected}
                          </code>{' '}
                          to confirm.
                        </span>
                      </div>
                    )}
                    <TextField id="manage-confirmation" label="confirmation" value={dialog.confirmation} disabled={dialog.busy} inputRef={confirmationRef} onChange={(confirmation) => controller.updateManageDialog({ confirmation })} />
                  </div>
                )}
              </div>
            )}
          </div>
          {dialog.error && (
            <div className={dialogErrorClassName} role="alert">
              {dialog.error}
            </div>
          )}
          <DialogFooter>
            <Button type="button" variant="outline" size="sm" disabled={dialog.busy} onClick={() => controller.closeManageDialog()}>
              Cancel
            </Button>
            <Button
              type="button"
              variant={confirmingDelete ? 'destructive' : 'outline'}
              size="sm"
              disabled={dialog.busy || (confirmingDelete && !deleteEnabled)}
              onClick={() => {
                if (confirmingDelete) {
                  void controller.submitManageDelete();
                  return;
                }
                controller.updateManageDialog({ tab: 'delete', confirmation: '' });
              }}
            >
              {dialog.busy && confirmingDelete ? <LoaderCircle className="animate-spin" aria-hidden="true" /> : <Trash2 aria-hidden="true" />}
              {dialog.busy && confirmingDelete ? 'Deleting...' : 'Delete'}
            </Button>
            {!confirmingDelete && (
              <Button type="button" size="sm" disabled={dialog.busy || dialog.configLoading} onClick={() => void controller.submitManageConfig().catch((error: unknown) => controller.showTerminalMessage(readError(error)))}>
                {dialog.busy ? <LoaderCircle className="animate-spin" aria-hidden="true" /> : <Save aria-hidden="true" />}
                {dialog.busy ? 'Saving...' : 'Save'}
              </Button>
            )}
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

function RuntimeDeployField({
  configuredVersion,
  overrideVersion,
  suggestions,
  choicesOpen,
  disabled,
  onValueChange,
  onChoicesOpenChange,
  onSelect,
  onDeploy,
}: {
  configuredVersion: string;
  overrideVersion: string;
  suggestions: UIVersionSuggestion[];
  choicesOpen: boolean;
  disabled?: boolean;
  onValueChange: (version: string) => void;
  onChoicesOpenChange: (open: boolean) => void;
  onSelect: (suggestion: UIVersionSuggestion | undefined) => void;
  onDeploy: () => void;
}): React.ReactElement {
  return (
    <div className="grid gap-2">
      <Label htmlFor="environment-config-runtimeversion">runtimeversion</Label>
      <div className="grid grid-cols-[minmax(0,1fr)_minmax(0,1fr)_auto] gap-2">
        <Input id="environment-config-runtimeversion" value={configuredVersion} type="text" autoComplete="off" spellCheck={false} disabled onChange={() => {}} />
        <div className="relative min-w-0">
          <Input
            id="manage-version"
            className="pr-10"
            value={overrideVersion}
            type="text"
            autoComplete="off"
            spellCheck={false}
            placeholder="deploy override"
            disabled={disabled}
            onChange={(event) => onValueChange(event.target.value)}
          />
          <Popover open={choicesOpen} onOpenChange={onChoicesOpenChange}>
            <PopoverTrigger asChild>
              <Button className="absolute right-1 top-1 size-7 text-muted-foreground" type="button" variant="ghost" size="icon" aria-label="Show version choices" disabled={disabled}>
                <ChevronsUpDown />
              </Button>
            </PopoverTrigger>
            <PopoverContent className="w-80 p-0" align="start">
              <Command>
                <CommandInput placeholder="Search versions..." />
                <CommandList>
                  <CommandEmpty>No version found.</CommandEmpty>
                  <CommandGroup>
                    {suggestions.map((suggestion) => {
                      const selected = suggestion.version === overrideVersion;
                      return (
                        <CommandItem
                          className="min-w-0"
                          key={`${suggestion.version}:${suggestion.image || ''}:${suggestion.source || ''}:${suggestion.label || ''}`}
                          value={versionChoiceLabel(suggestion)}
                          onSelect={() => onSelect(suggestion)}
                        >
                          <Check className={cn('size-4 shrink-0 opacity-0', selected && 'opacity-100')} />
                          <span className="flex min-w-0 flex-1 flex-col gap-0.5">
                            <span className="truncate text-sm font-medium leading-tight">{suggestion.version}</span>
                            <span className="truncate text-xs leading-tight text-muted-foreground">
                              {[versionChoiceImage(suggestion), versionChoiceKind(suggestion)].filter(Boolean).join(' | ')}
                            </span>
                          </span>
                        </CommandItem>
                      );
                    })}
                  </CommandGroup>
                </CommandList>
              </Command>
            </PopoverContent>
          </Popover>
        </div>
        <Button type="button" size="sm" disabled={disabled} onClick={onDeploy}>
          <Rocket aria-hidden="true" />
          Deploy
        </Button>
      </div>
    </div>
  );
}

function TextField({ id, label, value, disabled, inputMode, inputRef, onChange }: { id: string; label: string; value: string; disabled?: boolean; inputMode?: React.HTMLAttributes<HTMLInputElement>['inputMode']; inputRef?: React.Ref<HTMLInputElement>; onChange: (value: string) => void }): React.ReactElement {
  return (
    <div className="grid gap-2">
      <Label htmlFor={id}>{label}</Label>
      <Input id={id} ref={inputRef} value={value} type="text" inputMode={inputMode} autoComplete="off" spellCheck={false} disabled={disabled} onChange={(event) => onChange(event.target.value)} />
    </div>
  );
}

function ReadonlyField({ id, label, value }: { id: string; label: string; value: string }): React.ReactElement {
  return <TextField id={id} label={label} value={value} disabled onChange={() => {}} />;
}

function CheckboxField({ id, label, checked, disabled, onChange }: { id: string; label: string; checked: boolean; disabled?: boolean; onChange: (checked: boolean) => void }): React.ReactElement {
  return (
    <div className="flex items-center gap-2">
      <Checkbox id={id} checked={checked} disabled={disabled} onCheckedChange={(value) => onChange(value === true)} />
      <Label htmlFor={id} className="text-sm font-normal">
        {label}
      </Label>
    </div>
  );
}
