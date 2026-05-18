import { LoaderCircle, Plus, RefreshCw, Server } from 'lucide-react';
import * as React from 'react';

import {
  initGlobalCloudContext,
  refreshCloudContexts,
  startGlobalCloudContext,
  stopGlobalCloudContext,
  updateCloudContextDraft,
} from '@/app/globalConfigThunks';
import { useAppDispatch } from '@/app/hooks';
import type { AppState } from '@/app/state';
import { EmptyState } from '@/components/app/EmptyState';
import {
  cloudContextSummary,
  cloudRegionLabel,
  generatedContextName,
} from '@/components/app/GlobalConfigDialog.helpers';
import { CloudContextAction, CloudStatusBadge } from '@/components/app/GlobalConfigDialog.shared';
import { SelectField } from '@/components/app/SelectField';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';

type GlobalConfigDialog = AppState['globalConfigDialog'];

export function CloudContextsSection({
  dialog,
}: {
  dialog: GlobalConfigDialog;
}): React.ReactElement {
  const dispatch = useAppDispatch();
  const contexts = dialog.config.cloudContexts ?? [];
  return (
    <div className="grid gap-2">
      <div className="flex items-center justify-between gap-2">
        <Label>Cloud contexts</Label>
        <Button
          type="button"
          variant="ghost"
          size="icon"
          disabled={dialog.busy}
          aria-label="Refresh cloud contexts"
          onClick={() => void dispatch(refreshCloudContexts())}
        >
          <RefreshCw aria-hidden="true" />
        </Button>
      </div>
      <CloudContextDraftForm dialog={dialog} />
      {contexts.length === 0 ? (
        <EmptyState
          icon={<Server />}
          heading="No cloud contexts yet"
          body="Pick a cloud alias and region above, then click Init to provision a new context. Contexts are reusable across environments."
        />
      ) : (
        <CloudContextList dialog={dialog} />
      )}
    </div>
  );
}

function CloudContextDraftForm({ dialog }: { dialog: GlobalConfigDialog }): React.ReactElement {
  const dispatch = useAppDispatch();
  const config = dialog.config;
  const generated = generatedContextName(
    (config.cloudProviders ?? []).find(
      (provider) => provider.alias === dialog.cloudContextDraft.cloudProviderAlias,
    ),
    dialog.cloudContextDraft.region,
    config.cloudContexts ?? [],
  );
  return (
    <div className="grid gap-2 rounded-[var(--radius)] border border-border p-3">
      <div className="grid gap-2 sm:grid-cols-2">
        <SelectField
          id="global-config-cloudcontext-provider"
          label="Cloud provider"
          value={dialog.cloudContextDraft.cloudProviderAlias}
          options={(config.cloudProviders ?? []).map((provider) => ({
            value: provider.alias,
            label: provider.alias,
          }))}
          emptyLabel="No cloud aliases"
          placeholder="Select cloud alias"
          disabled={dialog.busy}
          onChange={(cloudProviderAlias) => {
            dispatch(updateCloudContextDraft({ cloudProviderAlias }));
          }}
        />
        <SelectField
          id="global-config-cloudcontext-region"
          label="Region"
          value={dialog.cloudContextDraft.region || 'eu-west-2'}
          options={[
            { value: 'eu-west-2', label: cloudRegionLabel('eu-west-2') },
            { value: 'eu-west-1', label: cloudRegionLabel('eu-west-1') },
          ]}
          disabled={dialog.busy}
          onChange={(region) => {
            dispatch(updateCloudContextDraft({ region }));
          }}
        />
        <SelectField
          id="global-config-cloudcontext-instancetype"
          label="Instance type"
          value={dialog.cloudContextDraft.instanceType}
          options={[
            { value: 'c8gd.2xlarge', label: 'c8gd.2xlarge' },
            { value: 't4g.xlarge', label: 't4g.xlarge' },
          ]}
          disabled={dialog.busy}
          onChange={(instanceType) => {
            dispatch(updateCloudContextDraft({ instanceType }));
          }}
        />
        <SelectField
          id="global-config-cloudcontext-disksize"
          label="Disk size"
          value={String(dialog.cloudContextDraft.diskSizeGb)}
          options={[
            { value: '100', label: '100' },
            { value: '200', label: '200' },
          ]}
          disabled={dialog.busy}
          onChange={(diskSizeGb) => {
            dispatch(updateCloudContextDraft({ diskSizeGb: Number(diskSizeGb) }));
          }}
        />
      </div>
      <p className="text-[12px] leading-[1.4] text-muted-foreground">
        Region, instance type, and disk size are common choices vetted for ERun. Contact an admin to
        expand the list.
      </p>
      <CloudContextNameField dialog={dialog} generatedName={generated} />
    </div>
  );
}

