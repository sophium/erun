import { Check, ChevronsUpDown, Rocket } from 'lucide-react';
import * as React from 'react';

import {
  deployComponentLabel,
  deployComponentSelectionChanged,
} from '@/app/deployComponentsSelection';
import { environmentTypeIsRemoteWorktree } from '@/app/environmentType';
import { readError } from '@/app/errors';
import { useAppDispatch, useAppSelector } from '@/app/hooks';
import {
  saveManageDeployComponents,
  selectManageVersionSuggestion,
  setManageVersionChoicesOpen,
  submitManageDeploy,
  toggleManageDeployComponent,
  updateManageConfig,
  updateManageDialog,
} from '@/app/manageEnvironmentThunks';
import { showTerminalMessage } from '@/app/notificationThunks';
import type { AppState } from '@/app/state';
import {
  versionChoiceImage,
  versionChoiceKind,
  versionChoiceLabel,
} from '@/app/versionSuggestions';
import { CheckboxField, TextField } from '@/components/app/ManageDialog.fields';
import { parseIdleTrafficBytes } from '@/components/app/ManageDialog.helpers';
import { RuntimeResourceControls } from '@/components/app/RuntimeResourceControls';
import { SelectField } from '@/components/app/SelectField';
import { Button } from '@/components/ui/button';
import {
  Command,
  CommandEmpty,
  CommandGroup,
  CommandInput,
  CommandItem,
  CommandList,
} from '@/components/ui/command';
import { Input } from '@/components/ui/input';
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover';
import { cn } from '@/lib/utils';
import type { UIVersionSuggestion } from '@/types';

type ManageDialog = AppState['manageDialog'];

export function RuntimeTab(): React.ReactElement {
  const dispatch = useAppDispatch();
  const dialog = useAppSelector((state) => state.manageDialog);
  const versionSuggestions = useAppSelector((state) => state.tenants.versionSuggestions);
  return (
    <>
      <RuntimeDeployField
        configuredVersion={dialog.config.runtimeVersion}
        overrideVersion={dialog.version}
        suggestions={versionSuggestions}
        choicesOpen={dialog.choicesOpen}
        disabled={dialog.busy || dialog.configLoading}
        onValueChange={(version) => {
          dispatch(updateManageDialog({ version }));
        }}
        onChoicesOpenChange={(open) => {
          dispatch(setManageVersionChoicesOpen(open));
        }}
        onSelect={(suggestion) => {
          dispatch(selectManageVersionSuggestion(suggestion));
        }}
        onDeploy={() =>
          void dispatch(submitManageDeploy()).catch((error: unknown) => {
            dispatch(showTerminalMessage(readError(error)));
          })
        }
      />
      <DeployComponentsField dialog={dialog} />
      <RuntimePodFields dialog={dialog} />
      <IdleStopFields dialog={dialog} />
    </>
  );
}

// Toggling changes only the one-shot selection the next Deploy uses; it becomes
// this env's saved default only when the operator clicks "Set as default".
function DeployComponentsField({ dialog }: { dialog: ManageDialog }): React.ReactElement {
  const dispatch = useAppDispatch();
  const { deployComponents, deployComponentSelection, deployComponentsLoading } = dialog;
  const selectionSet = new Set(deployComponentSelection);
  const changed = deployComponentSelectionChanged(deployComponents, deployComponentSelection);
  return (
    <div className="grid gap-3 rounded-[var(--radius)] border border-border p-3">
      <div className="flex items-center justify-between gap-2">
        <div className="text-xs leading-[1.2] font-semibold tracking-normal text-muted-foreground uppercase">
          Components to deploy
        </div>
        <Button
          id="environment-config-save-deploy-components"
          type="button"
          size="sm"
          variant="outline"
          disabled={dialog.busy || dialog.configLoading || deployComponentsLoading || !changed}
          onClick={() =>
            void dispatch(saveManageDeployComponents()).catch((error: unknown) => {
              dispatch(showTerminalMessage(readError(error)));
            })
          }
        >
          Set as default
        </Button>
      </div>
      <p className="text-xs leading-[1.35] text-muted-foreground">
        Deploy rolls out exactly the checked charts. The runtime is checked by default; set them as
        the default for this environment on this machine.
      </p>
      {deployComponentsLoading ? (
        <div className="text-sm leading-[1.35] text-muted-foreground">Loading components…</div>
      ) : deployComponents.length === 0 ? (
        <div className="text-sm leading-[1.35] text-muted-foreground">
          No deployable components found for this environment.
        </div>
      ) : (
        <div className="grid gap-2">
          {deployComponents.map((component) => (
            <CheckboxField
              key={component.name}
              id={`environment-config-deploy-component-${component.name}`}
              label={deployComponentLabel(component)}
              checked={selectionSet.has(component.name)}
              disabled={dialog.busy || dialog.configLoading}
              onChange={(checked) => {
                dispatch(toggleManageDeployComponent(component.name, checked));
              }}
            />
          ))}
        </div>
      )}
    </div>
  );
}

