import { Check, ChevronsUpDown, Plus, Rocket } from 'lucide-react';
import * as React from 'react';

import {
  environmentTypeBuildsHereLocally,
  environmentTypeIsRemoteWorktree,
  environmentTypeIsRuntime,
} from '@/app/environmentType';
import { readError } from '@/app/errors';
import { useAppDispatch, useAppSelector } from '@/app/hooks';
import {
  selectManageVersionSuggestion,
  setManageVersionChoicesOpen,
  submitCreateVersion,
  submitManageDeploy,
  updateManageConfig,
  updateManageDialog,
} from '@/app/manageEnvironmentThunks';
import { showTerminalMessage } from '@/app/notificationThunks';
import type { AppState } from '@/app/state';
import {
  groupVersionSuggestionsBySource,
  versionChoiceImage,
  versionChoiceKind,
  versionChoiceLabel,
} from '@/app/versionSuggestions';
import { CheckboxField, TextField } from '@/components/app/ManageDialog.fields';
import { parseIdleTrafficBytes } from '@/components/app/ManageDialog.helpers';
import { DeployComponentsField } from '@/components/app/ManageDialogDeployComponents';
import { RuntimeResourceControls } from '@/components/app/RuntimeResourceControls';
import { SelectField } from '@/components/app/SelectField';
import { VersionNotices } from '@/components/app/VersionNotices';
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
import type { UIVersionSuggestion, UIVersionSuggestionNotice } from '@/types';

type ManageDialog = AppState['manageDialog'];

export function RuntimeTab(): React.ReactElement {
  const dispatch = useAppDispatch();
  const dialog = useAppSelector((state) => state.manageDialog);
  // Dialog-owned (not the shared tenants slice): this dialog resolves versions for
  // its own env; boot/env-change deltas rewrite the tenants slice for the selected
  // env and must not clobber this picker.
  const versionSuggestions = dialog.versionSuggestions;
  const versionNotices = dialog.versionSuggestionNotices;
  return (
    <>
      <RuntimeDeployField
        dialog={dialog}
        configuredVersion={dialog.config.runtimeVersion}
        overrideVersion={dialog.version}
        suggestions={versionSuggestions}
        notices={versionNotices}
        choicesOpen={dialog.choicesOpen}
        disabled={dialog.busy || dialog.configLoading}
        showCreateVersion={environmentTypeBuildsHereLocally(dialog.config.type)}
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
        onCreateVersion={() =>
          void dispatch(submitCreateVersion()).catch((error: unknown) => {
            dispatch(showTerminalMessage(readError(error)));
          })
        }
      />
      <RuntimePodFields dialog={dialog} />
      <IdleStopFields dialog={dialog} />
    </>
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
      <MountSourceFields dialog={dialog} />
    </div>
  );
}

