import { Button, EmptyState, Label, SelectField, uniqueSuggestions } from 'erun-kit';
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
import { EnvironmentHealthSection } from '@/components/app/EnvironmentHealthSection';
import { cloudProviderTypeLabel } from '@/components/app/GlobalConfigDialog.helpers';
import { CloudStatusBadge } from '@/components/app/GlobalConfigDialog.shared';
import { LocalRepoPathInput } from '@/components/app/LocalRepoPathInput';
import { ReadonlyField } from '@/components/app/ManageDialog.fields';
import { PullCoordinatesFields } from '@/components/app/ManageDialogPullCoordinates';
import {
  EnvironmentTypeValues,
  type UICloudContextStatus,
  type UIEnvironmentCloudAlias,
} from '@/types';

// Radix Select rejects an empty-string item value, so the clear ("— None —")
// option needs a non-empty sentinel that maps back to "".
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
  // Fall back to the effective path so the field is never blank for an env
  // whose repo path is derived rather than explicitly set.
  const repoPathValue = config.localRepoPath?.trim() ? config.localRepoPath : config.repoPath;

  // One statement of "the editor is not accepting input right now", so each
  // field does not restate it.
  const fieldsDisabled = dialog.busy || dialog.configLoading;

  return (
    <>
      {config.type === 'local-agent' || config.type === 'host' ? (
        // A local-agent or host env's worktree is a directory on this
        // machine, so only these get an editable field — letting an operator
        // repoint a moved repo without hand-editing config.yaml. Remote-agent
        // (PVC) and runtime (no worktree) repos are not local paths, so they
        // stay read-only.
        <LocalRepoPathInput
          id="environment-config-repopath"
          label="Repository path"
          helper={
            config.type === 'host'
              ? 'Absolute path on this machine. This env has no pod — it IS this directory. Applied on Save.'
              : 'Absolute path on this machine, mounted into the agent pod as the worktree. Applied on Save; takes effect on the next deploy.'
          }
          value={repoPathValue}
          disabled={fieldsDisabled}
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
        inherited={config.containerRegistriesInherited}
        suggestions={containerRegistrySuggestions}
        disabled={fieldsDisabled}
        onChange={(containerRegistries) => {
          dispatch(updateManageConfig({ containerRegistries }));
        }}
      />
      <PullCoordinatesFields
        config={config}
        disabled={fieldsDisabled}
        onChange={(patch) => {
          dispatch(updateManageConfig(patch));
        }}
      />
      <CloudAliasSlots config={config} disabled={dialog.busy} />
      <CloudContextField
        context={config.cloudContext}
        cloudProviderAlias={config.cloudProviderAlias}
        disabled={fieldsDisabled}
        loading={
          dialog.busyAction === 'cloud-context-power' &&
          dialog.busyTarget === config.cloudContext?.name
        }
        onStart={(name) => void dispatch(startManageCloudContext(name))}
        onStop={(name) => void dispatch(stopManageCloudContext(name))}
      />
      <EnvironmentTypeField
        value={config.type}
        disabled={fieldsDisabled}
        onChange={(type) => {
          dispatch(updateManageConfig({ type }));
        }}
      />
      <EnvironmentHealthSection dialog={dialog} />
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
    case 'host':
      return 'Host (no pod, no cluster — this machine only)';
    default:
      return 'Unknown';
  }
}

// The env type drives build/deploy policy but is otherwise only correctable by
// hand-editing config.yaml, so a mis-set type is fixable here as a selector.
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

// Each provider type (e.g. an AWS account and a Cloudflare token) can be linked
// independently, so the env gets one selector per slot. An older backend that
// predates slots sends none; a single AWS selector renders as a fallback so the
// control never disappears.
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

// AWS keeps the historical element id so the long-standing specs that target
// the AWS selector stay stable.
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
            <CloudStatusBadge status={context.status} />
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

// An unlinked context must offer a recovery path, not just show "Not linked":
// either pick an alias above, or open ERun settings to init a context.
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
