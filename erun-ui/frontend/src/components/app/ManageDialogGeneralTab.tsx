import { Cog, LoaderCircle, Play, Power, Server } from 'lucide-react';
import * as React from 'react';

import { openGlobalConfigDialog } from '@/app/globalConfigThunks';
import { useAppDispatch, useAppSelector } from '@/app/hooks';
import {
  closeManageDialog,
  startManageCloudContext,
  stopManageCloudContext,
  updateManageCloudAliasSlot,
  updateManageConfig,
} from '@/app/manageEnvironmentThunks';
import { loadSavedPastContainerRegistries } from '@/app/storage';
import { ContainerRegistriesField } from '@/components/app/ContainerRegistriesField';
import { uniqueSuggestions } from '@/components/app/EditableComboField.helpers';
import { EmptyState } from '@/components/app/EmptyState';
import { cloudProviderTypeLabel } from '@/components/app/GlobalConfigDialog.helpers';
import { LocalRepoPathInput } from '@/components/app/LocalRepoPathInput';
import { ReadonlyField, StatusBadge } from '@/components/app/ManageDialog.fields';
import { SelectField } from '@/components/app/SelectField';
import { Button } from '@/components/ui/button';
import { Label } from '@/components/ui/label';
import {
  EnvironmentTypeValues,
  type UICloudContextStatus,
  type UIEnvironmentCloudAlias,
} from '@/types';

// Sentinel for the clear ("— None —") option in the cloud-alias dropdown.
// Radix Select rejects an empty-string item value, so the option carries this
// value and CloudAliasSelect maps it back to "" before persisting.
const CLOUD_ALIAS_NONE_VALUE = '__none__';

export function GeneralTab(): React.ReactElement {
  const dispatch = useAppDispatch();
  const dialog = useAppSelector((state) => state.manageDialog);
  const config = dialog.config;
  const containerRegistrySuggestions = React.useMemo(
    () =>
      uniqueSuggestions([
        ...config.containerRegistries.map((entry) => entry.registry),
        ...loadSavedPastContainerRegistries(),
      ]),
    [config.containerRegistries],
  );
  // Show the raw LocalRepoPath when set; fall back to the effective path so the
  // field is never blank for an env whose path is derived. Editing sets
  // localRepoPath, which SaveEnvironmentConfig persists.
  const repoPathValue = config.localRepoPath?.trim() ? config.localRepoPath : config.repoPath;
  return (
    <>
      {config.type === 'local-agent' ? (
        // A local-agent env mounts its worktree from this host path
        // (EnvConfig.LocalRepoPath); make it correctable here so an operator can
        // repoint a moved repo without hand-editing config.yaml. Free-text +
        // Browse (recognition over recall). Remote-agent (PVC worktree) and
        // runtime (no worktree) keep the read-only field — their repo is not a
        // local host path. Persisted on Save; takes effect on the next deploy.
        <LocalRepoPathInput
          id="environment-config-repopath"
          label="Repository path"
          helper="Absolute path on this machine, mounted into the agent pod as the worktree. Applied on Save; takes effect on the next deploy."
          value={repoPathValue}
          disabled={dialog.busy || dialog.configLoading}
          onChange={(localRepoPath) => {
            dispatch(updateManageConfig({ localRepoPath }));
          }}
        />
      ) : (
        <ReadonlyField
          id="environment-config-repopath"
          label="Repository path"
          value={config.repoPath}
        />
      )}
      <ReadonlyField
        id="environment-config-kubernetescontext"
        label="Kubernetes context"
        value={config.kubernetesContext}
      />
      <ContainerRegistriesField
        entries={config.containerRegistries}
        suggestions={containerRegistrySuggestions}
        disabled={dialog.busy || dialog.configLoading}
        onChange={(containerRegistries) => {
          dispatch(updateManageConfig({ containerRegistries }));
        }}
      />
      <CloudAliasSlots config={config} disabled={dialog.busy} />
      <CloudContextField
        context={config.cloudContext}
        cloudProviderAlias={config.cloudProviderAlias}
        disabled={dialog.busy || dialog.configLoading}
        loading={
          dialog.busyAction === 'cloud-context-power' &&
          dialog.busyTarget === config.cloudContext?.name
        }
        onStart={(name) => void dispatch(startManageCloudContext(name))}
        onStop={(name) => void dispatch(stopManageCloudContext(name))}
      />
      <EnvironmentTypeField
        value={config.type}
        disabled={dialog.busy || dialog.configLoading}
        onChange={(type) => {
          dispatch(updateManageConfig({ type }));
        }}
      />
    </>
  );
}

