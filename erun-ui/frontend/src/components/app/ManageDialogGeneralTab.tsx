import { Cog, LoaderCircle, Play, Power, Server } from 'lucide-react';
import * as React from 'react';

import { openGlobalConfigDialog } from '@/app/globalConfigThunks';
import { useAppDispatch, useAppSelector } from '@/app/hooks';
import {
  closeManageDialog,
  startManageCloudContext,
  stopManageCloudContext,
  updateManageConfig,
} from '@/app/manageEnvironmentThunks';
import { loadSavedPastContainerRegistries } from '@/app/storage';
import { EditableComboField } from '@/components/app/EditableComboField';
import { uniqueSuggestions } from '@/components/app/EditableComboField.helpers';
import { EmptyState } from '@/components/app/EmptyState';
import { CheckboxField, ReadonlyField, StatusBadge } from '@/components/app/ManageDialog.fields';
import { SelectField } from '@/components/app/SelectField';
import { Button } from '@/components/ui/button';
import { Label } from '@/components/ui/label';
import type { UICloudContextStatus } from '@/types';

export function GeneralTab(): React.ReactElement {
  const dispatch = useAppDispatch();
  const dialog = useAppSelector((state) => state.manageDialog);
  const config = dialog.config;
  const containerRegistrySuggestions = React.useMemo(
    () => uniqueSuggestions([config.containerRegistry, ...loadSavedPastContainerRegistries()]),
    [config.containerRegistry],
  );
  return (
    <>
      <ReadonlyField
        id="environment-config-repopath"
        label="Repository path"
        value={config.repoPath}
      />
      <ReadonlyField
        id="environment-config-kubernetescontext"
        label="Kubernetes context"
        value={config.kubernetesContext}
      />
      <EditableComboField
        id="environment-config-containerregistry"
        label="Container registry"
        value={config.containerRegistry}
        suggestions={containerRegistrySuggestions}
        disabled={dialog.busy || dialog.configLoading}
        onValueChange={(containerRegistry) => {
          dispatch(updateManageConfig({ containerRegistry }));
        }}
      />
      <CloudAliasSelect
        id="environment-config-cloudprovideralias"
        value={config.cloudProviderAlias}
        options={config.cloudProviderAliases ?? []}
        disabled={dialog.busy}
        onChange={(cloudProviderAlias) => {
          dispatch(updateManageConfig({ cloudProviderAlias }));
        }}
      />
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
      <ReadonlyField
        id="environment-config-type"
        label="Environment type"
        value={environmentTypeLabel(config.type)}
      />
      {config.remote && (
        <CheckboxField
          id="environment-config-remotehostcredentials"
          label="Use host AWS credentials inside this env"
          checked={config.remoteHostCredentials}
          disabled={dialog.busy}
          onChange={(remoteHostCredentials) => {
            dispatch(updateManageConfig({ remoteHostCredentials }));
          }}
        />
      )}
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
      return 'Legacy (derived from remote + snapshot)';
  }
}

function CloudAliasSelect({
  id,
  value,
  options,
  disabled,
  onChange,
}: {
  id: string;
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
        <Label htmlFor={id}>Cloud alias</Label>
        <EmptyState
          icon={<Server />}
          heading="No cloud aliases configured"
          body="Cloud aliases are how ERun connects to your AWS account. Add one in ERun settings to link this environment to a cloud context."
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
  return (
    <SelectField
      id={id}
      label="Cloud alias"
      value={normalizedValue}
      options={selectOptions.map((option) => ({ value: option, label: option }))}
      placeholder="Select cloud alias"
      emptyLabel="No cloud aliases configured"
      disabled={disabled}
      onChange={onChange}
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