// MountSourceFields renders the runtime-only opt-in for a mutable source
// worktree: a toggle, and — once on — the git remote to clone. Extracted from
// IdleStopFields to keep that function within its size/complexity budget.
function MountSourceFields({ dialog }: { dialog: ManageDialog }): React.ReactElement | null {
  const dispatch = useAppDispatch();
  const config = dialog.config;
  if (!environmentTypeIsRuntime(config.type)) {
    return null;
  }
  return (
    <>
      <CheckboxField
        id="environment-config-mountsource"
        label="Mount source code"
        helper="Clone the repository into a writable worktree in the runtime pod, checked out at the deployed release, so you can patch it live. Off by default — a runtime env deploys published charts and images by reference and needs no source."
        checked={config.mountSource}
        disabled={dialog.busy || dialog.configLoading}
        onChange={(mountSource) => {
          dispatch(updateManageConfig({ mountSource }));
        }}
      />
      {config.mountSource && (
        <TextField
          id="environment-config-repourl"
          label="Repository URL"
          value={config.repoURL}
          disabled={dialog.busy || dialog.configLoading}
          placeholder="e.g. https://github.com/sophium/erun.git"
          helper="Git remote cloned into the runtime pod at the deployed release tag. Required to mount source."
          onChange={(repoURL) => {
            dispatch(updateManageConfig({ repoURL }));
          }}
        />
      )}
    </>
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
  dialog,
  configuredVersion,
  overrideVersion,
  suggestions,
  notices,
  choicesOpen,
  disabled,
  showCreateVersion,
  onValueChange,
  onChoicesOpenChange,
  onSelect,
  onDeploy,
  onCreateVersion,
}: {
  dialog: ManageDialog;
  configuredVersion: string;
  overrideVersion: string;
  suggestions: UIVersionSuggestion[];
  notices: UIVersionSuggestionNotice[];
  choicesOpen: boolean;
  disabled?: boolean;
  showCreateVersion: boolean;
  onValueChange: (version: string) => void;
  onChoicesOpenChange: (open: boolean) => void;
  onSelect: (suggestion: UIVersionSuggestion | undefined) => void;
  onDeploy: () => void;
  onCreateVersion: () => void;
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
          dialog={dialog}
          overrideVersion={overrideVersion}
          suggestions={suggestions}
          notices={notices}
          choicesOpen={choicesOpen}
          disabled={disabled}
          onValueChange={onValueChange}
          onChoicesOpenChange={onChoicesOpenChange}
          onSelect={onSelect}
        />
        <Button
          id="environment-config-deploy"
          type="button"
          size="sm"
          // Deploy installs a chosen version by reference, so it stays disabled
          // until the operator picks one — never a build, never a guess — and
          // until that version's component charts have been probed, so it can't
          // fire the new version with the previous version's chart selection.
          disabled={
            disabled === true || overrideVersion.trim() === '' || dialog.deployComponentsLoading
          }
          onClick={onDeploy}
        >
          <Rocket aria-hidden="true" />
          Deploy
        </Button>
      </div>
      {/* Deploy above installs an existing published version by reference and never
          builds. Producing a new version from this env's source is this explicit,
          separate action (local-agent envs only). */}
      {showCreateVersion && (
        <Button
          id="environment-config-create-version"
          type="button"
          size="sm"
          variant="outline"
          className="justify-self-start"
          disabled={disabled}
          onClick={onCreateVersion}
        >
          <Plus aria-hidden="true" />
          Create &amp; deploy new version
        </Button>
      )}
    </div>
  );
}

function RuntimeDeployVersionPicker({
  dialog,
  overrideVersion,
  suggestions,
  notices,
  choicesOpen,
  disabled,
  onValueChange,
  onChoicesOpenChange,
  onSelect,
}: {
  dialog: ManageDialog;
  overrideVersion: string;
  suggestions: UIVersionSuggestion[];
  notices: UIVersionSuggestionNotice[];
  choicesOpen: boolean;
  disabled?: boolean;
  onValueChange: (version: string) => void;
  onChoicesOpenChange: (open: boolean) => void;
  onSelect: (suggestion: UIVersionSuggestion | undefined) => void;
}): React.ReactElement {
  // Group by source so a tenant env's two same-labelled lines — its own
  // <tenant>-devops runtime and the upstream erun-devops fallback — are told
  // apart. Headings only appear when there is more than one source; a single-line
  // picker keeps the plain "Version to deploy" heading.
  const suggestionGroups = groupVersionSuggestionsBySource(suggestions);
  const showSourceHeadings = suggestionGroups.length > 1;
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
        <PopoverContent
          // Cap to the viewport's available height and scroll, so the version
          // list plus the components checklist never overflow off-screen (which
          // clipped the last components and the dialog buttons on shorter windows).
          className="max-h-[var(--radix-popover-content-available-height)] w-[26rem] max-w-[calc(100vw-2rem)] overflow-y-auto p-0"
          align="start"
        >
          <Command>
            <CommandInput placeholder="Search versions..." />
            <CommandList>
              <CommandEmpty>No version found.</CommandEmpty>
              {suggestionGroups.map((group) => (
                <CommandGroup
                  key={group.source || 'default'}
                  heading={showSourceHeadings ? group.heading : 'Version to deploy'}
                >
                  {group.suggestions.map((suggestion) => (
                    <RuntimeDeploySuggestionItem
                      key={`${suggestion.version}:${suggestion.image ?? ''}:${suggestion.source ?? ''}:${suggestion.label}`}
                      suggestion={suggestion}
                      selected={suggestion.version === overrideVersion}
                      onSelect={onSelect}
                    />
                  ))}
                </CommandGroup>
              ))}
            </CommandList>
          </Command>
          <VersionNotices notices={notices} />
          {/* Pick a version above, then choose which of its charts to roll out:
              one panel so the component list always reads as that version's. The
              whole popover scrolls (capped to the viewport), so no nested scroll. */}
          <div className="border-t border-border p-3">
            <DeployComponentsField dialog={dialog} />
          </div>
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
