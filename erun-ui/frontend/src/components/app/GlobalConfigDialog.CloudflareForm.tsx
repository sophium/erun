import { LoaderCircle, Plus, X } from 'lucide-react';
import * as React from 'react';

import {
  closeCloudflareCloudInitForm,
  submitCloudflareCloudInit,
  updateCloudflareDraft,
} from '@/app/cloudflareProviderThunks';
import { useAppDispatch } from '@/app/hooks';
import type { AppState } from '@/app/state';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';

type GlobalConfigDialog = AppState['globalConfigDialog'];

// CloudflareAliasForm is the non-interactive "add Cloudflare token" form. It
// collects the three explicit inputs erun-common's InitCloudflareCloudProvider
// needs — account ID, a token label, and the scoped API token — and submits
// them in one masked round-trip. The API token uses type="password" so it is
// never shoulder-surfed or echoed; the backend stores it off-config and never
// returns it. The submit button stays disabled until all three are present
// (error prevention, Nielsen #5).
export function CloudflareAliasForm({
  dialog,
}: {
  dialog: GlobalConfigDialog;
}): React.ReactElement {
  const dispatch = useAppDispatch();
  const draft = dialog.cloudflareDraft;
  const submitting = dialog.busyAction === 'cloud-provider-cloudflare-init';
  const complete =
    draft.accountId.trim() !== '' && draft.tokenName.trim() !== '' && draft.apiToken.trim() !== '';
  return (
    <form
      className="grid gap-2 rounded-[var(--radius)] border border-border p-3"
      aria-label="Add Cloudflare token"
      onSubmit={(event) => {
        event.preventDefault();
        if (complete) {
          void dispatch(submitCloudflareCloudInit());
        }
      }}
    >
      <div className="flex items-center justify-between gap-2">
        <div className="text-sm font-medium">Add Cloudflare token</div>
        <Button
          type="button"
          variant="ghost"
          size="icon"
          disabled={dialog.busy}
          aria-label="Cancel adding Cloudflare token"
          onClick={() => {
            dispatch(closeCloudflareCloudInitForm());
          }}
        >
          <X aria-hidden="true" />
        </Button>
      </div>
      <CloudflareField
        id="global-config-cloudflare-accountid"
        label="Account ID"
        value={draft.accountId}
        placeholder="Cloudflare account ID"
        disabled={dialog.busy}
        onChange={(accountId) => {
          dispatch(updateCloudflareDraft({ accountId }));
        }}
      />
      <CloudflareField
        id="global-config-cloudflare-tokenname"
        label="Token label"
        value={draft.tokenName}
        placeholder="A name to recognize this token"
        disabled={dialog.busy}
        onChange={(tokenName) => {
          dispatch(updateCloudflareDraft({ tokenName }));
        }}
      />
      <CloudflareField
        id="global-config-cloudflare-apitoken"
        label="API token"
        type="password"
        value={draft.apiToken}
        placeholder="Scoped API token (Zone:Edit + DNS:Edit)"
        disabled={dialog.busy}
        onChange={(apiToken) => {
          dispatch(updateCloudflareDraft({ apiToken }));
        }}
      />
      <p className="text-[12px] leading-[1.4] text-muted-foreground">
        Mint an account-scoped token (account-level Zone:Edit + DNS:Edit) in the Cloudflare
        dashboard. ERun verifies it, then stores it on this machine outside the config file.
      </p>
      <div className="flex justify-end gap-1.5">
        <Button type="submit" size="sm" disabled={dialog.busy || !complete}>
          {submitting ? (
            <LoaderCircle className="animate-spin" aria-hidden="true" />
          ) : (
            <Plus aria-hidden="true" />
          )}
          {submitting ? 'Verifying...' : 'Add token'}
        </Button>
      </div>
    </form>
  );
}

function CloudflareField({
  id,
  label,
  value,
  placeholder,
  disabled,
  type,
  onChange,
}: {
  id: string;
  label: string;
  value: string;
  placeholder?: string;
  disabled?: boolean;
  type?: 'text' | 'password';
  onChange: (value: string) => void;
}): React.ReactElement {
  return (
    <div className="grid gap-1.5">
      <Label htmlFor={id}>{label}</Label>
      <Input
        id={id}
        type={type ?? 'text'}
        value={value}
        placeholder={placeholder}
        disabled={disabled}
        autoComplete={type === 'password' ? 'off' : undefined}
        onChange={(event) => {
          onChange(event.target.value);
        }}
      />
    </div>
  );
}
