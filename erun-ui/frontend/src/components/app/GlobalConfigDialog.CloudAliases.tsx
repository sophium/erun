import { Button, EmptyState, Label } from 'erun-kit';
import { Cloud, LoaderCircle, Plus, RefreshCw } from 'lucide-react';
import * as React from 'react';

import {
  loginGlobalCloudProvider,
  refreshCloudProviders,
  startAWSCloudInit,
  startCloudflareCloudInit,
} from '@/app/globalConfigThunks';
import { useAppDispatch } from '@/app/hooks';
import type { AppState } from '@/app/state';
import {
  cloudProviderSummary,
  cloudProviderTypeLabel,
} from '@/components/app/GlobalConfigDialog.helpers';
import { CloudAliasAction, CloudStatusBadge } from '@/components/app/GlobalConfigDialog.shared';
import { CloudProviderAWS, CloudProviderCloudflare, type UICloudProviderStatus } from '@/types';

type GlobalConfigDialog = AppState['globalConfigDialog'];

const providerGroupOrder = [CloudProviderAWS, CloudProviderCloudflare];

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
          <AddProviderButtons dialog={dialog} />
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
        <CloudAliasesEmptyState dialog={dialog} />
      ) : (
        <GroupedCloudAliasList dialog={dialog} providers={providers} />
      )}
    </div>
  );
}

// Alias creation is delegated to the CLI's guided `erun cloud init` flow, so
// the desktop never hosts its own add-provider form.
function AddProviderButtons({ dialog }: { dialog: GlobalConfigDialog }): React.ReactElement {
  const dispatch = useAppDispatch();
  return (
    <>
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
        variant="outline"
        size="sm"
        disabled={dialog.busy}
        onClick={() => void dispatch(startCloudflareCloudInit())}
      >
        {dialog.busyAction === 'cloud-provider-cloudflare-init' ? (
          <LoaderCircle className="animate-spin" aria-hidden="true" />
        ) : (
          <Plus aria-hidden="true" />
        )}
        Cloudflare
      </Button>
    </>
  );
}

function CloudAliasesEmptyState({ dialog }: { dialog: GlobalConfigDialog }): React.ReactElement {
  const dispatch = useAppDispatch();
  return (
    <EmptyState
      icon={<Cloud />}
      heading="No cloud aliases yet"
      body="Add a cloud account so ERun can deploy environments to it. Add an AWS account for compute, or a Cloudflare token for DNS and zone delegation."
      action={
        <div className="flex flex-wrap justify-center gap-1.5">
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
          <Button
            type="button"
            variant="outline"
            size="sm"
            disabled={dialog.busy}
            onClick={() => void dispatch(startCloudflareCloudInit())}
          >
            {dialog.busyAction === 'cloud-provider-cloudflare-init' ? (
              <LoaderCircle className="animate-spin" aria-hidden="true" />
            ) : (
              <Plus aria-hidden="true" />
            )}
            Add Cloudflare token
          </Button>
        </div>
      }
    />
  );
}

function GroupedCloudAliasList({
  dialog,
  providers,
}: {
  dialog: GlobalConfigDialog;
  providers: UICloudProviderStatus[];
}): React.ReactElement {
  const groups = groupProvidersByType(providers);
  return (
    <div className="grid gap-3">
      {groups.map((group) => (
        <div key={group.provider} className="grid gap-1.5">
          <div
            className="text-xs font-medium text-muted-foreground"
            data-cloud-alias-group={group.provider}
          >
            {cloudProviderTypeLabel(group.provider)}
          </div>
          <div className="overflow-hidden rounded-[var(--radius)] border border-border">
            {group.aliases.map((provider, index) => (
              <CloudAliasRow
                key={provider.alias}
                dialog={dialog}
                provider={provider}
                bordered={index > 0}
              />
            ))}
          </div>
        </div>
      ))}
    </div>
  );
}

interface ProviderGroup {
  provider: string;
  aliases: UICloudProviderStatus[];
}

function groupProvidersByType(providers: UICloudProviderStatus[]): ProviderGroup[] {
  const byType = new Map<string, UICloudProviderStatus[]>();
  for (const provider of providers) {
    const key = (provider.provider || '').trim().toLowerCase() || 'other';
    const bucket = byType.get(key) ?? [];
    bucket.push(provider);
    byType.set(key, bucket);
  }
  const ordered: ProviderGroup[] = [];
  for (const type of providerGroupOrder) {
    const aliases = byType.get(type);
    if (aliases) {
      ordered.push({ provider: type, aliases });
      byType.delete(type);
    }
  }
  for (const key of [...byType.keys()].sort()) {
    ordered.push({ provider: key, aliases: byType.get(key) ?? [] });
  }
  return ordered;
}

function CloudAliasRow({
  dialog,
  provider,
  bordered,
}: {
  dialog: GlobalConfigDialog;
  provider: UICloudProviderStatus;
  bordered: boolean;
}): React.ReactElement {
  const dispatch = useAppDispatch();
  const isCloudflare = (provider.provider || '').trim().toLowerCase() === CloudProviderCloudflare;
  return (
    <div
      className="grid grid-cols-[minmax(0,1fr)_auto] items-center gap-3 border-border px-3 py-2.5 data-[border=true]:border-t"
      data-border={bordered}
      data-cloud-alias={provider.alias}
      data-cloud-status={provider.status}
      data-cloud-provider={provider.provider}
    >
      <CloudAliasSummary provider={provider} />
      <CloudAliasAction
        status={provider.status}
        busy={dialog.busy}
        loading={
          dialog.busyAction === 'cloud-provider-login' && dialog.busyTarget === provider.alias
        }
        // Cloudflare has no browser SSO — its "login" re-verifies the stored
        // token — so the action reads "Verify token" rather than "Login".
        loginLabel={isCloudflare ? 'Verify token' : undefined}
        loadingLabel={isCloudflare ? 'Verifying...' : undefined}
        onLogin={() => void dispatch(loginGlobalCloudProvider(provider.alias))}
      />
    </div>
  );
}

function CloudAliasSummary({ provider }: { provider: UICloudProviderStatus }): React.ReactElement {
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
