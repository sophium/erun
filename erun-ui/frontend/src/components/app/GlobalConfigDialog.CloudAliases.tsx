import { Cloud, LoaderCircle, Plus, RefreshCw } from 'lucide-react';
import * as React from 'react';

import {
  loginGlobalCloudProvider,
  refreshCloudProviders,
  startAWSCloudInit,
} from '@/app/globalConfigThunks';
import { useAppDispatch } from '@/app/hooks';
import type { AppState } from '@/app/state';
import { EmptyState } from '@/components/app/EmptyState';
import { cloudProviderSummary } from '@/components/app/GlobalConfigDialog.helpers';
import { CloudAliasAction, CloudStatusBadge } from '@/components/app/GlobalConfigDialog.shared';
import { Button } from '@/components/ui/button';
import { Label } from '@/components/ui/label';

type GlobalConfigDialog = AppState['globalConfigDialog'];

export function CloudAliasesSection({
  dialog,
}: {
  dialog: GlobalConfigDialog;
}): React.ReactElement {
  const dispatch = useAppDispatch();
  const providers = dialog.config.cloudProviders ?? [];
  return (
    <div className="grid gap-2">
      <div className="flex items-center justify-between gap-2">
        <Label>Cloud aliases</Label>
        <div className="flex gap-1.5">
          <Button
            type="button"
            variant="outline"
            size="sm"
            disabled={dialog.busy}
            onClick={() => void dispatch(startAWSCloudInit())}
          >
            {dialog.busyAction === 'cloud-provider-init' ? (
              <LoaderCircle className="animate-spin" aria-hidden="true" />
            ) : (
              <Plus aria-hidden="true" />
            )}
            AWS
          </Button>
          <Button
            type="button"
            variant="ghost"
            size="icon"
            disabled={dialog.busy}
            aria-label="Refresh cloud aliases"
            onClick={() => void dispatch(refreshCloudProviders())}
          >
            <RefreshCw aria-hidden="true" />
          </Button>
        </div>
      </div>
      {providers.length === 0 ? (
        <EmptyState
          icon={<Cloud />}
          heading="No cloud aliases yet"
          body="Add a cloud account so ERun can deploy environments to it. AWS is the only provider supported today."
          action={
            <Button
              type="button"
              variant="outline"
              size="sm"
              disabled={dialog.busy}
              onClick={() => void dispatch(startAWSCloudInit())}
            >
              {dialog.busyAction === 'cloud-provider-init' ? (
                <LoaderCircle className="animate-spin" aria-hidden="true" />
              ) : (
                <Plus aria-hidden="true" />
              )}
              Add AWS account
            </Button>
          }
        />
      ) : (
        <CloudAliasList dialog={dialog} />
      )}
    </div>
  );
}

function CloudAliasList({ dialog }: { dialog: GlobalConfigDialog }): React.ReactElement {
  const dispatch = useAppDispatch();
  return (
    <div className="overflow-hidden rounded-[var(--radius)] border border-border">
      {(dialog.config.cloudProviders ?? []).map((provider, index) => (
        <div
          key={provider.alias}
          className="grid grid-cols-[minmax(0,1fr)_auto] items-center gap-3 border-border px-3 py-2.5 data-[border=true]:border-t"
          data-border={index > 0}
          data-cloud-alias={provider.alias}
          data-cloud-status={provider.status}
        >
          <CloudAliasSummary provider={provider} />
          <CloudAliasAction
            status={provider.status}
            busy={dialog.busy}
            loading={
              dialog.busyAction === 'cloud-provider-login' && dialog.busyTarget === provider.alias
            }
            onLogin={() => void dispatch(loginGlobalCloudProvider(provider.alias))}
          />
        </div>
      ))}
    </div>
  );
}

function CloudAliasSummary({
  provider,
}: {
  provider: NonNullable<GlobalConfigDialog['config']['cloudProviders']>[number];
}): React.ReactElement {
  return (
    <div className="grid min-w-0 gap-1">
      <div className="flex min-w-0 items-center gap-2 text-sm font-medium">
        <Cloud className="size-4 shrink-0 text-muted-foreground" aria-hidden="true" />
        <span className="truncate">{provider.alias}</span>
        <CloudStatusBadge status={provider.status} />
      </div>
      <div className="truncate text-xs text-muted-foreground">
        {cloudProviderSummary(provider)}
        {provider.message ? ` - ${provider.message}` : ''}
      </div>
    </div>
  );
}