function RuntimePodFields({ dialog }: { dialog: ManageDialog }): React.ReactElement {
  const dispatch = useAppDispatch();
  const config = dialog.config;
  return (
    <RuntimeResourceControls
      idPrefix="environment-config-runtime"
      value={config.runtimePod}
      status={dialog.resourceStatus}
      loading={dialog.resourceStatusLoading}
      disabled={dialog.busy || dialog.configLoading}
      capacityBlocks={false}
      onChange={(runtimePod) => {
        dispatch(updateManageConfig({ runtimePod }));
      }}
    />
  );
}

function IdleStopFields({ dialog }: { dialog: ManageDialog }): React.ReactElement {
  const dispatch = useAppDispatch();
  const config = dialog.config;
  return (
    <div className="grid gap-3 rounded-[var(--radius)] border border-border p-3">
      <div className="text-xs leading-[1.2] font-semibold tracking-normal text-muted-foreground uppercase">
        Idle stop
      </div>
      <TextField
        id="environment-config-idle-timeout"
        label="Timeout"
        value={config.idle.timeout}
        disabled={dialog.busy}
        placeholder="e.g. 5m, 1h30m"
        helper="Go duration (s, m, h). Default 5m."
        onChange={(timeout) => {
          dispatch(updateManageConfig({ idle: { ...config.idle, timeout } }));
        }}
      />
      <TextField
        id="environment-config-idle-workinghours"
        label="Working hours"
        value={config.idle.workingHours}
        disabled={dialog.busy}
        placeholder="e.g. 08:00-20:00"
        helper="Format HH:MM-HH:MM. Default 08:00-20:00."
        onChange={(workingHours) => {
          dispatch(updateManageConfig({ idle: { ...config.idle, workingHours } }));
        }}
      />
      <TextField
        id="environment-config-idle-traffic"
        label="Idle SSH activity threshold"
        value={String(config.idle.idleTrafficBytes)}
        inputMode="numeric"
        disabled={dialog.busy}
        placeholder="e.g. 0"
        helper="SSH bytes per check below which the connection counts as idle. 0 disables the check."
        onChange={(idleTrafficBytes) => {
          dispatch(
            updateManageConfig({
              idle: { ...config.idle, idleTrafficBytes: parseIdleTrafficBytes(idleTrafficBytes) },
            }),
          );
        }}
      />
      {environmentTypeIsRemoteWorktree(config.type) && (
        <SelectField
          id="environment-config-autostart"
          label="Auto-start when opening"
          value={autoStartMode(config.autoStart)}
          options={AUTO_START_OPTIONS}
          helper="Applies on the next sidebar click. 'Ask each time' shows the auto-start prompt before starting the EC2 instance."
          disabled={dialog.busy || dialog.configLoading}
          onChange={(mode) => {
            dispatch(updateManageConfig({ autoStart: parseAutoStartMode(mode) }));
          }}
        />
      )}
      <CheckboxField
        id="environment-config-autoupgrade"
        label="Include in Upgrade all"
        checked={config.autoUpgrade}
        disabled={dialog.busy || dialog.configLoading}
        onChange={(autoUpgrade) => {
          dispatch(updateManageConfig({ autoUpgrade }));
        }}
      />
      {config.autoUpgrade && (
        <SelectField
          id="environment-config-upgradechannel"
          label="Upgrade channel"
          value={config.upgradeChannel ?? 'stable'}
          options={UPGRADE_CHANNEL_OPTIONS}
          helper="Which release channel 'Upgrade all' tracks: stable (semver releases) or snapshot (latest snapshot build, or the stable release once one is published on top of it)."
          disabled={dialog.busy || dialog.configLoading}
          onChange={(upgradeChannel) => {
            dispatch(updateManageConfig({ upgradeChannel }));
          }}
        />
      )}
      <CheckboxField
        id="environment-config-disablebuildscript"
        label="Ignore project build.sh"
        helper="erun build resolves Docker/release contexts directly instead of running a project build.sh in this environment."
        checked={config.disableBuildScript}
        disabled={dialog.busy || dialog.configLoading}
        onChange={(disableBuildScript) => {
          dispatch(updateManageConfig({ disableBuildScript }));
        }}
      />
    </div>
  );
}

