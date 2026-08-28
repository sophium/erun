import {
  Button,
  EmptyState,
  FieldLabel,
  Input,
  Label,
  Popover,
  PopoverContent,
  PopoverTrigger,
} from 'erun-kit';
import { Cloud, LoaderCircle, Plus, RefreshCw } from 'lucide-react';
import * as React from 'react';

import {
  loginGlobalCloudProvider,
  refreshCloudProviders,
  startAWSCloudInit,
  startCloudflareCloudInit,
  startERunCloudInit,
  updateGlobalConfigDialog,
} from '@/app/globalConfigThunks';
import { useAppDispatch } from '@/app/hooks';
import type { AppState } from '@/app/state';
import {
  cloudProviderSummary,
  cloudProviderTypeLabel,
} from '@/components/app/GlobalConfigDialog.helpers';
import { CloudAliasAction, CloudStatusBadge } from '@/components/app/GlobalConfigDialog.shared';
import {
  CloudProviderAWS,
  CloudProviderCloudflare,
  CloudProviderERun,
  type UICloudProviderStatus,
} from '@/types';

type GlobalConfigDialog = AppState['globalConfigDialog'];

const providerGroupOrder = [CloudProviderAWS, CloudProviderCloudflare, CloudProviderERun];

export function CloudAliasesSection({
  dialog,
}: {
  dialog: GlobalConfigDialog;
}): React.ReactElement {
  const dispatch = useAppDispatch();
  const providers = dialog.config.cloudProviders ?? [];
  return (
    <div className="grid gap-2">
      {/* Three labelled add actions plus Refresh do not fit beside the label in
          a narrow dialog, so the row wraps rather than spilling past the card.
          min-w-0 lets the label shrink instead of pinning the row wider than
          its container. */}
      <div className="flex flex-wrap items-center justify-between gap-2">
        <Label className="min-w-0">Cloud aliases</Label>
        <div className="flex flex-wrap justify-end gap-1.5">
          {/* Once at least one alias exists, the header is the add affordance;
              the empty state below owns it while there are none, so the two
              surfaces never both offer the same action at once (see
              CloudAddActions, the one definition both render). */}
          {providers.length > 0 && <CloudAddActions dialog={dialog} />}
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

// CloudAddActions is the single definition of every "add a cloud alias"
// action. Both the header (once aliases exist) and the empty state (before
// any do) render this same list, so a provider type can never be added to one
// surface and forgotten in the other. AWS and Cloudflare hand off to the
// CLI's own guided `erun cloud init` terminal flow; erun collects its one
// required field (the platform API URL) in-app and reaches the same
// connect-and-sign-in code path the tenant dashboard's Connect panel uses.
function CloudAddActions({ dialog }: { dialog: GlobalConfigDialog }): React.ReactElement {
  return (
    <>
      <AddAWSButton dialog={dialog} />
      <AddCloudflareButton dialog={dialog} />
      <AddERunButton dialog={dialog} />
    </>
  );
}

function AddAWSButton({ dialog }: { dialog: GlobalConfigDialog }): React.ReactElement {
  const dispatch = useAppDispatch();
  return (
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
  );
}

function AddCloudflareButton({ dialog }: { dialog: GlobalConfigDialog }): React.ReactElement {
  const dispatch = useAppDispatch();
  return (
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
  );
}

// AddERunButton is the one in-app add form on this surface: unlike AWS and
// Cloudflare (which hand off to a guided CLI terminal), attaching a hosted
// erun platform needs exactly one value the operator already knows — the
// platform's API URL — so a popover collects it without leaving Settings.
function AddERunButton({ dialog }: { dialog: GlobalConfigDialog }): React.ReactElement {
  const dispatch = useAppDispatch();
  const [open, setOpen] = React.useState(false);
  const submitting = dialog.busyAction === 'cloud-provider-erun-init';
  const draft = dialog.erunApiUrlDraft;
  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger asChild>
        <Button type="button" variant="outline" size="sm" disabled={dialog.busy}>
          {submitting ? (
            <LoaderCircle className="animate-spin" aria-hidden="true" />
          ) : (
            <Plus aria-hidden="true" />
          )}
          Add erun platform
        </Button>
      </PopoverTrigger>
      <PopoverContent className="grid w-80 gap-2" align="end">
        <FieldLabel htmlFor="cloud-alias-erun-api-url" required>
          Platform API URL
        </FieldLabel>
        <Input
          id="cloud-alias-erun-api-url"
          placeholder="https://api.<tenant>-prod.services.erunpaas.com"
          value={draft}
          disabled={dialog.busy}
          onChange={(event) => {
            dispatch(updateGlobalConfigDialog({ erunApiUrlDraft: event.target.value }));
          }}
        />
        <Button
          type="button"
          size="sm"
          disabled={dialog.busy || !draft.trim()}
          onClick={() => {
            void dispatch(startERunCloudInit(draft)).then((connected) => {
              if (connected) {
                setOpen(false);
              }
            });
          }}
        >
          {submitting && <LoaderCircle className="animate-spin" aria-hidden="true" />}
          {submitting ? 'Connecting...' : 'Connect'}
        </Button>
      </PopoverContent>
    </Popover>
  );
}

function CloudAliasesEmptyState({ dialog }: { dialog: GlobalConfigDialog }): React.ReactElement {
  return (
    <EmptyState
      icon={<Cloud />}
      heading="No cloud aliases yet"
      body="Add a cloud account so ERun can deploy environments to it: an AWS account for compute, a Cloudflare token for DNS and zone delegation, or an erun platform to connect this machine to a hosted erun platform."
      action={
        <div className="flex flex-wrap justify-center gap-1.5">
          <CloudAddActions dialog={dialog} />
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
