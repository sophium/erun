import { LoaderCircle, Play, Power, Server } from 'lucide-react';
import * as React from 'react';

import { useAppDispatch, useAppSelector } from '@/app/hooks';
import {
  startManageCloudContext,
  stopManageCloudContext,
  updateManageConfig,
} from '@/app/manageEnvironmentThunks';
import { loadSavedPastContainerRegistries } from '@/app/storage';
import { EditableComboField } from '@/components/app/EditableComboField';
import { uniqueSuggestions } from '@/components/app/EditableComboField.helpers';
import { CheckboxField, ReadonlyField, StatusBadge } from '@/components/app/ManageDialog.fields';
import { SelectField } from '@/components/app/SelectField';
import { Button } from '@/components/ui/button';
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
        id="environment-config-remote"
        label="Remote environment"
        value={config.remote ? 'Yes' : 'No'}
      />
      <CheckboxField
        id="environment-config-snapshot"
        label="Snapshot deploy"
        checked={config.snapshot}
        disabled={dialog.busy}
        onChange={(snapshot) => {
          dispatch(updateManageConfig({ snapshot }));
        }}
      />
    </>
  );
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
  const normalizedValue = value.trim();
  const normalizedOptions = options.map((option) => option.trim()).filter(Boolean);
  const selectOptions =
    normalizedValue && !normalizedOptions.includes(normalizedValue)
      ? [normalizedValue, ...normalizedOptions]
      : normalizedOptions;
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
    return (
      <div className="grid gap-2">
        <div className="text-sm font-medium leading-none">Cloud context</div>
        <div className="rounded-[var(--radius)] border border-dashed border-border px-3 py-2.5 text-[13px] leading-[1.35] text-muted-foreground">
          {cloudProviderAlias.trim() ? 'No linked cloud context' : 'Not linked'}
        </div>
      </div>
    );
  }
  const running = context.status.trim() === 'running';
  return (
    <div className="grid gap-2">
      <div className="text-sm font-medium leading-none">Cloud context</div>
      <div className="grid grid-cols-[minmax(0,1fr)_auto] items-center gap-3 rounded-[var(--radius)] border border-border px-3 py-2.5">
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