const UPGRADE_CHANNEL_OPTIONS = [
  { value: 'stable', label: 'Stable' },
  { value: 'snapshot', label: 'Snapshot' },
];

const AUTO_START_OPTIONS = [
  { value: 'ask', label: 'Ask each time' },
  { value: 'always', label: 'Always auto-start' },
  { value: 'never', label: 'Never auto-start' },
];

function autoStartMode(value: boolean | undefined): string {
  if (value === true) {
    return 'always';
  }
  if (value === false) {
    return 'never';
  }
  return 'ask';
}

function parseAutoStartMode(mode: string): boolean | undefined {
  if (mode === 'always') {
    return true;
  }
  if (mode === 'never') {
    return false;
  }
  return undefined;
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
      <div className="text-sm font-medium leading-none">Runtime version</div>
      <div className="grid grid-cols-[minmax(0,1fr)_minmax(0,1fr)_auto] gap-2">
        <div
          id="environment-config-runtimeversion"
          className="min-h-10 rounded-[var(--radius)] border border-border bg-muted/35 px-3 py-2 text-sm leading-[1.35] text-muted-foreground [overflow-wrap:anywhere]"
        >
          {configuredVersion || 'Not configured'}
        </div>
        <RuntimeDeployVersionPicker
          overrideVersion={overrideVersion}
          suggestions={suggestions}
          choicesOpen={choicesOpen}
          disabled={disabled}
          onValueChange={onValueChange}
          onChoicesOpenChange={onChoicesOpenChange}
          onSelect={onSelect}
        />
        <Button type="button" size="sm" disabled={disabled} onClick={onDeploy}>
          <Rocket aria-hidden="true" />
          Deploy
        </Button>
      </div>
    </div>
  );
}

function RuntimeDeployVersionPicker({
  overrideVersion,
  suggestions,
  choicesOpen,
  disabled,
  onValueChange,
  onChoicesOpenChange,
  onSelect,
}: {
  overrideVersion: string;
  suggestions: UIVersionSuggestion[];
  choicesOpen: boolean;
  disabled?: boolean;
  onValueChange: (version: string) => void;
  onChoicesOpenChange: (open: boolean) => void;
  onSelect: (suggestion: UIVersionSuggestion | undefined) => void;
}): React.ReactElement {
  return (
    <div className="relative min-w-0">
      <Input
        id="manage-version"
        className="pr-10"
        value={overrideVersion}
        type="text"
        autoComplete="off"
        spellCheck={false}
        placeholder="Version to deploy"
        disabled={disabled}
        onChange={(event) => {
          onValueChange(event.target.value);
        }}
      />
      <Popover open={choicesOpen} onOpenChange={onChoicesOpenChange}>
        <PopoverTrigger asChild>
          <Button
            className="absolute right-1 top-1 size-7 text-muted-foreground"
            type="button"
            variant="ghost"
            size="icon"
            aria-label="Show version choices"
            disabled={disabled}
          >
            <ChevronsUpDown />
          </Button>
        </PopoverTrigger>
        <PopoverContent className="w-80 p-0" align="start">
          <Command>
            <CommandInput placeholder="Search versions..." />
            <CommandList>
              <CommandEmpty>No version found.</CommandEmpty>
              <CommandGroup>
                {suggestions.map((suggestion) => (
                  <RuntimeDeploySuggestionItem
                    key={`${suggestion.version}:${suggestion.image ?? ''}:${suggestion.source ?? ''}:${suggestion.label}`}
                    suggestion={suggestion}
                    selected={suggestion.version === overrideVersion}
                    onSelect={onSelect}
                  />
                ))}
              </CommandGroup>
            </CommandList>
          </Command>
        </PopoverContent>
      </Popover>
    </div>
  );
}

function RuntimeDeploySuggestionItem({
  suggestion,
  selected,
  onSelect,
}: {
  suggestion: UIVersionSuggestion;
  selected: boolean;
  onSelect: (suggestion: UIVersionSuggestion | undefined) => void;
}): React.ReactElement {
  return (
    <CommandItem
      className="min-w-0"
      value={versionChoiceLabel(suggestion)}
      onSelect={() => {
        onSelect(suggestion);
      }}
    >
      <Check className={cn('size-4 shrink-0 opacity-0', selected && 'opacity-100')} />
      <span className="flex min-w-0 flex-1 flex-col gap-0.5">
        <span className="truncate text-sm font-medium leading-tight">{suggestion.version}</span>
        <span className="truncate text-xs leading-tight text-muted-foreground">
          {[versionChoiceImage(suggestion), versionChoiceKind(suggestion)]
            .filter(Boolean)
            .join(' | ')}
        </span>
      </span>
    </CommandItem>
  );
}