function environmentTypeLabel(type: string | undefined): string {
  switch (type) {
    case 'local-agent':
      return 'Local agent (worktree mounted from your machine)';
    case 'remote-agent':
      return 'Remote agent (worktree cloned to PVC)';
    case 'runtime':
      return 'Runtime (no worktree; receives deploys)';
    default:
      return 'Unknown';
  }
}

// EnvironmentTypeField makes the env type a correctable, constrained selector
// rather than a read-only label. The type drives build/deploy policy
// (BuildsHere / RemoteWorktree), so it is set deliberately at init and is not a
// derived value — but a wrong value (e.g. a type that resolved to "runtime" on
// what is really a remote-agent env, issue #615) otherwise has no recovery
// surface short of hand-editing config.yaml. Recognition over recall: the
// option set is the three known types. The change is applied on Save and takes
// effect on the next deploy, which reconfigures the worktree storage.
function EnvironmentTypeField({
  value,
  disabled,
  onChange,
}: {
  value: string | undefined;
  disabled?: boolean;
  onChange: (value: string) => void;
}): React.ReactElement {
  return (
    <SelectField
      id="environment-config-type"
      label="Environment type"
      value={value ?? ''}
      placeholder="Select environment type"
      options={EnvironmentTypeValues.map((type) => ({
        value: type,
        label: environmentTypeLabel(type),
      }))}
      helper="Sets whether this environment builds here and where its worktree lives. Change it only to correct a mis-set type; the new type is applied on Save and takes effect on the next deploy, which reconfigures the worktree."
      disabled={disabled}
      onChange={onChange}
    />
  );
}

// CloudAliasSlots renders one cloud-alias selector per provider type the env
// can attach (issue #630): an AWS account AND a Cloudflare token can be linked
// independently, each with its own "— None —" clear option. The per-type slots
// come from the backend (EnvConfig.ResolvedCloudAliases grouped by type). When
// the backend predates slots, a single AWS selector renders as a fallback so
// the control never disappears.
function CloudAliasSlots({
  config,
  disabled,
}: {
  config: {
    cloudAliasSlots?: UIEnvironmentCloudAlias[];
    cloudProviderAlias: string;
    cloudProviderAliases?: string[];
  };
  disabled?: boolean;
}): React.ReactElement {
  const dispatch = useAppDispatch();
  const slots = config.cloudAliasSlots ?? [];
  if (slots.length === 0) {
    return (
      <CloudAliasSelect
        id="environment-config-cloudprovideralias"
        label="Cloud alias"
        value={config.cloudProviderAlias}
        options={config.cloudProviderAliases ?? []}
        disabled={disabled}
        onChange={(alias) => {
          dispatch(updateManageConfig({ cloudProviderAlias: alias }));
        }}
      />
    );
  }
  return (
    <>
      {slots.map((slot) => (
        <CloudAliasSelect
          key={slot.provider}
          id={cloudAliasSlotFieldId(slot.provider)}
          label={cloudProviderTypeLabel(slot.provider)}
          value={slot.alias}
          options={slot.options}
          disabled={disabled}
          onChange={(alias) => {
            dispatch(updateManageCloudAliasSlot(slot.provider, alias));
          }}
        />
      ))}
    </>
  );
}

// cloudAliasSlotFieldId keeps the AWS slot on the historical id
// (#environment-config-cloudprovideralias) so the long-standing AWS selector
// contract — and the specs that target it — stay stable, while every other
// provider type gets a suffixed id so the per-type selectors are addressable.
function cloudAliasSlotFieldId(provider: string): string {
  const type = provider.trim().toLowerCase();
  return type === '' || type === 'aws'
    ? 'environment-config-cloudprovideralias'
    : `environment-config-cloudprovideralias-${type}`;
}

function CloudAliasSelect({
  id,
  label,
  value,
  options,
  disabled,
  onChange,
}: {
  id: string;
  label: string;
  value: string;
  options: string[];
  disabled?: boolean;
  onChange: (value: string) => void;
}): React.ReactElement {
  const dispatch = useAppDispatch();
  const normalizedValue = value.trim();
  const normalizedOptions = options.map((option) => option.trim()).filter(Boolean);
  const selectOptions =
    normalizedValue && !normalizedOptions.includes(normalizedValue)
      ? [normalizedValue, ...normalizedOptions]
      : normalizedOptions;
  if (selectOptions.length === 0) {
    return (
      <div className="grid gap-2">
        <Label htmlFor={id}>{label}</Label>
        <EmptyState
          icon={<Server />}
          heading="No cloud aliases configured"
          body="Cloud aliases are how ERun connects to your cloud accounts. Add one in ERun settings to link this environment."
          action={
            <Button
              type="button"
              variant="outline"
              size="sm"
              disabled={disabled}
              onClick={() => {
                dispatch(closeManageDialog());
                dispatch(openGlobalConfigDialog());
              }}
            >
              <Cog aria-hidden="true" />
              Configure cloud aliases…
            </Button>
          }
        />
      </div>
    );
  }
  // Radix Select forbids an empty-string item value, so the clear option uses
  // a sentinel that maps back to "" on change. This gives the user a way out
  // of a selected alias — without it the dropdown only ever offered aliases
  // and a set value could never be cleared (issue #211). "" resolves to the
  // placeholder, which renders the env as "Not linked" downstream.
  const optionItems = [
    { value: CLOUD_ALIAS_NONE_VALUE, label: '— None —' },
    ...selectOptions.map((option) => ({ value: option, label: option })),
  ];
  return (
    <SelectField
      id={id}
      label={label}
      value={normalizedValue}
      options={optionItems}
      placeholder="Select cloud alias"
      emptyLabel="No cloud aliases configured"
      helper="Attaching an alias delivers its credentials into this environment, so it can act on your behalf."
      disabled={disabled}
      onChange={(next) => {
        onChange(next === CLOUD_ALIAS_NONE_VALUE ? '' : next);
      }}
    />
  );
}