function CloudContextNameField({
  dialog,
  generatedName,
}: {
  dialog: GlobalConfigDialog;
  generatedName: string;
}): React.ReactElement {
  const dispatch = useAppDispatch();
  return (
    <div className="grid gap-2">
      <Label htmlFor="global-config-cloudcontext-name">Context name</Label>
      <div className="grid gap-2 sm:grid-cols-[minmax(0,1fr)_auto] sm:items-center">
        <Input
          id="global-config-cloudcontext-name"
          value={dialog.cloudContextDraft.name}
          disabled={dialog.busy}
          placeholder="Generated when empty"
          onChange={(event) => {
            dispatch(updateCloudContextDraft({ name: event.target.value }));
          }}
        />
        <Button
          type="button"
          size="sm"
          disabled={
            dialog.busy ||
            dialog.configLoading ||
            !dialog.cloudContextDraft.cloudProviderAlias ||
            !dialog.cloudContextDraft.region
          }
          onClick={() => void dispatch(initGlobalCloudContext())}
        >
          {dialog.busyAction === 'cloud-context-init' ? (
            <LoaderCircle className="animate-spin" aria-hidden="true" />
          ) : (
            <Plus aria-hidden="true" />
          )}
          Init
        </Button>
      </div>
      {generatedName && !dialog.cloudContextDraft.name && (
        <div className="px-0.5 text-xs leading-[1.35] text-muted-foreground [overflow-wrap:anywhere]">
          Generated: {generatedName}
        </div>
      )}
    </div>
  );
}

function CloudContextList({ dialog }: { dialog: GlobalConfigDialog }): React.ReactElement {
  const dispatch = useAppDispatch();
  return (
    <div className="overflow-hidden rounded-[var(--radius)] border border-border">
      {(dialog.config.cloudContexts ?? []).map((context, index) => (
        <div
          key={context.name}
          className="grid grid-cols-[minmax(0,1fr)_auto] items-center gap-3 border-border px-3 py-2.5 data-[border=true]:border-t"
          data-border={index > 0}
          data-cloud-context={context.name}
          data-cloud-context-status={context.status}
        >
          <CloudContextSummary context={context} />
          <CloudContextAction
            status={context.status}
            busy={dialog.busy}
            loading={
              dialog.busyAction === 'cloud-context-power' && dialog.busyTarget === context.name
            }
            onStart={() => void dispatch(startGlobalCloudContext(context.name))}
            onStop={() => void dispatch(stopGlobalCloudContext(context.name))}
          />
        </div>
      ))}
    </div>
  );
}

function CloudContextSummary({
  context,
}: {
  context: NonNullable<GlobalConfigDialog['config']['cloudContexts']>[number];
}): React.ReactElement {
  return (
    <div className="grid min-w-0 gap-1">
      <div className="flex min-w-0 items-center gap-2 text-sm font-medium">
        <Server className="size-4 shrink-0 text-muted-foreground" aria-hidden="true" />
        <span className="truncate">{context.kubernetesContext || context.name}</span>
        <CloudStatusBadge status={context.status} />
      </div>
      <div className="truncate text-xs text-muted-foreground">
        {cloudContextSummary(context)}
        {context.message ? ` - ${context.message}` : ''}
      </div>
    </div>
  );
}