function CloudContextField({
  context,
  cloudProviderAlias,
  disabled,
  loading,
  onStart,
  onStop,
}: {
  context: UICloudContextStatus | undefined;
  cloudProviderAlias: string;
  disabled?: boolean;
  loading?: boolean;
  onStart: (name: string) => void;
  onStop: (name: string) => void;
}): React.ReactElement {
  if (!context) {
    return <UnlinkedCloudContext cloudProviderAlias={cloudProviderAlias} />;
  }
  const running = context.status.trim() === 'running';
  return (
    <div className="grid gap-2">
      <div id="environment-config-cloudcontext" className="text-sm font-medium leading-none">
        Cloud context
      </div>
      <div
        className="grid grid-cols-[minmax(0,1fr)_auto] items-center gap-3 rounded-[var(--radius)] border border-border px-3 py-2.5"
        aria-labelledby="environment-config-cloudcontext"
      >
        <div className="grid min-w-0 gap-1">
          <div className="flex min-w-0 items-center gap-2 text-sm font-medium">
            <Server className="size-4 shrink-0 text-muted-foreground" aria-hidden="true" />
            <span className="truncate">{context.kubernetesContext || context.name}</span>
            <StatusBadge status={context.status} />
          </div>
          <div className="truncate text-xs text-muted-foreground">
            {[context.cloudProviderAlias, context.region, context.instanceType, context.instanceId]
              .filter(Boolean)
              .join(' | ')}
            {context.message ? ` - ${context.message}` : ''}
          </div>
        </div>
        {running ? (
          <Button
            type="button"
            variant="outline"
            size="sm"
            disabled={disabled}
            onClick={() => {
              onStop(context.name);
            }}
          >
            {loading ? (
              <LoaderCircle className="animate-spin" aria-hidden="true" />
            ) : (
              <Power aria-hidden="true" />
            )}
            {loading ? 'Stopping...' : 'Stop'}
          </Button>
        ) : (
          <Button
            type="button"
            variant="outline"
            size="sm"
            disabled={disabled}
            onClick={() => {
              onStart(context.name);
            }}
          >
            {loading ? (
              <LoaderCircle className="animate-spin" aria-hidden="true" />
            ) : (
              <Play aria-hidden="true" />
            )}
            {loading ? 'Starting...' : 'Start'}
          </Button>
        )}
      </div>
    </div>
  );
}

// UnlinkedCloudContext renders an empty-state surface that explains why
// no context is currently linked AND offers the matching recovery action.
// Per AGENTS.md, the dashed-card with raw "Not linked" text was a gap:
// users saw the missing state but had no path to recover. Now the path is
// either "pick an alias above" or "open ERun settings to init a context".
function UnlinkedCloudContext({
  cloudProviderAlias,
}: {
  cloudProviderAlias: string;
}): React.ReactElement {
  const dispatch = useAppDispatch();
  const hasAlias = cloudProviderAlias.trim().length > 0;
  return (
    <div className="grid gap-2">
      <div className="text-sm font-medium leading-none">Cloud context</div>
      <EmptyState
        icon={<Server />}
        heading={hasAlias ? 'No linked cloud context' : 'Not linked'}
        body={
          hasAlias
            ? 'This environment has a cloud alias but no cloud context. Init a context in ERun settings, then return here to link it.'
            : 'Select a cloud alias above first. Cloud contexts are then created and managed from ERun settings.'
        }
        action={
          <Button
            type="button"
            variant="outline"
            size="sm"
            onClick={() => {
              dispatch(closeManageDialog());
              dispatch(openGlobalConfigDialog());
            }}
          >
            <Cog aria-hidden="true" />
            Open ERun settings…
          </Button>
        }
      />
    </div>
  );
}
